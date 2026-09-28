package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	codes, err := s.requireGrantableRolePermissions(ctx, repo, tenantID, roleID)
	if err != nil || codes == nil {
		return err
	}
	return s.requireGrantableDataScope(ctx, repo, tenantID, roleID, nil, codes)
}

// requireGrantableRolePermissions requires the actor to hold every permission
// of the role and returns them. It returns nil codes without an actor.
func (s *Service) requireGrantableRolePermissions(
	ctx context.Context,
	repo *Repository,
	tenantID, roleID guid.ID,
) ([]string, error) {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil, nil
	}
	if err := s.requireGrantablePermissionsOn(ctx, repo, tenantID); err != nil {
		return nil, err
	}
	var role struct{ ID guid.ID }
	err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.Roles.String()).Select("id").Where("tenant_id = ? AND id = ?", tenantID, roleID).Take(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRoleNotFound
	}
	if err != nil {
		return nil, err
	}
	codes, err := rolePermissionCodes(ctx, repo, tenantID, roleID)
	if err != nil {
		return nil, err
	}
	if err := s.requireGrantablePermissionsOn(ctx, repo, tenantID, codes...); err != nil {
		return nil, err
	}
	if codes == nil {
		codes = []string{}
	}
	return codes, nil
}

func rolePermissionCodes(ctx context.Context, repo *Repository, tenantID, roleID guid.ID) ([]string, error) {
	var codes []string
	if err := systemCurrentRead(repo.writer().WithContext(ctx)).Table(repo.names.RolePermissions.String()).Select("permission_code").Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Find(&codes).Error; err != nil {
		return nil, err
	}
	return codes, nil
}

// requireGrantableDataScope refuses making anyone hold one of codes through
// a tenant role whose data scope reaches further than the actor's own
// constraint for that code. scope is the role's scope as it will be after the
// change, or nil for its current scope (no row means all). The role's scope is
// measured as it applies to the actor: all needs an unrestricted constraint;
// department and department-and-descendants need the actor's own department
// (and, with descendants, every department below it) inside the constraint;
// selected departments need each active selected department inside it; self
// is the narrowest scope of a permission the caller already proved the actor
// holds. Tenant or platform full administrators are unaffected. Callers
// already hold the role-grantability fence.
func (s *Service) requireGrantableDataScope(ctx context.Context, repo *Repository, tenantID, roleID guid.ID, scope *RoleDataScope, codes []string) error {
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() {
		return nil
	}
	scoped := make([]string, 0, len(codes))
	for _, code := range codes {
		action, known := s.registry.Find(code)
		if known && action.DataScoped && action.Scope != authz.ActionScopeSystem && action.Scope != authz.ActionScopeOperatingPlatform {
			scoped = append(scoped, code)
		}
	}
	if len(scoped) == 0 {
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
	if scope == nil {
		current, err := repo.GetRoleDataScope(ctx, tenantID, roleID)
		if err != nil {
			return err
		}
		scope = &current
	}
	reach, err := roleScopeReach(ctx, repo, tenantID, claims.PrincipalID, *scope)
	if err != nil {
		return err
	}
	for _, code := range scoped {
		constraint, err := pinned.checker.Constraint(ctx, tenantID, code, claims.PrincipalID)
		if err != nil {
			return err
		}
		if constraint.AllowAll {
			continue
		}
		if reach == nil || !departmentsWithin(reach, constraint.DepartmentIDs) {
			return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, code)
		}
	}
	return nil
}

