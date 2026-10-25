package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"regexp"
	"slices"
	"strings"
	"time"
)

type LoopAnchor struct {
	ChannelID           guid.ID `json:"channel_id"`
	ThreadRootMessageID guid.ID `json:"thread_root_message_id"`
}
type LoopReportBinding struct {
	Level      string `json:"level" enum:"MEMBER,PROJECT"`
	PeriodKind string `json:"period_kind" enum:"DAY,WEEK,MONTH,QUARTER,YEAR"`
}
type LoopSelfCheck struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}
type LoopBudget struct {
	MaxCostMicrosPerRun  int64  `json:"max_cost_micros_per_run"`
	MaxDurationMinutes   int    `json:"max_duration_minutes"`
	MonthlyCostCapMicros *int64 `json:"monthly_cost_cap_micros,omitempty"`
}
type LoopEscalation struct {
	OnRequiredCheckFailed      bool `json:"on_required_check_failed"`
	OnBudgetExceeded           bool `json:"on_budget_exceeded"`
	ConsecutiveFailuresToPause int  `json:"consecutive_failures_to_pause"`
}
type LoopDefinition struct {
	ProjectID        *guid.ID           `json:"project_id,omitempty"`
	AgentID          guid.ID            `json:"agent_id"`
	OwnerPrincipalID guid.ID            `json:"owner_principal_id"`
	Kind             string             `json:"kind" enum:"REPORT,CHECK"`
	Name             string             `json:"name" maxLength:"200"`
	Anchor           LoopAnchor         `json:"anchor"`
	ScheduleKind     string             `json:"schedule_kind" enum:"INTERVAL,PERIOD"`
	IntervalMinutes  *int               `json:"interval_minutes,omitempty"`
	PeriodKind       *string            `json:"period_kind,omitempty"`
	ReportBinding    *LoopReportBinding `json:"report_binding,omitempty"`
	SelfChecks       []LoopSelfCheck    `json:"self_checks" maxItems:"20"`
	Budget           LoopBudget         `json:"budget"`
	Escalation       LoopEscalation     `json:"escalation"`
	OutputFormat     string             `json:"output_format" enum:"REPORT_DRAFT,CHECK_RESULT"`
	AllowOverlap     bool               `json:"allow_overlap"`
}
type LoopContractView struct {
	ID guid.ID `json:"id"`
	LoopDefinition
	State                   string   `json:"state"`
	ReminderID              *guid.ID `json:"reminder_id,omitempty"`
	ConsecutiveFailures     int      `json:"consecutive_failures"`
	Version                 int      `json:"version"`
	UnavailableCapabilities []string `json:"unavailable_capabilities"`
}
type LoopRunView struct {
	CheckResult             json.RawMessage `json:"check_result,omitempty"`
	SelfCheckResults        json.RawMessage `json:"self_check_results"`
	EscalationReason        *string         `json:"escalation_reason,omitempty"`
	Note                    *string         `json:"note,omitempty"`
	ID                      guid.ID         `json:"id"`
	LoopContractID          guid.ID         `json:"loop_contract_id"`
	FireNo                  int             `json:"fire_no"`
	State                   string          `json:"state"`
	ScheduledFor            time.Time       `json:"scheduled_for"`
	OpenedAt                time.Time       `json:"opened_at"`
	CompletedAt             *time.Time      `json:"completed_at,omitempty"`
	ReportID                *guid.ID        `json:"report_id,omitempty"`
	CostMicros              *int64          `json:"cost_micros,omitempty"`
	UnavailableCapabilities []string        `json:"unavailable_capabilities"`
}

