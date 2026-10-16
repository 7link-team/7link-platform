package core

import (
	"context"

	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

// ExplainOn keeps authorization and its provenance on the caller's transaction.
// Tenant explanations consider tenant grants only: an existing entity binding
// never grants authority to create a different entity.
func (a *Authorizer) ExplainOn(ctx context.Context, tx *gorm.DB, req AuthorizationExplanationRequest) (authz.Explanation, error) {
	if err := validateAuthorizationRequest(req.TenantID, req.Permission, req.PrincipalID); err != nil {
		return authz.Explanation{}, err
	}
	if req.EntityID < 0 || (req.ResourceType == "") != (req.ResourceID == 0) || req.ResourceID < 0 || len(req.ResourceType) > 64 || (req.EntityID == 0 && req.ResourceType != "") {
		return authz.Explanation{}, ErrInvalidArgument
	}
	if a == nil || a.registry == nil {
		return authz.Explanation{}, ErrUnavailable
	}
	if action, ok := a.registry.Find(req.Permission); !ok || action.Scope != authz.ActionScopeTenant {
		return authz.Explanation{}, ErrInvalidArgument
	}
	var pinned *Authorizer
	var revision int64
	var err error
	if tx == nil {
		pinned, err = a.pinWriter()
	} else {
		if tx.Statement == nil {
			return authz.Explanation{}, ErrInvalidArgument
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			return authz.Explanation{}, ErrInvalidArgument
		}
		// Business writes may subsequently provision IAM bindings. Acquire their
		// principal/system fences before the tenant fence, matching IAM mutation
		// order. System authority is not a grant for this tenant explanation.
		transactional := *a
		transactional.db = tx
		if _, err := transactional.checkSystemPermission(ctx, "platform_administer", req.PrincipalID); err != nil {
			return authz.Explanation{}, err
		}
		pinned, revision, err = a.pinAuthorizationTransaction(ctx, tx, req.TenantID)
	}
	if err != nil {
		return authz.Explanation{}, err
	}
	var result authz.Explanation
	switch {
	case req.ResourceType != "":
		result, err = pinned.ExplainResource(ctx, req.TenantID, req.EntityID, req.ResourceType, req.ResourceID, req.Permission, req.PrincipalID)
	case req.EntityID != 0:
		result, err = pinned.ExplainEntity(ctx, req.TenantID, req.EntityID, req.Permission, req.PrincipalID)
	default:
		var snapshot authorizationSnapshot
		snapshot, err = pinned.snapshot(ctx, req.TenantID, 0, req.Permission, req.PrincipalID, false, policyx.TrustedFromContext(ctx, pinned.now()))
		if err == nil {
			result = explainAuthorization(pinned.registry, snapshot, req.TenantID, 0, "", 0, req.Permission)
		}
	}
	if err != nil {
		return authz.Explanation{}, err
	}
	if tx != nil && result.Revision != revision {
		return authz.Explanation{}, ErrAuthorizationChanged
	}
	return result, nil
}
