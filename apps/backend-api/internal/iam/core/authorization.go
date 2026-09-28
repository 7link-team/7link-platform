package core

import (
	"context"
	"fmt"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
)

// AuthorizationConstraint compiles a data-scoped tenant permission into the
// repository filter inputs available to subject.
func (s *Service) AuthorizationConstraint(
	ctx context.Context,
	tenantID guid.ID,
	permission string,
	subject guid.ID,
) (authz.DataConstraint, error) {
	permission = strings.TrimSpace(permission)
	if err := s.validateDataPermission(permission); err != nil {
		return authz.DataConstraint{}, err
	}
	return s.checker.Constraint(ctx, tenantID, permission, subject)
}

// ExplainEntityAuthorization returns the effective decision and matching
// grants for one entity.
func (s *Service) ExplainEntityAuthorization(
	ctx context.Context,
	tenantID, entityID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	permission = strings.TrimSpace(permission)
	if err := s.validateDataPermission(permission); err != nil {
		return authz.Explanation{}, err
	}
	return s.checker.ExplainEntity(ctx, tenantID, entityID, permission, subject)
}

// CheckEntityAuthorization is the compatibility alias for
// ExplainEntityAuthorization.
func (s *Service) CheckEntityAuthorization(
	ctx context.Context,
	tenantID, entityID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	return s.ExplainEntityAuthorization(ctx, tenantID, entityID, permission, subject)
}

// ExplainResourceAuthorization returns the effective decision and matching
// grants for one business resource owned by an entity.
func (s *Service) ExplainResourceAuthorization(
	ctx context.Context,
	tenantID, entityID guid.ID,
	resourceType string,
	resourceID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	permission, resourceType = strings.TrimSpace(permission), strings.TrimSpace(resourceType)
	if err := s.validateDataPermission(permission); err != nil {
		return authz.Explanation{}, err
	}
	action, _ := s.registry.Find(permission)
	if action.Resource != resourceType || entityID.Zero() || resourceID.Zero() {
		return authz.Explanation{}, fmt.Errorf("%w: permission and business resource type must match", ErrInvalidResourceAuthorization)
	}
	return s.checker.ExplainResource(ctx, tenantID, entityID, resourceType, resourceID, permission, subject)
}

// CheckResourceAuthorization is the compatibility alias for
// ExplainResourceAuthorization.
func (s *Service) CheckResourceAuthorization(
	ctx context.Context,
	tenantID, entityID guid.ID,
	resourceType string,
	resourceID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	return s.ExplainResourceAuthorization(ctx, tenantID, entityID, resourceType, resourceID, permission, subject)
}

func (s *Service) validateDataPermission(permission string) error {
	action, ok := s.registry.Find(permission)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPermissionNotFound, permission)
	}
	if !action.DataScoped {
		return fmt.Errorf("%w: permission %s is not data scoped", ErrInvalidArgument, permission)
	}
	return nil
}
