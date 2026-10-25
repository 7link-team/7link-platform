package delivery

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RetirementDecision cites actual decisions on results authored by this Agent.
// It exports no result/evidence body and rechecks current work read authority.
type RetirementDecision struct {
	ID         guid.ID   `json:"id"`
	ProjectID  guid.ID   `json:"project_id"`
	WorkItemID guid.ID   `json:"work_item_id"`
	ResultID   guid.ID   `json:"result_id"`
	Kind       string    `json:"kind"`
	Verdict    string    `json:"verdict"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

func (m *Module) checkRetirementWorkReadOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) error {
	_, err := m.readWork(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx))
	return err
}

// Retirement invalidates the source lead binding. This fixed lifecycle effect
// grants no project-edit authority and never selects a replacement lead.
func (m *Module) clearRetirementLeadsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) error {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return err
	}
	if m.service == nil || m.agents.ByPrincipalOn == nil || m.service.audit == nil || m.service.events == nil {
		return ErrUnavailable
	}
	fact, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return err
	}
	if !found || (fact.State != "RETIRING" && fact.State != "RETIRED") {
		return ErrAssignmentState
	}
	p := query.BindProjects(tx, m.service.names)
	rows, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.LeadAgentID.Eq(fact.ID), p.ArchivedAt.IsNull()).Order(p.ProjectID).Clauses(clause.Locking{Strength: "UPDATE"}).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		now := time.Now().UTC().Truncate(time.Millisecond)
		changed, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(row.ProjectID), p.Version.Eq(row.Version), p.LeadAgentID.Eq(fact.ID)).Updates(map[string]any{"lead_agent_id": nil, "dispatch_policy": "MANUAL", "dispatch_policy_explicit": true, "version": row.Version + 1, "updated_at": now})
		if err != nil {
			return err
		}
		if changed.RowsAffected != 1 {
			return ErrWorkVersion
		}
		system := authn.WithClaims(ctx, nil)
		e := audit.NewDomainEvent(system, tenant, "delivery.retirement_lead_cleared", "project", row.ProjectID)
		e.Detail["authority_source"], e.Detail["agent_principal_id"] = "SYSTEM:agent_retirement", agent
		if err = m.service.audit(system, tx, e); err != nil {
			return err
		}
		if _, err = m.service.events.Publish(system, tx, events.Event{TenantID: tenant, ProjectID: row.ProjectID, Type: "project.members_changed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:agent_retirement"}, Subject: events.Subject{Kind: "project", ID: row.ProjectID}, SubjectVersion: int64(row.Version + 1), Data: map[string]any{"lead_agent_id": nil, "dispatch_policy": "MANUAL", "reason": "AGENT_RETIREMENT"}}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) retirementLeadProjectsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]guid.ID, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if m.agents.ByPrincipalOn == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	fact, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, agent)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrInvalidMember
	}
	p := query.BindProjects(tx, m.service.names)
	rows, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.LeadAgentID.Eq(fact.ID), p.ArchivedAt.IsNull()).Order(p.ProjectID).Limit(1001).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, ErrUnavailable
	}
	out := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		readable, err := m.projectControlReadableOn(ctx, tx, tenant, row.ProjectID, authn.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, err
		}
		if readable {
			out = append(out, row.ProjectID)
		}
	}
	return out, nil
}

// The Execution owner calls this only after fencing a delivered retirement
// attempt with UNKNOWN outcome. It clears the exact pointer without claiming
// model failure/success, cancellation acknowledgement, evidence or a result.
func (m *Module) interruptRetirementExecutionOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink) error {
	work, assignment, err := m.executionStopWorkOn(ctx, tx, l, true)
	if err != nil {
		return err
	}
	if m.service.audit == nil || m.service.events == nil {
		return ErrUnavailable
	}
	before := work.State
	now := time.Now().UTC().Truncate(time.Millisecond)
	if assignment.EnsemblePlanID == nil {
		if work.State == "IN_PROGRESS" {
			work.State = "CLAIMED"
		}
		w := query.BindWorkItems(tx, m.service.names)
		changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(l.WorkItemID), w.CurrentExecutionID.Eq(l.ExecutionID)).UpdateSimple(w.CurrentExecutionID.Null())
		if err != nil {
			return err
		}
		if changed.RowsAffected != 1 {
			return ErrWorkVersion
		}
		if err = m.executionWorkChangedOn(ctx, tx, l, work, before, "RETIREMENT_EXECUTION_UNKNOWN", now); err != nil {
			return err
		}
	}
	// A proven ensemble branch owns no work-level execution pointer. Preserve
	// other branches and submitted results while its Execution owner fences it.
	detail := map[string]any{"execution_id": l.ExecutionID.String(), "outcome": "UNKNOWN", "reason": "AGENT_RETIREMENT", "state": work.State, "branch_only": assignment.EnsemblePlanID != nil}
	event := audit.NewDomainEvent(ctx, l.TenantID, "delivery.retirement_execution_detached", "work_item", l.WorkItemID)
	event.Detail = detail
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: l.TenantID, ProjectID: l.ProjectID, Type: "work_item.state_changed", Actor: events.Actor{Kind: "HUMAN", ID: l.ActorID, AuthoritySource: "permission:execution_stop"}, Subject: events.Subject{Kind: "work_item", ID: l.WorkItemID}, SubjectVersion: int64(work.Version), Data: detail})
	return err
}

func (m *Module) retirementDecisionsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID, since time.Time) ([]RetirementDecision, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if m == nil || m.service == nil || since.IsZero() {
		return nil, ErrUnavailable
	}
	a, r := query.BindResultActions(tx, m.service.names), query.BindResultVersions(tx, m.service.names)
	var rows []RetirementDecision
	err := a.WithContext(ctx).Select(a.ID, a.ProjectID, a.WorkItemID, a.ResultID, a.Kind, a.Verdict, a.Reason, a.CreatedAt).Join(r, a.TenantID.EqCol(r.TenantID), a.ProjectID.EqCol(r.ProjectID), a.WorkItemID.EqCol(r.WorkItemID), a.ResultID.EqCol(r.ID)).Where(a.TenantID.Eq(tenant), r.ActorID.Eq(agent), a.CreatedAt.Gte(since)).Order(a.ID).Limit(1001).Scan(&rows)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, ErrUnavailable
	}
	out := make([]RetirementDecision, 0, len(rows))
	for _, row := range rows {
		if _, err := m.readWork(ctx, tx, tenant, row.ProjectID, row.WorkItemID, authn.PrincipalIDFromContext(ctx)); err != nil {
			if errors.Is(err, ErrProjectForbidden) || errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}
