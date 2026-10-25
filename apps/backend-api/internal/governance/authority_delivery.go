package governance

import (
	"context"
	"errors"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// DeliveryAuthority exposes implemented direct Human authority. Business owners
// retain lifecycle, exact-version, evidence and step-up guards in their transaction.
type DeliveryAuthority func(context.Context, *gorm.DB, AuthorityRequest) (AuthorityDecision, error)

// ProjectLead checks the actual current seeded delivery_lead role match.
type ProjectLead func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)

func (m *Module) projectLeadOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	role, err := m.roleIDOn(ctx, tx, tenant, "delivery_lead")
	if errors.Is(err, ErrRoleNotSeeded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	read, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: "project_read"})
	if err != nil || !read.Allowed {
		return false, err
	}
	for _, match := range read.Matches {
		if match.Effect == "allow" && match.RoleID == role.String() {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) deliveryAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	return m.Check(ctx, tx, req)
}

func (m *Module) checkDeliveryAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	result := AuthorityDecision{Steps: []AuthorityStep{{Step: "S0", Outcome: "PASS"}}}
	deny := func(step, reason string) (AuthorityDecision, error) {
		result.ReasonCode = reason
		result.Steps = append(result.Steps, AuthorityStep{Step: step, Outcome: "DENY"})
		return result, nil
	}
	kind := "work_item"
	if requirementReviewAuthorityRequest(req) {
		kind = "requirement"
	}
	if req.Code == "scope_change" {
		kind = "requirement"
	}
	if req.Code == "delivery_send" {
		kind = "delivery"
	}
	if req.ProjectID <= 0 || req.Resource.ID <= 0 || req.Resource.Kind != kind || req.Spawn != nil {
		return deny("S0", "INVALID_REQUEST")
	}
	if kind == "work_item" && req.Facts == nil {
		return deny("S0", "INVALID_REQUEST")
	}
	if req.ActorKind != "HUMAN" {
		return deny("S2", "ACTOR_NOT_ELIGIBLE")
	}
	// No SOD override policy is bound yet. A direct role cannot bypass the
	// default separation gate for approving or returning the actor's own result.
	if (req.Code == "decision_force_valid" || req.Code == "decision_approve" || req.Code == "decision_request_changes") && req.Facts != nil && req.Facts.SelfProduced {
		return deny("S3", "SELF_REVIEW")
	}
	if req.Environment == "PRODUCTION" || m.explain == nil {
		return AuthorityDecision{}, ErrUnavailable
	}
	if _, ok := m.seed.registry.Find(req.Code); !ok {
		return AuthorityDecision{}, ErrUnavailable
	}
	explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: req.ProjectID, PrincipalID: req.PrincipalID, Permission: req.Code})
	if err != nil {
		return AuthorityDecision{}, err
	}
	result.PolicyRevision = explanation.Revision
	if explanation.Reason == "inactive_membership" || explanation.Reason == "inactive_tenant" {
		return deny("S1", "PRINCIPAL_INACTIVE")
	}
	result.Steps = append(result.Steps, AuthorityStep{Step: "S1", Outcome: "PASS"})
	if explanation.Reason == "explicit_deny" || explanation.Reason == "administrator_scope_denied" {
		return deny("S4", "EXPLICIT_DENY")
	}
	if !explanation.Allowed {
		return deny("S4", "NO_ROLE_GRANT")
	}
	read := tx
	if read == nil {
		if m.runtime == nil || m.runtime.Database() == nil {
			return AuthorityDecision{}, ErrUnavailable
		}
		read = m.runtime.Database().Write()
	}
	source, err := m.seed.roleAuthoritySource(ctx, read, req.TenantID, req.Code, explanation)
	if err != nil {
		return AuthorityDecision{}, err
	}
	if source.source == "" {
		return deny("S5", "NO_DIRECT_AUTHORITY")
	}
	result.Allowed, result.AuthoritySource = true, source.source
	result.Steps = append(result.Steps, AuthorityStep{Step: "S4", Outcome: "MATCH"})
	return result, nil
}
