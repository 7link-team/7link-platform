package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"time"
)

// LoopFireObserver runs inside the reminder fire transaction. It returns true
// when a reminder belongs to a loop whose overlapping delivery must be skipped.
type LoopFireObserver func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error)
type LoopReminderInput struct {
	AgentID, ThreadID, ReminderID guid.ID
	ProjectID                     *guid.ID
	Name                          string
	IntervalMinutes               int
	DueAt                         *time.Time
}

// ReminderLoops adapts existing reminders; it does not start agent executions.
// Calendar owners can provide an exact one-shot DueAt and create a successor on fire.
type ReminderLoops struct {
	UpsertOn func(context.Context, *gorm.DB, guid.ID, LoopReminderInput) (guid.ID, error)
	ChangeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error
	Bind     func(LoopFireObserver) error
}

func (m *Module) provideLoopReminders(s *module.Services) error {
	return module.Provide[ReminderLoops](s, ReminderLoops{UpsertOn: m.upsertLoopReminderOn, ChangeOn: m.changeLoopReminderOn, Bind: func(observer LoopFireObserver) error {
		if observer == nil || m.loopObserver != nil {
			return ErrConflict
		}
		m.loopObserver = observer
		return nil
	}})
}
func (m *Module) upsertLoopReminderOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in LoopReminderInput) (guid.ID, error) {
	if tx == nil || tx.Statement == nil || in.AgentID <= 0 || (in.ProjectID != nil && *in.ProjectID <= 0) || in.ThreadID <= 0 || (in.DueAt == nil && (in.IntervalMinutes < 15 || in.IntervalMinutes > 10080)) || (in.DueAt != nil && (in.IntervalMinutes != 0 || !in.DueAt.After(m.now()))) || in.Name == "" {
		return 0, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalid
	}
	if m.loopObserver == nil || m.threads.CheckOn == nil || m.ids == nil {
		return 0, ErrUnavailable
	}
	if _, e := m.authorize(ctx, tx, tenant, in.AgentID, m.reminderPermission(ctx, in.AgentID), false); e != nil {
		return 0, e
	}
	if _, e := m.threads.CheckOn(ctx, tx, tenant, reminderProject(in.ProjectID), in.ThreadID, in.AgentID); e != nil {
		return 0, e
	}
	q := query.BindReminder(tx, m.names)
	now := m.now()
	due := now.Add(time.Duration(in.IntervalMinutes) * time.Minute)
	maxFires := 1000
	scheduleKind := "INTERVAL"
	if in.DueAt != nil {
		scheduleKind = "ONCE"
		due = in.DueAt.UTC()
		maxFires = 1
	}
	if in.ReminderID > 0 {
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(in.ReminderID), q.AgentPrincipalID.Eq(in.AgentID)).First()
		if e != nil {
			return 0, e
		}
		if r.State == "DONE" || r.State == reminderCancelled {
			return 0, ErrConflict
		}
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(r.ID), q.Version.Eq(r.Version)).Updates(map[string]any{"schedule_kind": scheduleKind, "thread_id": in.ThreadID, "project_id": in.ProjectID, "title": in.Name, "interval_minutes": in.IntervalMinutes, "max_fires": maxFires, "next_fire_at": due, "state": "ACTIVE", "version": r.Version + 1})
		if e != nil {
			return 0, e
		}
		if result.RowsAffected != 1 {
			return 0, ErrConflict
		}
		return r.ID, m.record(ctx, tx, tenant, in.AgentID, "LOOP_REMINDER_CHANGED", true)
	}
	if e := m.checkReminderQuotaOn(ctx, tx, tenant, in.AgentID); e != nil {
		return 0, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return 0, e
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	// Loop budget/checks live in planning's contract, rather than being converted
	// into the standalone reminder's differently denominated budget_minor field.
	r := model.Reminder{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: in.AgentID, AnchorKind: "THREAD", ScheduleKind: scheduleKind, ProjectID: in.ProjectID, ThreadID: in.ThreadID, Title: in.Name, NextFireAt: &due, IntervalMinutes: in.IntervalMinutes, MaxFires: maxFires, State: "ACTIVE", Checks: "Planning loop contract", Escalation: "Planning loop contract", OutputFormat: "Planning loop contract", Version: 1, CreatedAt: now}
	if e = q.WithContext(ctx).Create(&r); e != nil {
		return 0, e
	}
	return r.ID, m.record(ctx, tx, tenant, in.AgentID, "LOOP_REMINDER_CREATED", true)
}
func (m *Module) changeLoopReminderOn(ctx context.Context, tx *gorm.DB, tenant, agent, id guid.ID, state string) error {
	if id <= 0 {
		return ErrInvalid
	}
	q := query.BindReminder(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentPrincipalID.Eq(agent), q.ID.Eq(id)).First()
	if e != nil {
		return e
	}
	_, e = m.changeReminderOn(ctx, tx, tenant, agent, id, r.Version, state)
	return e
}
func (m *Module) observeLoopFireOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, fire int, scheduled time.Time) (bool, error) {
	if m.loopObserver == nil {
		return false, nil
	}
	skip, e := m.loopObserver(ctx, tx, tenant, id, fire, scheduled)
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return skip, e
}
