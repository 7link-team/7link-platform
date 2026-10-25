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

// MemberEntities returns the union of independently authorized entity lists.
// Each permission keeps its own deny and department scope before unioning.
func (s *Service) MemberEntities(ctx context.Context, tenantID, subject guid.ID, permissions []string) ([]Entity, error) {
	if len(permissions) == 0 || len(permissions) > 32 {
		return nil, ErrInvalidArgument
	}
	for _, permission := range permissions {
		if err := s.validateDataPermission(strings.TrimSpace(permission)); err != nil {
			return nil, err
		}
	}
	entities, err := s.ListEntities(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	admitted := make(map[guid.ID]bool)
	seen := make(map[string]bool)
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if seen[permission] {
			continue
		}
		seen[permission] = true
		constraint, err := s.AuthorizationConstraint(ctx, tenantID, permission, subject)
		if err != nil {
			return nil, err
		}
		departments := make([]guid.ID, 0, len(constraint.DepartmentIDs))
		for _, raw := range constraint.DepartmentIDs {
			id, err := guid.RequireID(raw)
			if err != nil {
				return nil, err
			}
			departments = append(departments, id)
		}
		throughDepartments, err := s.EntitiesOwningDepartments(ctx, tenantID, departments)
		if err != nil {
			return nil, err
		}
		for _, entity := range admittedEntities(entities, constraint, throughDepartments) {
			admitted[entity.ID] = true
		}
	}
	result := make([]Entity, 0, len(admitted))
	for _, entity := range entities {
		if admitted[entity.ID] {
			result = append(result, entity)
		}
	}
	return result, nil
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