func loopRunView(r *model.LoopRun) LoopRunView {
	out := LoopRunView{ID: r.ID, LoopContractID: r.LoopContractID, FireNo: r.FireNo, State: r.State, ScheduledFor: r.ScheduledFor, OpenedAt: r.OpenedAt, CompletedAt: r.CompletedAt, ReportID: r.ReportID, SelfCheckResults: json.RawMessage(r.SelfCheckResults), EscalationReason: r.EscalationReason, Note: r.Note, UnavailableCapabilities: []string{}}
	if r.CheckResult != nil {
		out.CheckResult = json.RawMessage(*r.CheckResult)
	}
	if r.State == "SUCCEEDED" || r.State == "ESCALATED" {
		out.CostMicros = &r.CostMicros
	} else {
		out.UnavailableCapabilities = append(out.UnavailableCapabilities, "SETTLED_EXECUTION_COST")
	}
	return out
}

func loopJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func loopView(r *model.LoopContract) (LoopContractView, error) {
	v := LoopContractView{ID: r.ID, State: r.State, ReminderID: r.ReminderID, ConsecutiveFailures: r.ConsecutiveFailures, Version: r.Version, UnavailableCapabilities: []string{"ACTIVATION", "SETTLED_EXECUTION_COST", "RUN_COMPLETION", "TIMEOUT_ESCALATION"}}
	v.LoopDefinition = LoopDefinition{ProjectID: r.ProjectID, AgentID: r.AgentID, OwnerPrincipalID: r.OwnerPrincipalID, Kind: r.Kind, Name: r.Name, ScheduleKind: r.ScheduleKind, IntervalMinutes: r.IntervalMinutes, PeriodKind: r.PeriodKind, OutputFormat: r.OutputFormat, AllowOverlap: r.AllowOverlap}
	for _, pair := range []struct {
		s string
		v any
	}{{r.Anchor, &v.Anchor}, {r.SelfChecks, &v.SelfChecks}, {r.Budget, &v.Budget}, {r.Escalation, &v.Escalation}} {
		if e := json.Unmarshal([]byte(pair.s), pair.v); e != nil {
			return v, e
		}
	}
	if r.ReportBinding != nil {
		if e := json.Unmarshal([]byte(*r.ReportBinding), &v.ReportBinding); e != nil {
			return v, e
		}
	}
	return v, nil
}

var loopCheckKey = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

const loopReminderCancelled = "CANCELLED" //nolint:misspell // Existing reminder wire state.

