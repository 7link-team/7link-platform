package core

import (
	"context"
	"fmt"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen"
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

// pageOf returns the window offset and limit select from items, together with
// the number of items before the window was applied. It serves the listings a
// SQL window cannot bound faithfully -- a de-duplicated union of two tables, a
// set grouped per principal across two tables, or a projection of the declared
// field catalog -- where the whole set has to be assembled before it can be cut.
func pageOf[T any](items []T, offset, limit int) ([]T, int64) {
	total := int64(len(items))
	offset, limit = normalizePage(offset, limit)
	if offset >= len(items) {
		return items[len(items):], total
	}
	window := items[offset:]
	if limit > 0 && limit < len(window) {
		window = window[:limit]
	}
	return window, total
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

// ListEntityRoleBindingsPage returns a stable page of the direct role bindings
// on one tenant entity and the number of bindings before pagination. The entity
// is read first so an unknown entity is not reported as an empty binding set.
func (r *Repository) ListEntityRoleBindingsPage(
	ctx context.Context,
	tenantID, entityID guid.ID,
	offset, limit int,
) ([]EntityRoleBinding, int64, error) {
	if _, err := r.getEntityRow(ctx, tenantID, entityID); err != nil {
		return nil, 0, err
	}
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	conditions := []gen.Condition{
		tables.RoleBindings.TenantID.Eq(tenantID),
		tables.RoleBindings.ScopeType.Eq(entityBindingScope),
		tables.RoleBindings.ScopeID.Eq(entityID),
	}
	total, err := tables.RoleBindings.WithContext(ctx).Where(conditions...).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count entity role bindings: %w", err)
	}
	listing := tables.RoleBindings.WithContext(ctx).Where(conditions...).
		Order(tables.RoleBindings.RoleID, tables.RoleBindings.PrincipalID)
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	rows, err := listing.Find()
	if err != nil {
		return nil, 0, fmt.Errorf("list entity role bindings: %w", err)
	}
	bindings := make([]EntityRoleBinding, len(rows))
	for index, row := range rows {
		bindings[index] = entityRoleBindingFromRow(row)
	}
	return bindings, total, nil
}

// ListRelationshipsPage returns a stable page of the relationships a filter
// selects and the number of matches before pagination. The filter picks the arm
// the way ListRelationships does: an entity-scoped filter reads that entity's
// business-resource grants, and an unscoped one the tenant's entity grants.
func (r *Repository) ListRelationshipsPage(
	ctx context.Context,
	tenantID guid.ID,
	filter RelationshipFilter,
	offset, limit int,
) ([]Relationship, int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, 0, err
	}
	offset, limit = normalizePage(offset, limit)
	if !filter.EntityID.Zero() {
		resources := tables.ResourceRelationships
		conditions := []gen.Condition{
			resources.TenantID.Eq(tenantID), resources.EntityID.Eq(filter.EntityID),
		}
		if filter.ResourceType != "" {
			conditions = append(conditions, resources.ResourceType.Eq(filter.ResourceType))
		}
		if !filter.ResourceID.Zero() {
			conditions = append(conditions, resources.ResourceID.Eq(filter.ResourceID))
		}
		total, err := resources.WithContext(ctx).Where(conditions...).Count()
		if err != nil {
			return nil, 0, fmt.Errorf("count IAM resource relationships: %w", err)
		}
		listing := resources.WithContext(ctx).Where(conditions...).Order(
			resources.ResourceType, resources.ResourceID, resources.Relation,
			resources.SubjectType, resources.SubjectID, resources.SubjectRelation,
		)
		if limit > 0 {
			listing = listing.Limit(limit)
		}
		if offset > 0 {
			listing = listing.Offset(offset)
		}
		rows, err := listing.Find()
		if err != nil {
			return nil, 0, fmt.Errorf("list IAM resource relationships: %w", err)
		}
		result := make([]Relationship, len(rows))
		for index, row := range rows {
			result[index] = resourceRelationshipFromRow(row)
		}
		return result, total, nil
	}

	entities := tables.Relationships
	conditions := []gen.Condition{entities.TenantID.Eq(tenantID)}
	if filter.ResourceType != "" {
		conditions = append(conditions, entities.ResourceType.Eq(filter.ResourceType))
	}
	if !filter.ResourceID.Zero() {
		conditions = append(conditions, entities.ResourceID.Eq(filter.ResourceID))
	}
	total, err := entities.WithContext(ctx).Where(conditions...).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count IAM relationships: %w", err)
	}
	listing := entities.WithContext(ctx).Where(conditions...).Order(
		entities.ResourceType, entities.ResourceID, entities.Relation,
		entities.SubjectType, entities.SubjectID, entities.SubjectRelation,
	)
	if limit > 0 {
		listing = listing.Limit(limit)
	}
	if offset > 0 {
		listing = listing.Offset(offset)
	}
	rows, err := listing.Find()
	if err != nil {
		return nil, 0, fmt.Errorf("list IAM relationships: %w", err)
	}
	result := make([]Relationship, len(rows))
	for index, row := range rows {
		result[index] = relationshipFromRow(row)
	}
	return result, total, nil
}

