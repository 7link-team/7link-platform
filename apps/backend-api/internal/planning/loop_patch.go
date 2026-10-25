package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
)

type LoopPatch struct {
	Name            *string            `json:"name,omitempty" maxLength:"200"`
	Anchor          *LoopAnchor        `json:"anchor,omitempty"`
	ScheduleKind    *string            `json:"schedule_kind,omitempty" enum:"INTERVAL,PERIOD"`
	IntervalMinutes *int               `json:"interval_minutes,omitempty"`
	PeriodKind      *string            `json:"period_kind,omitempty"`
	ReportBinding   *LoopReportBinding `json:"report_binding,omitempty"`
	SelfChecks      *[]LoopSelfCheck   `json:"self_checks,omitempty" maxItems:"20"`
	Budget          *LoopBudget        `json:"budget,omitempty"`
	Escalation      *LoopEscalation    `json:"escalation,omitempty"`
	AllowOverlap    *bool              `json:"allow_overlap,omitempty"`
}

func (m *Module) patchLoopOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, patch LoopPatch) (LoopContractView, error) {
	var out LoopContractView
	r, e := m.commandLoopOn(ctx, tx, tenant, id)
	if e != nil {
		return out, e
	}
	if e = m.loopAuthority(ctx, tx, tenant, r, true); e != nil {
		return out, e
	}
	if r.Version != version || r.State == "ARCHIVED" || patch.ReportBinding != nil && r.State != "DRAFT" {
		return out, ErrConflict
	}
	v, e := loopView(r)
	if e != nil {
		return out, e
	}
	in := v.LoopDefinition
	if patch.Name != nil {
		in.Name = *patch.Name
	}
	if patch.Anchor != nil {
		in.Anchor = *patch.Anchor
	}
	if patch.ScheduleKind != nil {
		in.ScheduleKind = *patch.ScheduleKind
		if in.ScheduleKind == "INTERVAL" {
			in.PeriodKind = nil
		} else if in.ScheduleKind == "PERIOD" {
			in.IntervalMinutes = nil
		}
	}
	if patch.IntervalMinutes != nil {
		in.IntervalMinutes = patch.IntervalMinutes
	}
	if patch.PeriodKind != nil {
		in.PeriodKind = patch.PeriodKind
	}
	if patch.ReportBinding != nil {
		in.ReportBinding = patch.ReportBinding
	}
	if patch.SelfChecks != nil {
		in.SelfChecks = *patch.SelfChecks
	}
	if patch.Budget != nil {
		in.Budget = *patch.Budget
	}
	if patch.Escalation != nil {
		in.Escalation = *patch.Escalation
	}
	if patch.AllowOverlap != nil {
		in.AllowOverlap = *patch.AllowOverlap
	}
	if e = m.validateLoopOn(ctx, tx, tenant, &in); e != nil {
		return out, e
	}
	if r.State == "ACTIVE" {
		if m.loopReminders.UpsertOn == nil || r.ReminderID == nil {
			return out, ErrUnavailable
		}
		view := LoopContractView{LoopDefinition: in}
		input, err := m.loopReminderInputOn(ctx, tx, tenant, view, *r.ReminderID)
		if err != nil {
			return out, err
		}
		next, e := m.loopReminders.UpsertOn(ctx, tx, tenant, input)
		if e != nil {
			return out, e
		}
		if next != *r.ReminderID {
			return out, ErrConflict
		}
	}
	updates := map[string]any{"name": in.Name, "anchor": loopJSON(in.Anchor), "schedule_kind": in.ScheduleKind, "interval_minutes": in.IntervalMinutes, "period_kind": in.PeriodKind, "self_checks": loopJSON(in.SelfChecks), "budget": loopJSON(in.Budget), "escalation": loopJSON(in.Escalation), "allow_overlap": in.AllowOverlap, "version": version + 1, "updated_at": m.now(), "updated_by": authn.PrincipalIDFromContext(ctx)}
	if in.ReportBinding != nil {
		updates["report_binding"] = loopJSON(in.ReportBinding)
	}
	q := query.BindLoopContracts(tx, m.names)
	res, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(updates)
	if e != nil {
		return out, e
	}
	if res.RowsAffected != 1 {
		return out, ErrConflict
	}
	r, e = m.loopOn(ctx, tx, tenant, id)
	if e != nil {
		return out, e
	}
	if e = m.recordLoop(ctx, tx, r, "PATCH"); e != nil {
		return out, e
	}
	return m.availableLoopView(r)
}