func validLoop(v LoopDefinition) bool {
	if v.AgentID <= 0 || v.OwnerPrincipalID < 0 || v.ProjectID != nil && *v.ProjectID <= 0 || !validText(v.Name, 200) || v.Anchor.ChannelID <= 0 || v.Anchor.ThreadRootMessageID <= 0 || len(v.SelfChecks) > 20 || v.Budget.MaxCostMicrosPerRun < 0 || v.Budget.MaxDurationMinutes < 5 || v.Budget.MaxDurationMinutes > 240 || v.Budget.MonthlyCostCapMicros != nil && *v.Budget.MonthlyCostCapMicros < 0 || !v.Escalation.OnRequiredCheckFailed || v.Escalation.ConsecutiveFailuresToPause < 1 || v.Escalation.ConsecutiveFailuresToPause > 10 {
		return false
	}
	if v.ScheduleKind == "INTERVAL" {
		if v.IntervalMinutes == nil || *v.IntervalMinutes < 15 || *v.IntervalMinutes > 10080 || v.PeriodKind != nil {
			return false
		}
	} else if v.ScheduleKind == "PERIOD" {
		if v.IntervalMinutes != nil || v.PeriodKind == nil || !slices.Contains([]string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"}, *v.PeriodKind) {
			return false
		}
	} else {
		return false
	}
	if v.Kind == "REPORT" {
		if v.ReportBinding == nil || v.OutputFormat != "REPORT_DRAFT" || !slices.Contains([]string{"MEMBER", "PROJECT"}, v.ReportBinding.Level) || !slices.Contains([]string{"DAY", "WEEK", "MONTH", "QUARTER", "YEAR"}, v.ReportBinding.PeriodKind) || v.ReportBinding.Level == "PROJECT" && v.ProjectID == nil {
			return false
		}
	} else if v.Kind != "CHECK" || v.ReportBinding != nil || v.OutputFormat != "CHECK_RESULT" {
		return false
	}
	seen := map[string]bool{}
	for _, c := range v.SelfChecks {
		if !loopCheckKey.MatchString(c.Key) || seen[c.Key] || !validText(c.Description, 500) {
			return false
		}
		seen[c.Key] = true
	}
	return true
}
func (m *Module) loopAuthority(ctx context.Context, tx *gorm.DB, tenant guid.ID, r *model.LoopContract, write bool) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if tx == nil || tx.Statement == nil {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.policy == nil {
		return ErrUnavailable
	}
	p, ok, e := m.principals.LockActiveRow(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !ok || p.ID != actor {
		return ErrForbidden
	}
	ok, e = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	h, human, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	human = human && h.ID == actor
	if write && !human {
		return ErrForbidden
	}
	owner := r.OwnerPrincipalID
	if write {
		if m.agents.ByPrincipalOn == nil {
			return ErrUnavailable
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, r.AgentID)
		if e != nil {
			return e
		}
		if !found || agent.PrincipalID != r.AgentID || agent.TenantID != tenant {
			return ErrForbidden
		}
		owner = agent.OwnerPrincipalID
	}
	if actor == owner && human || !write && actor == r.AgentID {
		return nil
	}
	permission := "report_read"
	if write {
		permission = "agent_manage"
	}
	c, e := m.policy(ctx, tx, tenant, permission, actor)
	if e != nil {
		return e
	}
	if !c.AllowAll {
		return ErrForbidden
	}
	return nil
}
func (m *Module) loopOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.LoopContract, error) {
	q := query.BindLoopContracts(tx, m.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return r, e
}
func (m *Module) validateLoopOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in *LoopDefinition) error {
	if in.SelfChecks == nil {
		in.SelfChecks = []LoopSelfCheck{}
	}
	if in.Escalation.ConsecutiveFailuresToPause == 0 {
		in.Escalation.ConsecutiveFailuresToPause = 3
	}
	if !validLoop(*in) {
		return ErrInvalid
	}
	if m.agents.ByPrincipalOn == nil || m.loopAnchors.CheckOn == nil {
		return ErrUnavailable
	}
	agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, in.AgentID)
	if e != nil {
		return e
	}
	if !found || agent.TenantID != tenant || agent.PrincipalID != in.AgentID || agent.State != "ACTIVE" {
		return ErrInvalid
	}
	if in.OwnerPrincipalID == 0 {
		in.OwnerPrincipalID = agent.OwnerPrincipalID
	}
	if e = m.lockTeamLoopParticipantsOn(ctx, tx, tenant, &model.LoopContract{AgentID: in.AgentID, OwnerPrincipalID: in.OwnerPrincipalID, ProjectID: in.ProjectID}); e != nil {
		return e
	}
	if in.ProjectID == nil {
		current, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, in.AgentID)
		if err != nil {
			return err
		}
		if !found || current.PrincipalID != agent.PrincipalID || current.TenantID != agent.TenantID || current.OwnerPrincipalID != agent.OwnerPrincipalID || current.State != agent.State {
			return ErrConflict
		}
	}

	owner, ok, e := m.principals.ActiveHumanByID(ctx, tx, in.OwnerPrincipalID)
	if e != nil {
		return e
	}
	if !ok || owner.ID != in.OwnerPrincipalID {
		return ErrInvalid
	}
	ok, e = m.members.IsMemberActiveOn(ctx, tx, tenant, in.OwnerPrincipalID)
	if e != nil {
		return e
	}
	if !ok {
		return ErrInvalid
	}
	project := guid.ID(0)
	if in.ProjectID != nil {
		project = *in.ProjectID
	}
	return m.loopAnchors.CheckOn(ctx, tx, tenant, project, in.Anchor.ChannelID, in.Anchor.ThreadRootMessageID, in.AgentID)
}
func (m *Module) createLoopOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in LoopDefinition, activate bool) (LoopContractView, error) {
	var out LoopContractView
	if m.ids == nil {
		return out, ErrUnavailable
	}
	if e := m.validateLoopOn(ctx, tx, tenant, &in); e != nil {
		return out, e
	}
	r := &model.LoopContract{TenantID: tenant, AgentID: in.AgentID, OwnerPrincipalID: in.OwnerPrincipalID, ProjectID: in.ProjectID}
	if e := m.loopAuthority(ctx, tx, tenant, r, true); e != nil {
		return out, e
	}
	if e := m.loopProjectWriteOn(ctx, tx, tenant, r.ProjectID); e != nil {
		return out, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	r.ID = guid.ID(id)
	r.Kind = in.Kind
	r.Name = strings.TrimSpace(in.Name)
	r.Anchor = loopJSON(in.Anchor)
	r.ScheduleKind = in.ScheduleKind
	r.IntervalMinutes = in.IntervalMinutes
	r.PeriodKind = in.PeriodKind
	r.SelfChecks = loopJSON(in.SelfChecks)
	r.Budget = loopJSON(in.Budget)
	r.Escalation = loopJSON(in.Escalation)
	r.OutputFormat = in.OutputFormat
	r.AllowOverlap = in.AllowOverlap
	r.State = "DRAFT"
	r.Version = 1
	r.CreatedBy = authn.PrincipalIDFromContext(ctx)
	r.UpdatedBy = r.CreatedBy
	r.CreatedAt = m.now()
	r.UpdatedAt = r.CreatedAt
	if in.ReportBinding != nil {
		s := loopJSON(in.ReportBinding)
		r.ReportBinding = &s
	}
	if e = query.BindLoopContracts(tx, m.names).WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	if e = m.recordLoop(ctx, tx, r, "CREATE"); e != nil {
		return out, e
	}
	if activate {
		return m.changeLoopStateOn(ctx, tx, tenant, r.ID, 1, "ACTIVE")
	}
	return m.availableLoopView(r)
}
func (m *Module) changeLoopStateOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, state string) (LoopContractView, error) {
	var out LoopContractView
	r, e := m.commandLoopOn(ctx, tx, tenant, id)
	if e != nil {
		return out, e
	}
	if e = m.loopAuthority(ctx, tx, tenant, r, true); e != nil {
		return out, e
	}
	if r.Version != version || r.State == "ARCHIVED" || !slices.Contains([]string{"ACTIVE", "PAUSED", "ARCHIVED"}, state) || state == r.State || r.State == "DRAFT" && state == "PAUSED" {
		return out, ErrConflict
	}
	v, e := loopView(r)
	if e != nil {
		return out, e
	}
	if state == "ACTIVE" {
		if e = m.validateLoopOn(ctx, tx, tenant, &v.LoopDefinition); e != nil {
			return out, e
		}
		if m.loopReminders.UpsertOn == nil {
			return out, ErrUnavailable
		}
		// Serialize contract-limit checks on the current Agent principal row.
		if _, ok, e := m.principals.LockActiveRow(ctx, tx, r.AgentID); e != nil {
			return out, e
		} else if !ok {
			return out, ErrInvalid
		}
		q := query.BindLoopContracts(tx, m.names)
		count, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.AgentID.Eq(r.AgentID), q.ID.Neq(id), q.State.In("ACTIVE", "PAUSED")).Count()
		if e != nil {
			return out, e
		}
		if count >= 20 {
			return out, ErrConflict
		}
		reminder := guid.ID(0)
		if r.ReminderID != nil {
			reminder = *r.ReminderID
		}
		reminderInput, err := m.loopReminderInputOn(ctx, tx, tenant, v, reminder)
		if err != nil {
			return out, err
		}
		reminder, e = m.loopReminders.UpsertOn(ctx, tx, tenant, reminderInput)
		if e != nil {
			return out, e
		}
		r.ReminderID = &reminder
		r.ConsecutiveFailures = 0
	} else if r.ReminderID != nil {
		if m.loopReminders.ChangeOn == nil {
			return out, ErrUnavailable
		}
		next := "PAUSED"
		if state == "ARCHIVED" {
			next = loopReminderCancelled
		}
		if e = m.loopReminders.ChangeOn(ctx, tx, tenant, r.AgentID, *r.ReminderID, next); e != nil {
			return out, e
		}
	}
	q := query.BindLoopContracts(tx, m.names)
	now := m.now()
	res, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"state": state, "reminder_id": r.ReminderID, "consecutive_failures": r.ConsecutiveFailures, "version": version + 1, "updated_by": authn.PrincipalIDFromContext(ctx), "updated_at": now})
	if e != nil {
		return out, e
	}
	if res.RowsAffected != 1 {
		return out, ErrConflict
	}
	if state == "ARCHIVED" {
		runs := query.BindLoopRuns(tx, m.names)
		note := "Contract archived"
		if _, e = runs.WithContext(ctx).Where(runs.TenantID.Eq(tenant), runs.LoopContractID.Eq(id), runs.State.Eq("OPEN")).Updates(map[string]any{"state": "FAILED", "completed_at": now, "note": note}); e != nil {
			return out, e
		}
	}
	r.State = state
	r.Version++
	if e = m.recordLoop(ctx, tx, r, "STATE_CHANGED"); e != nil {
		return out, e
	}
	return m.availableLoopView(r)
}
func (m *Module) recordLoop(ctx context.Context, tx *gorm.DB, r *model.LoopContract, action string) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	ev := audit.NewDomainEvent(ctx, r.TenantID, "planning.loop_contract_changed", "loop_contract", r.ID)
	authority := "IAM:agent_owner_or_manage"
	actor := events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}
	if action == "AUTO_PAUSED" {
		authority = "SELF:loop_run_result"
		actor = events.Actor{Kind: "AGENT", ID: r.AgentID, AuthoritySource: authority}
		if authn.PrincipalIDFromContext(ctx) == 0 {
			authority = "scheduled:planning.loop_run_timeout"
			actor = events.Actor{Kind: "SYSTEM", AuthoritySource: authority}
		}
	}
	ev.Detail["authority_source"] = authority
	ev.Detail["action"] = action
	ev.Detail["state"] = r.State
	if e := m.audit(ctx, tx, ev); e != nil {
		return e
	}
	project := guid.ID(0)
	if r.ProjectID != nil {
		project = *r.ProjectID
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: project, Type: "loop_contract.state_changed", Actor: actor, Subject: events.Subject{Kind: "loop_contract", ID: r.ID}, SubjectVersion: int64(r.Version), Data: map[string]any{"state": r.State, "action": action}})
	return e
}

