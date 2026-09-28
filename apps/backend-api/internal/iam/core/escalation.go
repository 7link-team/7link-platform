package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

func (s *Service) requireGrantablePermissions(ctx context.Context, tenantID guid.ID, codes ...string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	wanted := make([]string, 0, len(codes))
	seen := map[string]bool{}
	for _, code := range codes {
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		wanted = append(wanted, code)
	}
	if len(wanted) == 0 {
		return nil
	}
	allowed, err := s.checkRootPermissions(ctx, tenantID, wanted, claims.PrincipalID)
	if err != nil {
		return err
	}
	for _, code := range wanted {
		if !allowed[code] {
			return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, code)
		}
	}
	return nil
}

func (s *Service) requireGrantableRoleWithRepository(
	ctx context.Context,
	repo *Repository,
	tenantID, roleID guid.ID,
) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	if err := s.requireGrantablePermissionsOn(ctx, repo, tenantID); err != nil {
		return err
	}
	var role struct{ ID guid.ID }
	err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.Roles.String()).Select("id").Where("tenant_id = ? AND id = ?", tenantID, roleID).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrRoleNotFound
	}
	if err != nil {
		return err
	}
	codes, err := rolePermissionCodes(ctx, repo, tenantID, roleID)
	if err != nil {
		return err
	}
	return s.requireGrantablePermissionsOn(ctx, repo, tenantID, codes...)
}

func rolePermissionCodes(ctx context.Context, repo *Repository, tenantID, roleID guid.ID) ([]string, error) {
	var codes []string
	if err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.RolePermissions.String()).Select("permission_code").Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Find(&codes).Error; err != nil {
		return nil, err
	}
	return codes, nil
}

// requireGrantableDataScope refuses widening a role to the all-data scope for
// an actor who reaches any of the role's data-scoped permissions only through
// a narrower scope. Callers already hold the role-grantability fence.
func (s *Service) requireGrantableDataScope(ctx context.Context, repo *Repository, tenantID, roleID guid.ID) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	kind, err := repo.rootKind(ctx, tenantID)
	if err != nil || kind != "tenant" {
		return err
	}
	pinned := s.pinnedTo(repo)
	administrator, err := pinned.checker.CheckBulk(ctx, tenantID, []string{tenantAdministratorPermission}, claims.PrincipalID)
	if err != nil || administrator[tenantAdministratorPermission] {
		return err
	}
	codes, err := rolePermissionCodes(ctx, repo, tenantID, roleID)
	if err != nil {
		return err
	}
	for _, code := range codes {
		action, known := s.registry.Find(code)
		if !known || !action.DataScoped || action.Scope == "platform" || action.Scope == "operating_platform" {
			continue
		}
		constraint, err := pinned.checker.Constraint(ctx, tenantID, code, claims.PrincipalID)
		if err != nil {
			return err
		}
		if !constraint.AllowAll {
			return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, code)
		}
	}
	return nil
}

// requireGrantableRelationship refuses a relationship that would confer an
// action the actor does not hold on the same target, using the authorizer's
// own relation matching. Callers already hold the tenant policy fence.
func (s *Service) requireGrantableRelationship(ctx context.Context, repo *Repository, relationship Relationship) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	pinned := s.pinnedTo(repo)
	grant := relationshipGrant{ResourceType: relationship.ResourceType, Relation: relationship.Relation}
	for _, action := range s.registry.All() {
		if !relationshipAllowed(action, grant) {
			continue
		}
		var explanation authz.Explanation
		var err error
		if relationship.EntityID.Zero() {
			explanation, err = pinned.checker.ExplainEntity(ctx, relationship.TenantID, relationship.ResourceID, action.Code, claims.PrincipalID)
		} else {
			explanation, err = pinned.checker.ExplainResource(ctx, relationship.TenantID, relationship.EntityID, relationship.ResourceType, relationship.ResourceID, action.Code, claims.PrincipalID)
		}
		if err != nil {
			return err
		}
		if !explanation.Allowed {
			return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, action.Code)
		}
	}
	return nil
}

// The fence and checker share the mutation transaction so revocation cannot race a grant.
func (s *Service) requireGrantablePermissionsOn(ctx context.Context, repo *Repository, owner guid.ID, codes ...string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	kind, err := repo.rootKind(ctx, owner)
	if err != nil {
		return err
	}
	if kind == "system" {
		resolved, err := repo.lockSystemAuthorization(ctx, claims.PrincipalID)
		if err != nil {
			return err
		}
		if resolved != owner {
			return ErrInvalidArgument
		}
	} else {
		if kind == "tenant" || kind == "platform" {
			if checker, ok := s.checker.(*Authorizer); ok {
				pinned := *checker
				pinned.db = repo.writer()
				if _, err := pinned.checkSystemPermission(ctx, "platform_administer", claims.PrincipalID); err != nil {
					return err
				}
			}
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), owner); err != nil {
			return err
		}
	}
	return s.pinnedTo(repo).requireGrantablePermissions(ctx, owner, codes...)
}

// pinnedTo returns a copy that reads authorization through the mutation
// transaction, so a grantability decision sees the policy the fence protects.
func (s *Service) pinnedTo(repo *Repository) *Service {
	clone := *s
	clone.repo = repo
	if checker, ok := s.checker.(*Authorizer); ok {
		pinned := *checker
		pinned.db = repo.writer()
		pinned.currentRead = repo.writer().Dialector.Name() == "mysql"
		clone.checker = &pinned
	}
	return &clone
}
