package governance

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"slices"
)

// DispatchAutoFacts are locked, server-resolved project and Agent facts supplied
// by Delivery. This capability cannot be invoked by an HTTP payload directly.
type DispatchAutoFacts struct {
	TenantID, ProjectID, WorkItemID, PrincipalID guid.ID
	AgentID, LeadAgentID                         guid.ID
	Policy, TeamMode, Stage, Risk, WorkType      string
	ActiveLead, Held, RequireHumanDecision       bool
	AssigneePrincipalID                          guid.ID
}
type DispatchAutoAuthority func(context.Context, *gorm.DB, DispatchAutoFacts) (AuthorityDecision, error)
type DispatchAutoConsumer struct {
	Bind func(DispatchAutoAuthority) error
}
type dispatchAutoFactsKey struct{}

func validDispatchAutoFacts(f DispatchAutoFacts) bool {
	return f.TenantID > 0 && f.ProjectID > 0 && f.WorkItemID > 0 && f.PrincipalID > 0 && f.AgentID > 0 && f.AgentID == f.LeadAgentID && f.ActiveLead && f.Policy == "AUTO" && (f.TeamMode == "HYBRID" || f.TeamMode == "AUTONOMOUS") && f.Stage == "ACTIVE" && (f.Risk == "LOW" || f.Risk == "MEDIUM") && !f.Held && !f.RequireHumanDecision && f.AssigneePrincipalID > 0 && f.AssigneePrincipalID != f.PrincipalID
}

func (m *Module) bindAutoDispatch(s *module.Services) error {
	c, err := module.Resolve[DispatchAutoConsumer](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	if err = m.policies.Register(PolicyRule{ID: "dispatch_auto", Codes: []string{"work_item_assign"}, Evaluate: func(ctx context.Context, _ AuthorityRequest) (bool, error) {
		f, ok := ctx.Value(dispatchAutoFactsKey{}).(DispatchAutoFacts)
		return ok && validDispatchAutoFacts(f), nil
	}}); err != nil {
		return err
	}
	return c.Bind(m.checkAutoDispatch)
}

func (m *Module) checkAutoDispatch(ctx context.Context, tx *gorm.DB, f DispatchAutoFacts) (AuthorityDecision, error) {
	denied := AuthorityDecision{ReasonCode: "AUTO_DISPATCH_GUARD", Steps: []AuthorityStep{{Step: "S7", Outcome: "DENY"}}}
	if tx == nil || tx.Statement == nil || authn.TenantIDFromContext(ctx) != f.TenantID || authn.PrincipalIDFromContext(ctx) != f.PrincipalID || authn.EntityIDFromContext(ctx) != f.ProjectID || f.WorkItemID <= 0 || !validDispatchAutoFacts(f) {
		return denied, nil
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return AuthorityDecision{}, ErrInvalidArgument
	}
	if m == nil || m.explain == nil || m.spawnDirectory.AgentOn == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	agent, found, err := m.spawnDirectory.AgentOn(ctx, tx, f.TenantID, f.AgentID)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if !found || agent.PrincipalID != f.PrincipalID || agent.TenantID != f.TenantID || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") {
		return denied, nil
	}
	// Policy supplies assignment authority, but never overrides an explicit IAM
	// deny or grants access to an unreadable/inactive project or membership.
	for _, permission := range []string{"work_item_read", "work_item_assign"} {
		e, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: f.TenantID, EntityID: f.ProjectID, PrincipalID: f.PrincipalID, Permission: permission})
		if err != nil {
			return AuthorityDecision{}, err
		}
		if e.Reason == "inactive_membership" || e.Reason == "inactive_tenant" || e.Reason == "explicit_deny" || e.Reason == "administrator_scope_denied" || (permission == "work_item_read" && !e.Allowed) {
			return denied, nil
		}
	}
	req := AuthorityRequest{TenantID: f.TenantID, ProjectID: f.ProjectID, PrincipalID: f.PrincipalID, ActorKind: "AGENT", Code: "work_item_assign", Resource: ResourceRef{Kind: "work_item", ID: f.WorkItemID}, Facts: &WorkItemFacts{Type: f.WorkType, Risk: f.Risk, AssigneePrincipalID: f.AssigneePrincipalID}}
	out, err := m.policies.evaluate(context.WithValue(ctx, dispatchAutoFactsKey{}, f), req)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if out.source != "POLICY:dispatch_auto" {
		return denied, nil
	}
	return AuthorityDecision{Allowed: true, AuthoritySource: out.source, Steps: []AuthorityStep{{Step: "S7", Outcome: "MATCH"}}}, nil
}

// dispatchAuthority registers the direct Human ASSIGN branch. Automatic
// policy/delegated assignment remains unavailable until its policy is bound.
func (m *Module) dispatchAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	return m.checkDeliveryAuthority(ctx, tx, req)
}