// openLoopReminderOn is invoked by agents before the corresponding real inbox
// append. Its writes roll back with the reminder, audit and inbox transaction.
func (m *Module) openLoopReminderOn(ctx context.Context, tx *gorm.DB, tenant, reminder guid.ID, fire int, scheduled time.Time) (bool, error) {
	q := query.BindLoopContracts(tx, m.names)
	ref, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ReminderID.Eq(reminder)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	_, skip, e := m.openLoopRunOn(ctx, tx, tenant, ref.ID, ref.ReminderFireOffset+fire, scheduled)
	if e != nil {
		return false, e
	}
	// OpenOn acquires the contract fence. Re-read its current state before
	// scheduling a successor, so a concurrently paused contract stays paused.
	ref, e = m.loopOn(ctx, tx, tenant, ref.ID)
	if e != nil {
		return false, e
	}
	if ref.ScheduleKind == "PERIOD" && ref.State == "ACTIVE" {
		view, err := loopView(ref)
		if err != nil {
			return false, err
		}
		input, err := m.loopReminderInputOn(ctx, tx, tenant, view, 0)
		if err != nil {
			return false, err
		}
		agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: ref.AgentID, SubjectType: authn.SubjectTypePrincipal})
		next, err := m.loopReminders.UpsertOn(agentCtx, tx, tenant, input)
		if err != nil {
			return false, err
		}
		if next <= 0 || next == reminder {
			return false, ErrConflict
		}
		res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(ref.ID), q.Version.Eq(ref.Version), q.ReminderID.Eq(reminder)).Updates(map[string]any{"reminder_id": next, "reminder_fire_offset": ref.ReminderFireOffset + fire, "version": ref.Version + 1, "updated_at": m.now()})
		if err != nil {
			return false, err
		}
		if res.RowsAffected != 1 {
			return false, ErrConflict
		}
		return skip, nil
	}
	// Standalone reminders are limited to 1000 fires. Continue an interval loop
	// with a real successor reminder while preserving its cumulative fire number.
	if fire >= 1000 && ref.State == "ACTIVE" {
		view, e := loopView(ref)
		if e != nil {
			return false, e
		}
		if m.loopReminders.UpsertOn == nil || ref.IntervalMinutes == nil {
			return false, ErrUnavailable
		}
		agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: ref.AgentID, SubjectType: authn.SubjectTypePrincipal})
		next, e := m.loopReminders.UpsertOn(agentCtx, tx, tenant, agents.LoopReminderInput{AgentID: ref.AgentID, ProjectID: ref.ProjectID, ThreadID: view.Anchor.ThreadRootMessageID, Name: ref.Name, IntervalMinutes: *ref.IntervalMinutes})
		if e != nil {
			return false, e
		}
		if next <= 0 || next == reminder {
			return false, ErrConflict
		}
		res, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(ref.ID), q.Version.Eq(ref.Version), q.ReminderID.Eq(reminder)).Updates(map[string]any{"reminder_id": next, "reminder_fire_offset": ref.ReminderFireOffset + fire, "version": ref.Version + 1, "updated_at": m.now()})
		if e != nil {
			return false, e
		}
		if res.RowsAffected != 1 {
			return false, ErrConflict
		}
	}
	return skip, nil
}
func (m *Module) openLoopRunOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, fire int, scheduled time.Time) (LoopRunView, bool, error) {
	var out LoopRunView
	if fire < 1 || scheduled.IsZero() {
		return out, false, ErrInvalid
	}
	r, e := m.loopOn(ctx, tx, tenant, id)
	if e != nil {
		return out, false, e
	}
	q := query.BindLoopRuns(tx, m.names)
	previous, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.LoopContractID.Eq(id), q.FireNo.Eq(fire)).First()
	if e == nil {
		return loopRunView(previous), previous.State == "SKIPPED", nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, e
	}
	count, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.LoopContractID.Eq(id), q.State.Eq("OPEN")).Count()
	if e != nil {
		return out, false, e
	}
	skip := r.State != "ACTIVE" || !r.AllowOverlap && count > 0
	state := "OPEN"
	if skip {
		state = "SKIPPED"
	}
	next, e := m.ids.Next()
	if e != nil {
		return out, false, e
	}
	if next <= 0 {
		return out, false, ErrUnavailable
	}
	run := model.LoopRun{TenantID: tenant, ID: guid.ID(next), LoopContractID: id, FireNo: fire, ScheduledFor: scheduled, OpenedAt: m.now(), State: state, SelfCheckResults: "[]"}
	if skip {
		now := m.now()
		run.CompletedAt = &now
	}
	if e = q.WithContext(ctx).Create(&run); e != nil {
		return out, false, e
	}
	return loopRunView(&run), skip, nil
}
