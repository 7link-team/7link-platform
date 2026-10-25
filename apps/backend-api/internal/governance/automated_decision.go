package governance

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AppliedDelegationFact struct {
	WorkItemID, ActorID guid.ID
	At                  time.Time
}

// AppliedDelegations returns command receipts only for currently readable
// projects; denied/unavailable proposals never become successful activity.
type AppliedDelegations func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time, time.Time) ([]AppliedDelegationFact, error)

// Only applied command receipts bearing this exact delegation's immutable
// authority are uses. Failed proposals and a replacement grant are excluded.
func (m *Module) delegationUsesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, view *DelegationView) error {
	if view == nil || view.ID <= 0 || view.ProjectID <= 0 {
		return ErrInvalidArgument
	}
	if err := m.authorizeDelegationReadOn(ctx, tx, tenant, view.ProjectID); err != nil {
		return err
	}
	q := query.BindAutomatedDecisions(tx, m.seed.names)
	scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(view.ProjectID), q.ActorID.Eq(view.GranteePrincipalID), q.AuthoritySource.Eq("DELEGATION:"+view.ID.String()), q.State.Eq("APPLIED"), q.DecisionID.Gt(guid.ID(0)))
	total, err := scope.Count()
	if err != nil {
		return err
	}
	rows, err := scope.Order(q.CreatedAt.Desc(), q.ID.Desc()).Limit(50).Find()
	if err != nil {
		return err
	}
	view.UseCount = &total
	view.Uses = []DelegationUse{}
	for _, row := range rows {
		view.Uses = append(view.Uses, DelegationUse{WorkItemID: row.WorkItemID, DecisionID: row.DecisionID, Reason: row.Reason, At: row.CreatedAt})
	}
	return nil
}

func (m *Module) appliedDelegationsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, start, end time.Time) ([]AppliedDelegationFact, error) {
	if m == nil || m.explain == nil || tx == nil || m.seed == nil {
		return nil, ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant || !start.Before(end) {
		return nil, ErrForbidden
	}
	a, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: actor, Permission: "project_read"})
	if err != nil {
		return nil, err
	}
	if !a.Allowed {
		return nil, ErrForbidden
	}
	q := query.BindAutomatedDecisions(tx, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("APPLIED"), q.DecisionID.Gt(guid.ID(0)), q.CreatedAt.Gte(start), q.CreatedAt.Lt(end)).Find()
	if err != nil {
		return nil, err
	}
	out := []AppliedDelegationFact{}
	for _, row := range rows {
		out = append(out, AppliedDelegationFact{WorkItemID: row.WorkItemID, ActorID: row.ActorID, At: row.CreatedAt})
	}
	return out, nil
}

// AutomatedDecisionSource resolves current source facts and executes the owner's
// canonical decision command in the same transaction. Neither is an HTTP input.
// A nil ApplyOn explicitly means the source has no delegated decision command.
type AutomatedDecisionSource struct {
	ResolveOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (AuthorityRequest, bool, error)
	ApplyOn   func(context.Context, *gorm.DB, AuthorityRequest, AuthorityDecision) (guid.ID, error)
}
type AutomatedDecisionConnector struct {
	Bind func(AutomatedDecisionSource) error
}

func (m *Module) bindAutomatedDecisions(source AutomatedDecisionSource) error {
	if source.ResolveOn == nil || m.automatedDecisions.ResolveOn != nil {
		return ErrInvalidArgument
	}
	m.automatedDecisions = source
	return nil
}

func (m *Module) consumeAutomatedDecision(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	if e.EventType != "work_item.state_changed" {
		return ErrInvalidArgument
	}
	var data struct {
		To       string  `json:"to"`
		ResultID guid.ID `json:"result_id"`
	}
	if json.Unmarshal(e.Data, &data) != nil {
		return ErrInvalidArgument
	}
	if data.To != "NEEDS_HUMAN_REVIEW" {
		return nil
	}
	if tx == nil || tx.Statement == nil || e.TenantID <= 0 || e.ProjectID <= 0 || e.Subject.ID <= 0 || data.ResultID <= 0 || e.EventID <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m == nil || m.seed == nil || m.seed.ids == nil || m.seed.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	// The evaluator and the executor share this transaction; subscriber order can
	// never make a claim eligible before its escalation evaluation is persisted.
	if err := m.evaluateClaimEvent(ctx, tx, e); err != nil {
		return err
	}
	q := query.BindAutomatedDecisions(tx, m.seed.names)
	old, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(e.TenantID), q.ClaimID.Eq(data.ResultID)).First()
	if err == nil {
		if old.ProjectID != e.ProjectID || old.WorkItemID != e.Subject.ID {
			return ErrInvalidArgument
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	r := model.AutomatedDecision{TenantID: e.TenantID, ProjectID: e.ProjectID, WorkItemID: e.Subject.ID, ClaimID: data.ResultID, TriggerEventID: e.EventID, State: "UNAVAILABLE", Reason: "SOURCE_COMMAND_UNAVAILABLE", CreatedAt: m.seed.now()}
	if m.automatedDecisions.ResolveOn != nil {
		req, current, resolveErr := m.automatedDecisions.ResolveOn(ctx, tx, e.TenantID, e.ProjectID, e.Subject.ID, data.ResultID)
		if resolveErr != nil && !errors.Is(resolveErr, ErrUnavailable) {
			return resolveErr
		}
		if resolveErr == nil && !current {
			r.State, r.Reason = "SUPERSEDED", "CLAIM_NOT_CURRENT"
		}
		if resolveErr == nil && current && m.automatedDecisions.ApplyOn != nil {
			if req.TenantID != e.TenantID || req.ProjectID != e.ProjectID || req.Resource.Kind != "work_item" || req.Resource.ID != e.Subject.ID || req.Facts == nil || req.Facts.ClaimID != data.ResultID || req.ActorKind != "AGENT" || req.PrincipalID <= 0 || req.Code != "decision_approve" {
				return ErrInvalidArgument
			}
			authority, err := m.Check(ctx, tx, req)
			if err != nil {
				return err
			}
			r.ActorID = req.PrincipalID
			r.State, r.Reason = "DENIED", authority.ReasonCode
			if authority.Allowed {
				if authority.DelegationID <= 0 || authority.AuthoritySource != "DELEGATION:"+authority.DelegationID.String() {
					return ErrForbidden
				}
				decision, err := m.automatedDecisions.ApplyOn(ctx, tx, req, authority)
				if err != nil {
					return err
				}
				if decision <= 0 {
					return ErrUnavailable
				}
				r.DecisionID, r.AuthoritySource, r.State, r.Reason = decision, authority.AuthoritySource, "APPLIED", ""
			}
		}
	}
	id, err := m.seed.ids.Next()
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	r.ID = guid.ID(id)
	if err = q.WithContext(ctx).Create(&r); err != nil {
		return err
	}
	a := audit.NewDomainEvent(ctx, r.TenantID, "governance.automated_decision_recorded", "automated_decision", r.ID)
	a.Detail["state"], a.Detail["reason"], a.Detail["claim_id"] = r.State, r.Reason, r.ClaimID.String()
	a.Detail["authority_source"], a.Detail["decision_id"] = r.AuthoritySource, r.DecisionID.String()
	if err = m.seed.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: "governance.automated_decision_recorded", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:delegation_executor"}, Subject: events.Subject{Kind: "automated_decision", ID: r.ID}, CausationID: e.EventID, Data: map[string]any{"state": r.State, "reason": r.Reason, "work_item_id": r.WorkItemID, "claim_id": r.ClaimID, "decision_id": r.DecisionID}})
	return err
}
