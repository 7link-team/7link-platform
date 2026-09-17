package core

import (
	"context"
	"fmt"

	"7link.com/platform/internal/infra/guid"
)

const maxPageLimit = 200

func normalizePage(offset, limit int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	return offset, limit
}

// ListEntitiesPage returns a stable tenant entity page and the total number of
// tenant entities before pagination.
func (r *Repository) ListEntitiesPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Entity, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	total, err := tables.Entities.WithContext(ctx).
		Where(tables.Entities.TenantID.Eq(tenantID)).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count entities: %w", err)
	}
	listing := tables.Entities.WithContext(ctx).
		Where(tables.Entities.TenantID.Eq(tenantID)).Order(
		tables.Entities.ParentID,
		tables.Entities.Type,
		tables.Entities.Name,
		tables.Entities.ID,
	)
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	rows, err := listing.Find()
	if err != nil {
		return nil, 0, fmt.Errorf("list entities: %w", err)
	}
	entities, err := entitiesFromRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return entities, total, nil
}

// ListRolesPage returns a stable tenant role page and the total number of
// tenant roles before pagination.
func (r *Repository) ListRolesPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Role, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	total, err := tables.Roles.WithContext(ctx).
		Where(tables.Roles.TenantID.Eq(tenantID)).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count roles: %w", err)
	}
	listing := tables.Roles.WithContext(ctx).
		Where(tables.Roles.TenantID.Eq(tenantID)).
		Order(tables.Roles.Name, tables.Roles.ID)
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	rows, err := listing.Find()
	if err != nil {
		return nil, 0, fmt.Errorf("list roles: %w", err)
	}
	roles := make([]Role, len(rows))
	for index, row := range rows {
		roles[index] = roleFromRow(row)
	}
	return roles, total, nil
}

// ListRoleMembersPage returns a stable page of the principals holding a role and
// the total number of holders before pagination. The role is checked first so
// an unknown role is not reported as an empty membership.
func (r *Repository) ListRoleMembersPage(
	ctx context.Context,
	tenantID, roleID guid.ID,
	offset, limit int,
) ([]guid.ID, int64, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return nil, 0, err
	}
	tables, _, err := repo.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	total, err := tables.RoleMembers.WithContext(ctx).
		Where(tables.RoleMembers.TenantID.Eq(tenantID), tables.RoleMembers.RoleID.Eq(roleID)).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count role members: %w", err)
	}
	listing := tables.RoleMembers.WithContext(ctx).
		Select(tables.RoleMembers.PrincipalID).
		Where(tables.RoleMembers.TenantID.Eq(tenantID), tables.RoleMembers.RoleID.Eq(roleID)).
		Order(tables.RoleMembers.PrincipalID)
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	rows, err := listing.Find()
	if err != nil {
		return nil, 0, fmt.Errorf("list role members: %w", err)
	}
	principals := make([]guid.ID, len(rows))
	for index, row := range rows {
		principals[index] = row.PrincipalID
	}
	return principals, total, nil
}

// ListEntitiesPage validates tenant scope and returns one entity page with its
// unfiltered tenant total.
func (s *Service) ListEntitiesPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Entity, int64, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListEntitiesPage(ctx, tenantID, offset, limit)
}

// ListRolesPage validates tenant scope and returns one role page with its
// unfiltered tenant total.
func (s *Service) ListRolesPage(
	ctx context.Context,
	tenantID guid.ID,
	offset, limit int,
) ([]Role, int64, error) {
	if err := validateTenant(tenantID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListRolesPage(ctx, tenantID, offset, limit)
}

// ListRoleMembersPage validates the role reference and returns one page of its
// holders with the unfiltered total.
func (s *Service) ListRoleMembersPage(
	ctx context.Context,
	tenantID, roleID guid.ID,
	offset, limit int,
) ([]guid.ID, int64, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListRoleMembersPage(ctx, tenantID, roleID, offset, limit)
}
