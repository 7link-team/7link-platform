package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// reminderProject selects the thread scope; zero means tenant relation scope, never an entity.
func reminderProject(project *guid.ID) guid.ID {
	if project != nil {
		return *project
	}
	return 0
}

const reminderLimit = 50

const reminderCancelled = "CANCELLED" //nolint:misspell // Spec 05 ReminderState wire spelling.

// ReminderCalendar computes occurrences from planning's current team calendar.
type ReminderCalendar func(context.Context, *gorm.DB, guid.ID, string, time.Time) (time.Time, string, error)
type ReminderCalendarConnector struct{ Bind func(ReminderCalendar) error }

func (m *Module) provideReminderCalendar(s *module.Services) error {
	return module.Provide[ReminderCalendarConnector](s, ReminderCalendarConnector{Bind: func(next ReminderCalendar) error {
		if next == nil || m.reminderCalendar != nil {
			return ErrConflict
		}
		m.reminderCalendar = next
		return nil
	}})
}
func (m *Module) injectReminderWork(s *module.Services) error {
	v, err := module.Resolve[delivery.ReminderWorkAccess](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	m.reminderWork = v
	return err
}

type ReminderInput struct {
	AnchorKind   string   `json:"anchor_kind,omitempty" enum:"THREAD,WORK_ITEM"`
	WorkItemID   *guid.ID `json:"work_item_id,omitempty"`
	ScheduleKind string   `json:"schedule_kind,omitempty" enum:"ONCE,INTERVAL,PERIOD"`
	PeriodKind   string   `json:"period_kind,omitempty" enum:",DAY,WEEK,MONTH,QUARTER,YEAR"`

	ThreadID        guid.ID   `json:"thread_id,omitempty"`
	ProjectID       *guid.ID  `json:"project_id,omitempty"`
	Title           string    `json:"title" minLength:"1" maxLength:"200"`
	Note            string    `json:"note" maxLength:"2000"`
	DueAt           time.Time `json:"due_at,omitempty"`
	IntervalMinutes int       `json:"interval_minutes" minimum:"0" maximum:"10080"`
	MaxFires        int       `json:"max_fires" minimum:"1" maximum:"1000"`
	Checks          string    `json:"checks" maxLength:"2000"`
	BudgetMinor     int64     `json:"budget_minor" minimum:"0" doc:"Maximum budget in minor currency units per run; a contract, not a spending grant."`
	Escalation      string    `json:"escalation" maxLength:"2000"`
	OutputFormat    string    `json:"output_format" maxLength:"2000"`
}
type ReminderView struct {
	ID guid.ID `json:"id"`
	ReminderInput
	Timezone  string `json:"timezone"`
	State     string `json:"state"`
	FireCount int    `json:"fire_count"`
	Version   int    `json:"version"`
	Next      []Next `json:"next"`
}

func reminderView(r *model.Reminder) ReminderView {
	v := ReminderView{ID: r.ID, ReminderInput: ReminderInput{AnchorKind: r.AnchorKind, WorkItemID: r.WorkItemID, ScheduleKind: r.ScheduleKind, PeriodKind: r.PeriodKind, ThreadID: r.ThreadID, ProjectID: r.ProjectID, Title: r.Title, Note: r.Note, IntervalMinutes: r.IntervalMinutes, MaxFires: r.MaxFires, Checks: r.Checks, BudgetMinor: r.BudgetMinor, Escalation: r.Escalation, OutputFormat: r.OutputFormat}, Timezone: r.Timezone, State: r.State, FireCount: r.FireCount, Version: r.Version, Next: []Next{{Operation: "inbox_events.list"}}}
	if r.NextFireAt != nil {
		v.DueAt = *r.NextFireAt
	}
	return v
}
func (m *Module) reminderPermission(ctx context.Context, agent guid.ID) string {
	if actorID(ctx) == agent {
		return "SELF"
	}
	return "agent_manage"
}
func (m *Module) reminderOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in ReminderInput) (ReminderView, error) {
	ctx = reminderMutationContext(ctx)
	var out ReminderView
	if in.AnchorKind == "" {
		in.AnchorKind = "THREAD"
	}
	if in.ScheduleKind == "" {
		in.ScheduleKind = "ONCE"
		if in.IntervalMinutes > 0 {
			in.ScheduleKind = "INTERVAL"
		}
	}
	if !slices.Contains([]string{"THREAD", "WORK_ITEM"}, in.AnchorKind) || (in.AnchorKind == "THREAD" && (in.ThreadID <= 0 || in.WorkItemID != nil)) || (in.AnchorKind == "WORK_ITEM" && (in.WorkItemID == nil || *in.WorkItemID <= 0 || in.ThreadID != 0 || in.ProjectID == nil)) {
		return out, ErrInvalid
	}
	if !slices.Contains([]string{"ONCE", "INTERVAL", "PERIOD"}, in.ScheduleKind) || (in.ScheduleKind == "INTERVAL" && in.IntervalMinutes == 0) || (in.ScheduleKind != "INTERVAL" && in.IntervalMinutes != 0) || (in.ScheduleKind != "PERIOD" && in.PeriodKind != "") || (in.ScheduleKind == "PERIOD" && (!in.DueAt.IsZero() || !slices.Contains([]string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"}, in.PeriodKind))) {
		return out, ErrInvalid
	}
	timezone := ""
	if in.ScheduleKind == "PERIOD" {
		if m.reminderCalendar == nil {
			return out, ErrUnavailable
		}
		var err error
		in.DueAt, timezone, err = m.reminderCalendar(ctx, tx, tenant, in.PeriodKind, m.now())
		if err != nil {
			return out, err
		}
	}
	if (in.ProjectID != nil && *in.ProjectID <= 0) || strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 200 || utf8.RuneCountInString(in.Note) > 2000 || in.DueAt.Before(m.now()) || in.IntervalMinutes != 0 && (in.IntervalMinutes < 15 || in.IntervalMinutes > 10080) || in.MaxFires < 1 || in.MaxFires > 1000 || in.BudgetMinor < 0 {
		return out, ErrInvalid
	}
	for _, s := range []string{in.Checks, in.Escalation, in.OutputFormat} {
		if utf8.RuneCountInString(s) > 2000 {
			return out, ErrInvalid
		}
	}
	if in.IntervalMinutes > 0 && (strings.TrimSpace(in.Checks) == "" || strings.TrimSpace(in.Escalation) == "" || strings.TrimSpace(in.OutputFormat) == "" || in.BudgetMinor < 1) {
		return out, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, m.reminderPermission(ctx, agent), false); err != nil {
		return out, err
	}
	if m.ids == nil {
		return out, ErrUnavailable
	}

	if _, err := m.reminderAnchorOn(ctx, tx, tenant, agent, &model.Reminder{AnchorKind: in.AnchorKind, WorkItemID: in.WorkItemID, ProjectID: in.ProjectID, ThreadID: in.ThreadID}); err != nil {
		return out, ErrForbidden
	}
	if err := m.checkReminderQuotaOn(ctx, tx, tenant, agent); err != nil {
		return out, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	due := in.DueAt.UTC().Truncate(time.Millisecond)
	r := model.Reminder{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: agent, AnchorKind: in.AnchorKind, WorkItemID: in.WorkItemID, ScheduleKind: in.ScheduleKind, PeriodKind: in.PeriodKind, Timezone: timezone, ThreadID: in.ThreadID, ProjectID: in.ProjectID, Title: strings.TrimSpace(in.Title), Note: in.Note, NextFireAt: &due, IntervalMinutes: in.IntervalMinutes, MaxFires: in.MaxFires, State: "ACTIVE", Checks: in.Checks, BudgetMinor: in.BudgetMinor, Escalation: in.Escalation, OutputFormat: in.OutputFormat, Version: 1, CreatedAt: m.now()}
	if err = query.BindReminder(tx, m.names).WithContext(ctx).Create(&r); err != nil {
		return out, err
	}
	if err = m.record(ctx, tx, tenant, agent, "REMINDER_CREATED", actorID(ctx) != agent); err != nil {
		return out, err
	}
	return reminderView(&r), nil
}
func (m *Module) remindersOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, after guid.ID, limit int) ([]ReminderView, bool, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, false, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, m.reminderPermission(ctx, agent), false); err != nil {
		return nil, false, err
	}
	q := query.BindReminder(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Gt(after)).Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return nil, false, err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := []ReminderView{}
	for _, r := range rows {
		if _, err = m.reminderAnchorOn(ctx, tx, tenant, agent, r); err != nil {
			return nil, false, ErrForbidden
		}
		out = append(out, reminderView(r))
	}
	return out, more, nil
}
func (m *Module) changeReminderOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID, version int, state string) (ReminderView, error) {
	ctx = reminderMutationContext(ctx)
	if version < 1 || !slices.Contains([]string{"ACTIVE", "PAUSED", reminderCancelled}, state) {
		return ReminderView{}, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, agent, m.reminderPermission(ctx, agent), false); err != nil {
		return ReminderView{}, err
	}
	if err := m.checkReminderAccess(ctx, tx, tenant, agent, id); err != nil {
		return ReminderView{}, err
	}
	q := query.BindReminder(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Eq(id)).First()
	if err != nil {
		return ReminderView{}, err
	}
	if r.Version != version || r.State == "DONE" || r.State == reminderCancelled {
		return ReminderView{}, ErrConflict
	}
	updates := map[string]any{"state": state, "version": version + 1}
	if r.State == "PAUSED" && state == "ACTIVE" {
		due := m.now()
		if r.ScheduleKind == "PERIOD" {
			if m.reminderCalendar == nil {
				return ReminderView{}, ErrUnavailable
			}
			due, r.Timezone, err = m.reminderCalendar(ctx, tx, tenant, r.PeriodKind, due)
			if err != nil {
				return ReminderView{}, err
			}
			updates["timezone"] = r.Timezone
		} else if r.IntervalMinutes > 0 {
			due = due.Add(time.Duration(r.IntervalMinutes) * time.Minute)
		}
		r.NextFireAt = &due
		updates["next_fire_at"] = due
	}
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(updates)
	if err != nil {
		return ReminderView{}, err
	}
	if res.RowsAffected != 1 {
		return ReminderView{}, ErrConflict
	}
	r.State = state
	r.Version++
	if err = m.record(ctx, tx, tenant, agent, "REMINDER_CHANGED", actorID(ctx) != agent); err != nil {
		return ReminderView{}, err
	}
	return reminderView(r), nil
}
func (m *Module) fireReminderOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, now time.Time) error {
	q := query.BindReminder(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if r.State != "ACTIVE" || r.NextFireAt == nil || r.NextFireAt.After(now) {
		return nil
	}
	// Recheck the target's current IAM/channel access at delivery time. A revoked
	// membership must not receive an anchor or revive its own permissions.
	agentCtx := authn.WithClaims(ctx, &authn.Claims{PrincipalID: r.AgentPrincipalID, TenantID: tenant, SubjectType: authn.SubjectTypePrincipal})
	if _, err = m.authorize(agentCtx, tx, tenant, r.AgentPrincipalID, "SELF", false); err != nil {
		return err
	}
	seq, err := m.reminderAnchorOn(agentCtx, tx, tenant, r.AgentPrincipalID, r)
	if err != nil {
		return err
	}
	live, err := m.lockState(ctx, tx, tenant, r.AgentPrincipalID)
	if err != nil {
		return err
	}
	_ = live
	next := r.NextFireAt
	state := "DONE"
	fires := 1
	if r.IntervalMinutes > 0 {
		step := time.Duration(r.IntervalMinutes) * time.Minute
		fires = int(now.Sub(*r.NextFireAt)/step) + 1
		n := r.NextFireAt.Add(time.Duration(fires) * step)
		next = &n
		if r.FireCount+fires < r.MaxFires {
			state = "ACTIVE"
		}
	}
	if r.ScheduleKind == "PERIOD" && r.FireCount+1 < r.MaxFires {
		if m.reminderCalendar == nil {
			return ErrUnavailable
		}
		n, zone, e := m.reminderCalendar(ctx, tx, tenant, r.PeriodKind, now)
		if e != nil {
			return e
		}
		next = &n
		state = "ACTIVE"
		r.Timezone = zone
	}
	count := min(r.FireCount+fires, r.MaxFires)
	if state == "DONE" {
		next = nil
	}
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(r.Version), q.State.Eq("ACTIVE")).Updates(map[string]any{"timezone": r.Timezone, "next_fire_at": next, "fire_count": count, "state": state, "version": r.Version + 1})
	if err != nil {
		return err
	}
	if res.RowsAffected != 1 {
		return nil
	}
	if skip, e := m.observeLoopFireOn(ctx, tx, tenant, id, count, *r.NextFireAt); e != nil || skip {
		return e
	}
	_, err = m.appendOn(ctx, tx, tenant, r.AgentPrincipalID, InboxReference{SourceEventID: fmt.Sprintf("reminder:%s:%d", id.String(), count), Kind: "REMINDER", SubjectID: id, StreamKey: reminderStream(r), StreamSeq: seq})
	return err
}
func reminderStream(r *model.Reminder) string {
	if r.AnchorKind == "WORK_ITEM" && r.WorkItemID != nil {
		return "work_item:" + r.WorkItemID.String()
	}
	return "thread:" + r.ThreadID.String()
}
func (m *Module) reminderAnchorOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, r *model.Reminder) (int64, error) {
	if r.AnchorKind == "WORK_ITEM" {
		if r.WorkItemID == nil || r.ProjectID == nil {
			return 0, ErrInvalid
		}
		if m.reminderWork.CheckOn == nil {
			return 0, ErrUnavailable
		}
		seq, err := m.reminderWork.CheckOn(ctx, tx, tenant, *r.ProjectID, *r.WorkItemID, actorID(ctx))
		if err != nil {
			return 0, ErrForbidden
		}
		if actorID(ctx) != agent {
			targetCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: agent, SubjectType: authn.SubjectTypePrincipal})
			seq, err = m.reminderWork.CheckOn(targetCtx, tx, tenant, *r.ProjectID, *r.WorkItemID, agent)
			if err != nil {
				return 0, ErrForbidden
			}
		}
		return seq, nil
	}
	if m.threads.CheckOn == nil {
		return 0, ErrUnavailable
	}
	return m.threads.CheckOn(ctx, tx, tenant, reminderProject(r.ProjectID), r.ThreadID, agent)
}
func (m *Module) checkReminderAccess(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID) error {
	if m.threads.CheckOn == nil && m.reminderWork.CheckOn == nil {
		return ErrUnavailable
	}
	q := query.BindReminder(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	_, err = m.reminderAnchorOn(ctx, tx, tenant, agent, r)
	return err
}
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: "agents.reminder_fire", Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		db, err := m.database()
		if err != nil {
			return err
		}
		now := m.now()
		var failures []error
		var after guid.ID
		// Bounded keyset batches continue past individually inaccessible reminders.
		// Keep their state intact so restoring membership can make them deliverable.
		for {
			if err = ctx.Err(); err != nil {
				return errors.Join(append(failures, err)...)
			}
			q := query.BindReminder(db, m.names)
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Gt(after), q.State.Eq("ACTIVE"), q.NextFireAt.Lte(now)).Order(q.ID).Limit(500).Find()
			if e != nil {
				return errors.Join(append(failures, e)...)
			}
			for _, r := range rows {
				if e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, job.TenantID, r.ID, now) }); e != nil {
					failures = append(failures, e)
				}
			}
			if len(rows) < 500 {
				break
			}
			after = rows[len(rows)-1].ID
		}
		return errors.Join(failures...)
	}}, m.wakeJob(), m.retirementJob()}
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "agents.reminder_fire", Kind: "agents.reminder_fire", Interval: 30 * time.Second, TenantScope: jobs.ScopePerTenant}, {Name: "agents.webhook_wake", Kind: "agents.webhook_wake", Interval: time.Second, TenantScope: jobs.ScopePerTenant}, {Name: "agents.retirement_progress", Kind: "agents.retirement_progress", Interval: 30 * time.Second, TenantScope: jobs.ScopePerTenant}}
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
var _ module.Module = (*Module)(nil)

func (m *Module) checkReminderInputAccess(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, in ReminderInput) error {
	_, err := m.reminderAnchorOn(ctx, tx, tenant, agent, &model.Reminder{AnchorKind: in.AnchorKind, WorkItemID: in.WorkItemID, ProjectID: in.ProjectID, ThreadID: in.ThreadID})
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return ErrUnavailable
		}
		return ErrForbidden
	}
	return nil
}

// All reminder producers serialize against the same Agent state row. A locking
// read observes current rows even under a previously established MySQL snapshot.
func (m *Module) checkReminderQuotaOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) error {
	if _, err := m.lockState(ctx, tx, tenant, agent); err != nil {
		return err
	}
	q := query.BindReminder(tx, m.names)
	rows, err := q.WithContext(ctx).Select(q.ID).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.State.In("ACTIVE", "PAUSED")).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(reminderLimit).Find()
	if err != nil {
		return err
	}
	if len(rows) >= reminderLimit {
		return ErrConflict
	}
	return nil
}