// memberDepartmentGuard bounds a member's department change by the actor's own
// data scope. Department and department-and-descendants scopes follow the
// member's department, so placing a member -- the actor included -- in a
// department widens what those scopes reach for them, without any grant being
// written. Before the change it records, for every data-scoped tenant
// permission the member holds, the departments the member reaches; the
// returned check runs after the change in the same transaction and refuses any
// department the member gains that the actor's own constraint for that
// permission, as it stood before the change, does not cover. Clearing or
// keeping a department, non-tenant roots and tenant or platform full
// administrators are unaffected, as in requireGrantableDataScope.
func (s *Service) memberDepartmentGuard(ctx context.Context, repo *Repository, kind string, tenantID, principalID, departmentID guid.ID) (func() error, error) {
	unaffected := func() error { return nil }
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID.Zero() || kind != "tenant" || departmentID.Zero() {
		return unaffected, nil
	}
	current, err := repo.GetMember(ctx, tenantID, principalID)
	if err != nil && !errors.Is(err, ErrMemberNotFound) {
		return nil, err
	}
	if err == nil && current.DepartmentID == departmentID {
		return unaffected, nil
	}
	pinned := s.pinnedTo(repo)
	administrator, err := pinned.checker.CheckBulk(ctx, tenantID, []string{tenantAdministratorPermission}, claims.PrincipalID)
	if err != nil || administrator[tenantAdministratorPermission] {
		return unaffected, err
	}
	codes := make([]string, 0)
	for _, action := range s.registry.All() {
		if action.DataScoped && action.Scope != authz.ActionScopeSystem && action.Scope != authz.ActionScopeOperatingPlatform {
			codes = append(codes, action.Code)
		}
	}
	reach := func() (map[string][]string, error) {
		held, err := pinned.checker.CheckBulk(ctx, tenantID, codes, principalID)
		if err != nil {
			return nil, err
		}
		departments := map[string][]string{}
		for _, code := range codes {
			if !held[code] {
				continue
			}
			constraint, err := pinned.checker.Constraint(ctx, tenantID, code, principalID)
			if err != nil {
				return nil, err
			}
			departments[code] = constraint.DepartmentIDs
		}
		return departments, nil
	}
	before, err := reach()
	if err != nil {
		return nil, err
	}
	actor := map[string]authz.DataConstraint{}
	for _, code := range codes {
		if actor[code], err = pinned.checker.Constraint(ctx, tenantID, code, claims.PrincipalID); err != nil {
			return nil, err
		}
	}
	return func() error {
		after, err := reach()
		if err != nil {
			return err
		}
		for code, departments := range after {
			for _, department := range departments {
				if slices.Contains(before[code], department) || actor[code].AllowAll || slices.Contains(actor[code].DepartmentIDs, department) {
					continue
				}
				return fmt.Errorf("%w: %s", ErrPrivilegeEscalation, code)
			}
		}
		return nil
	}, nil
}

// roleScopeReach returns the departments scope reaches when applied to the
// actor: an empty set for self, nil when it cannot be bounded by departments
// (all, or a department scope for an actor without an active department).
func roleScopeReach(ctx context.Context, repo *Repository, tenantID, actor guid.ID, scope RoleDataScope) ([]guid.ID, error) {
	switch scope.Scope {
	case DataScopeSelf:
		return []guid.ID{}, nil
	case DataScopeSelectedDepartments:
		statuses, err := repo.departmentStatuses(ctx, tenantID, scope.DepartmentIDs)
		if err != nil {
			return nil, err
		}
		reach := []guid.ID{}
		for _, id := range scope.DepartmentIDs {
			if statuses[id] == "active" {
				reach = append(reach, id)
			}
		}
		return reach, nil
	case DataScopeDepartment, DataScopeDepartmentAndDescendants:
		directory, err := repo.directoryTables()
		if err != nil {
			return nil, err
		}
		reach, err := memberDepartmentScope(ctx, repo.writer(), directory, tenantID, actor,
			scope.Scope == DataScopeDepartment, scope.Scope == DataScopeDepartmentAndDescendants)
		if err != nil || len(reach) == 0 {
			return nil, err
		}
		return reach, nil
	default:
		return nil, nil
	}
}

func departmentsWithin(reach []guid.ID, allowed []string) bool {
	for _, id := range reach {
		if !slices.Contains(allowed, id.String()) {
			return false
		}
	}
	return true
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
