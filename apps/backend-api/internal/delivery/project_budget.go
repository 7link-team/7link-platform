package delivery

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"gorm.io/gorm"
)

type projectBudgetBody struct {
	CapMicros         *int64 `json:"cap_micros" minimum:"1" maximum:"1000000000000000" doc:"Null removes the cap in HYBRID mode only."`
	NotifyPct         int    `json:"notify_pct" minimum:"50" maximum:"95"`
	ConfirmBelowSpent bool   `json:"confirm_below_spent" doc:"Explicit confirmation is required when the new cap is below accounted spend."`
	Reason            string `json:"reason" minLength:"10" maxLength:"2000"`
}

// The project fence serializes cap edits with execution settlement. A new cap
// never resets spent money, and releases only budget-owned holds.
func (m *Module) changeProjectBudgetOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, expected int, in projectBudgetBody) (ProjectView, error) {
	if in.NotifyPct < 50 || in.NotifyPct > 95 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || (in.CapMicros != nil && (*in.CapMicros <= 0 || *in.CapMicros > 1_000_000_000_000_000)) {
		return ProjectView{}, ErrInvalidArgument
	}
	if m == nil || m.ids == nil || m.service == nil || m.service.audit == nil || m.service.events == nil {
		return ProjectView{}, ErrUnavailable
	}
	fence, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, 0, actor)
	if err != nil {
		return ProjectView{}, err
	}
	p, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "project_manage")
	if err != nil {
		return ProjectView{}, err
	}
	if err = fence(); err != nil {
		return ProjectView{}, err
	}
	if kind != "HUMAN" {
		return ProjectView{}, ErrWorkForbidden
	}
	if p.Version != expected {
		return ProjectView{}, ErrWorkVersion
	}
	if p.TeamMode != "HYBRID" && in.CapMicros == nil {
		return ProjectView{}, ErrInvalidArgument
	}
	if in.CapMicros != nil && *in.CapMicros < p.BudgetSpentMicros && !in.ConfirmBelowSpent {
		return ProjectView{}, ErrInvalidArgument
	}
	before := ProjectBudget{p.BudgetCapMicros, p.BudgetSpentMicros, p.BudgetNotifyPct, p.BudgetCapVersion, p.BudgetState, p.Holds}
	p.BudgetCapMicros, p.BudgetNotifyPct = in.CapMicros, in.NotifyPct
	p.BudgetCapVersion++
	p.Version++
	p.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	p.UpdatedBy = &actor
	holds := []map[string]json.RawMessage{}
	if err = json.Unmarshal([]byte(p.Holds), &holds); err != nil {
		return ProjectView{}, ErrUnavailable
	}
	// Re-evaluate the budget hold against the new cap without removing any other hold.
	holds = slices.DeleteFunc(holds, func(h map[string]json.RawMessage) bool { return string(h["kind"]) == `"BUDGET"` })
	data, err := json.Marshal(holds)
	if err != nil {
		return ProjectView{}, err
	}
	p.Holds, p.BudgetState = string(data), "NORMAL"
	q := query.BindProjects(tx, m.service.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Version.Eq(expected)).Select(q.BudgetCapMicros, q.BudgetNotifyPct, q.BudgetCapVersion, q.BudgetState, q.Holds, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(p)
	if err != nil {
		return ProjectView{}, err
	}
	if changed.RowsAffected != 1 {
		return ProjectView{}, ErrWorkVersion
	}
	if err = m.spendProjectBudgetOn(ctx, tx, tenant, project, 0); err != nil {
		return ProjectView{}, err
	}
	p, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if err != nil {
		return ProjectView{}, err
	}
	if p.BudgetState == "EXHAUSTED" && m.executionHolds.ChangeOn != nil {
		if _, err = m.executionHolds.ChangeOn(ctx, tx, tenant, project, 0, false); err != nil {
			return ProjectView{}, err
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return ProjectView{}, err
	}
	if id <= 0 {
		return ProjectView{}, ErrUnavailable
	}
	row := &model.ProjectBudgetEvent{TenantID: tenant, ID: guid.ID(id), ProjectID: project, Kind: "CAP_CHANGED", CapMicros: p.BudgetCapMicros, SpentMicros: p.BudgetSpentMicros, NotifyPct: p.BudgetNotifyPct, CapVersion: p.BudgetCapVersion, State: p.BudgetState, Reason: strings.TrimSpace(in.Reason), AuthoritySource: "permission:project_manage", CapBeforeMicros: before.CapMicros, ActorID: &actor, OccurredAt: p.UpdatedAt}
	if err = query.BindBudgetEvents(tx, m.service.names).WithContext(ctx).Create(row); err != nil {
		return ProjectView{}, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.project_budget_changed", "project", project)
	event.Detail["before"], event.Detail["after"], event.Detail["reason"] = before, row, in.Reason
	if err = m.service.audit(ctx, tx, event); err != nil {
		return ProjectView{}, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.budget_changed", Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(p.Version), Actor: events.Actor{Kind: "HUMAN", ID: actor}, Data: map[string]any{"cap_version": p.BudgetCapVersion, "cap_micros": p.BudgetCapMicros, "spent_micros": p.BudgetSpentMicros, "state": p.BudgetState, "reason": in.Reason}})
	if err != nil {
		return ProjectView{}, err
	}
	if before.State == "EXHAUSTED" && p.BudgetState != "EXHAUSTED" {
		id, e := m.ids.Next()
		if e != nil {
			return ProjectView{}, e
		}
		if id <= 0 {
			return ProjectView{}, ErrUnavailable
		}
		released := *row
		released.ID = guid.ID(id)
		released.Kind = "HOLD_RELEASED"
		if e = query.BindBudgetEvents(tx, m.service.names).WithContext(ctx).Create(&released); e != nil {
			return ProjectView{}, e
		}
	}
	return projectRecordView(*p)
}

const projectBudgetEnforceJob = "delivery.budget_enforce"

// Enforce persistent budget holds through the execution owner's existing
// control protocol. A requested pause never represents a Runner ACK.
func (m *Module) projectBudgetEnforceHandler() jobs.Handler {
	return jobs.Handler{Kind: projectBudgetEnforceJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.service == nil || m.executionHolds.ChangeOn == nil || m.executionHolds.PrincipalsOn == nil {
			return ErrUnavailable
		}
		db := m.runtime.Database().Write()
		q := query.BindProjects(db, m.service.names)
		// Keyset through all projects, including when an early project is persistently held.
		var after guid.ID
		for {
			rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ProjectID.Gt(after), q.BudgetState.Eq("EXHAUSTED"), q.ArchivedAt.IsNull(), q.Stage.NotIn("CLOSED", "CANCELLED")).Order(q.ProjectID).Limit(100).Find() //nolint:misspell // Preserve the published domain or serialized contract spelling.
			if err != nil {
				return err
			}
			for _, p := range rows {
				after = p.ProjectID
				if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.enforceProjectBudgetOn(ctx, tx, job.TenantID, p.ProjectID) }); err != nil {
					return err
				}
			}
			if len(rows) < 100 {
				return nil
			}
		}
	}}
}
func (m *Module) enforceProjectBudgetOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if m == nil || m.executionHolds.ChangeOn == nil || m.executionHolds.PrincipalsOn == nil {
		return ErrUnavailable
	}
	fence, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, 0)
	if err != nil {
		return err
	}
	fact, err := m.lockProjectBudgetOn(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	if err = fence(); err != nil {
		return err
	}
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).First()
	if err != nil {
		return err
	}
	if p.ArchivedAt != nil || p.Stage == "CLOSED" || p.Stage == "CANCELLED" || fact.State != "EXHAUSTED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return nil
	}
	_, err = m.executionHolds.ChangeOn(ctx, tx, tenant, project, 0, false)
	return err
}