// ListDirectoryBindingsPage returns a stable page of a role's group assignments.
func (r *Repository) ListDirectoryBindingsPage(
	ctx context.Context,
	tenantID, roleID guid.ID,
	offset, limit int,
) ([]RoleDirectoryBinding, int64, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, 0, err
	}
	directory, err := repo.directoryTables()
	if err != nil {
		return nil, 0, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return nil, 0, err
	}
	db := repo.writer().WithContext(ctx)
	offset, limit = normalizePage(offset, limit)
	var groupTotal int64
	if err := db.Table(directory.GroupRoleBindings.String()).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Count(&groupTotal).Error; err != nil {
		return nil, 0, fmt.Errorf("count group role bindings: %w", err)
	}
	bounded := limit > 0
	remaining := limit
	bindings := make([]RoleDirectoryBinding, 0)
	if int64(offset) < groupTotal {
		listing := db.Table(directory.GroupRoleBindings.String()).
			Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Order("group_id ASC")
		if bounded {
			listing = listing.Limit(remaining)
		}
		if offset > 0 {
			listing = listing.Offset(offset)
		}
		var groups []groupRoleBindingRecord
		if err := listing.Find(&groups).Error; err != nil {
			return nil, 0, fmt.Errorf("list group role bindings: %w", err)
		}
		for _, row := range groups {
			bindings = append(bindings, RoleDirectoryBinding{
				RoleID: row.RoleID, AssigneeType: DirectoryAssigneeGroup,
				AssigneeID: row.GroupID, CreatedAt: row.CreatedAt.UTC(),
			})
		}
	}
	return bindings, groupTotal, nil
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

// ListEntityRoleBindingsPage validates the entity reference and returns one page
// of its direct bindings with the unfiltered total.
func (s *Service) ListEntityRoleBindingsPage(
	ctx context.Context,
	tenantID, entityID guid.ID,
	offset, limit int,
) ([]EntityRoleBinding, int64, error) {
	if err := validateEntityRef(tenantID, entityID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListEntityRoleBindingsPage(ctx, tenantID, entityID, offset, limit)
}

// ListRelationshipsPage validates the tenant filter the way ListRelationships
// does and returns one page of the matches with the unfiltered total.
func (s *Service) ListRelationshipsPage(
	ctx context.Context,
	tenantID guid.ID,
	filter RelationshipFilter,
	offset, limit int,
) ([]Relationship, int64, error) {
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	if err := validateTenant(tenantID); err != nil ||
		(filter.ResourceType != "" && !validEntityType(filter.ResourceType)) {
		return nil, 0, fmt.Errorf("%w: invalid relationship filter", ErrInvalidArgument)
	}
	return s.repo.ListRelationshipsPage(ctx, tenantID, filter, offset, limit)
}

// ListDirectoryBindingsPage validates the role reference and returns one page of
// its group assignments with the unfiltered total.
func (s *Service) ListDirectoryBindingsPage(
	ctx context.Context,
	tenantID, roleID guid.ID,
	offset, limit int,
) ([]RoleDirectoryBinding, int64, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, 0, err
	}
	return s.repo.ListDirectoryBindingsPage(ctx, tenantID, roleID, offset, limit)
}

// ListTenantMemberRolesPage returns one page of a member's role IDs with the
// unfiltered total. The set is the union of the permanent memberships and the
// currently active temporary grants, de-duplicated across both tables, so the
// window is taken after the merge rather than in either table's query.
func (s *Service) ListTenantMemberRolesPage(
	ctx context.Context,
	tenantID, principalID guid.ID,
	offset, limit int,
) ([]guid.ID, int64, error) {
	roles, err := s.ListTenantMemberRoles(ctx, tenantID, principalID)
	if err != nil {
		return nil, 0, err
	}
	page, total := pageOf(roles, offset, limit)
	return page, total, nil
}

// ListPlatformAdministratorsPage returns one page of the platform
// administrators with the unfiltered total. A principal is assembled from a full
// row and any number of grant rows across two tables, so the window is taken
// after the grouping, which is what decides how many administrators there are.
func (s *Service) ListPlatformAdministratorsPage(
	ctx context.Context,
	offset, limit int,
) ([]PlatformAdministrator, int64, error) {
	administrators, err := s.ListPlatformAdministrators(ctx)
	if err != nil {
		return nil, 0, err
	}
	page, total := pageOf(administrators, offset, limit)
	return page, total, nil
}

// ListFieldPoliciesPage returns one page of the managed field policies with the
// unfiltered total. The listing is a projection of the declared field catalog
// onto the tenant's stored rows, so it exists only once both have been read.
func (s *Service) ListFieldPoliciesPage(
	ctx context.Context,
	tenantID guid.ID,
	resource string,
	offset, limit int,
) ([]ManagedFieldPolicy, int64, error) {
	policies, err := s.ListFieldPolicies(ctx, tenantID, resource)
	if err != nil {
		return nil, 0, err
	}
	page, total := pageOf(policies, offset, limit)
	return page, total, nil
}
