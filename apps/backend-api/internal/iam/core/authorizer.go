package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

const policySnapshotAttempts = 3

// Authorizer evaluates declared platform, tenant, entity, and business
// resource permissions against the current policy revision.
type Authorizer struct {
	runtime          *appenv.Runtime
	db               *gorm.DB
	prefix           string
	registry         *authz.Registry
	revisions        *RevisionStore
	now              func() time.Time
	directorySources DirectoryAuthorizationSourceStore
}

type tenantGrant struct {
	PermissionCode string  `gorm:"column:permission_code"`
	RoleID         guid.ID `gorm:"column:role_id"`
	SourceType     string  `gorm:"column:source_type"`
	SourceID       guid.ID `gorm:"column:source_id"`
	DataScope      string  `gorm:"column:data_scope"`
	ConditionJSON  string  `gorm:"column:condition_json"`
}

type scopedGrant struct {
	PermissionCode string  `gorm:"column:permission_code"`
	RoleID         guid.ID `gorm:"column:role_id"`
	Effect         string  `gorm:"column:effect"`
	ScopeID        guid.ID `gorm:"column:scope_id"`
	EntityID       guid.ID `gorm:"column:entity_id"`
	Depth          int     `gorm:"column:depth"`
	ConditionJSON  string  `gorm:"column:condition_json"`
}

type authorizationSnapshot struct {
	revision           int64
	tenantActive       bool
	memberActive       bool
	entityIDs          []guid.ID
	tenantGrants       []tenantGrant
	scopedGrants       []scopedGrant
	relationshipGrants []relationshipGrant
	ownerIDs           []guid.ID
	departmentIDs      []guid.ID
}

// NewAuthorizer constructs an authorizer with the process-wide default action
// registry.
func NewAuthorizer(runtime *appenv.Runtime) *Authorizer {
	return NewAuthorizerWithRegistry(runtime, authz.DefaultRegistry())
}

// NewAuthorizerWithRegistry constructs an authorizer with an explicit action
// registry and the runtime's current writer.
func NewAuthorizerWithRegistry(runtime *appenv.Runtime, registry *authz.Registry) *Authorizer {
	if runtime == nil || runtime.Database() == nil {
		panic("iam authorizer requires database runtime")
	}
	if registry == nil {
		panic("iam authorizer requires authorization registry")
	}
	revisions, err := NewRevisionStore(runtime.TablePrefix())
	if err != nil {
		panic(err)
	}
	return &Authorizer{
		runtime: runtime, prefix: runtime.TablePrefix(), registry: registry,
		revisions: revisions, now: time.Now,
	}
}

func (a *Authorizer) pinWriter() (*Authorizer, error) {
	if a == nil || a.registry == nil || a.revisions == nil || a.now == nil {
		return nil, ErrUnavailable
	}
	if a.db != nil {
		return a, nil
	}
	if a.runtime == nil || a.runtime.Database() == nil {
		return nil, ErrUnavailable
	}
	db := a.runtime.Database().Write()
	if db == nil {
		return nil, ErrUnavailable
	}
	pinned := *a
	pinned.db = db
	return &pinned, nil
}

func (a *Authorizer) table(name string) string { return a.prefix + name }

// Check reports whether subject has one declared permission in a tenant.
func (a *Authorizer) Check(
	ctx context.Context,
	tenantID guid.ID,
	permission string,
	subject guid.ID,
) (bool, error) {
	result, err := a.CheckBulk(ctx, tenantID, []string{permission}, subject)
	return result[permission], err
}

// CheckPlatform reports whether an active principal has one declared platform
// permission, independently of tenant membership.
func (a *Authorizer) CheckPlatform(ctx context.Context, permission string, subject guid.ID) (bool, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return false, err
	}
	if pinned != a {
		return pinned.CheckPlatform(ctx, permission, subject)
	}
	if permission == "" || subject.Zero() || len(permission) > 128 {
		return false, errors.New("platform permission and subject are required")
	}
	if action, ok := a.registry.Find(permission); !ok || action.Scope != "platform" {
		return false, nil
	}
	var count int64
	err = a.db.WithContext(ctx).Table(a.table("iam_principals")+" AS p").
		Where("p.id = ? AND p.status = 'active'", subject).
		Where("(EXISTS (SELECT 1 FROM "+a.table("iam_platform_administrators")+" AS pa WHERE pa.principal_id = p.id)"+
			" OR EXISTS (SELECT 1 FROM "+a.table("iam_platform_grants")+" AS pg WHERE pg.principal_id = p.id AND pg.permission_code = ?))", permission).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("evaluate platform permission: %w", err)
	}
	return count == 1, nil
}

// CheckBulk evaluates declared permissions for one tenant subject. Unknown
// permission codes are denied rather than expanded through administrator roles.
func (a *Authorizer) CheckBulk(
	ctx context.Context,
	tenantID guid.ID,
	permissions []string,
	subject guid.ID,
) (map[string]bool, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return nil, err
	}
	if pinned != a {
		return pinned.CheckBulk(ctx, tenantID, permissions, subject)
	}
	allowed := make(map[string]bool, len(permissions))
	if len(permissions) == 0 {
		return allowed, nil
	}
	tenantPermissions := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		if permission == "" {
			return nil, errors.New("permission is empty")
		}
		if action, ok := a.registry.Find(permission); ok && action.Scope != "platform" {
			tenantPermissions = append(tenantPermissions, permission)
		}
	}
	requested, err := requestedPermissions(tenantPermissions...)
	if err != nil {
		return nil, err
	}
	_, memberActive, err := tenantMembershipState(ctx, a.db, a.prefix, tenantID, subject)
	if err != nil {
		return nil, err
	}
	if !memberActive {
		return allowed, nil
	}
	trusted := policyx.TrustedFromContext(ctx, a.now())
	grants, err := a.loadTenantGrants(ctx, tenantID, subject, requested, trusted)
	if err != nil {
		return nil, err
	}
	grantSet := make(map[string]bool, len(grants))
	for _, grant := range grants {
		grantSet[grant.PermissionCode] = true
	}
	platformAdministrator, err := a.CheckPlatform(ctx, "platform_administer", subject)
	if err != nil {
		return nil, err
	}
	for _, permission := range permissions {
		action, known := a.registry.Find(permission)
		if !known {
			allowed[permission] = false
		} else if action.Scope == "platform" {
			allowed[permission] = platformAdministrator
		} else {
			allowed[permission] = grantSet[permission] || grantSet["tenant_administer"] || platformAdministrator
		}
	}
	return allowed, nil
}

// Constraint compiles one data-scoped permission into concrete entity,
// ancestor, owner, and department filters.
func (a *Authorizer) Constraint(
	ctx context.Context,
	tenantID guid.ID,
	permission string,
	subject guid.ID,
) (authz.DataConstraint, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return authz.DataConstraint{}, err
	}
	if pinned != a {
		return pinned.Constraint(ctx, tenantID, permission, subject)
	}
	if err := validateAuthorizationRequest(tenantID, permission, subject); err != nil {
		return authz.DataConstraint{}, err
	}
	trusted := policyx.TrustedFromContext(ctx, a.now())
	snapshot, err := a.snapshot(ctx, tenantID, 0, permission, subject, true, trusted)
	if err != nil {
		return authz.DataConstraint{}, err
	}
	return compileDataConstraint(a.registry, snapshot, permission), nil
}

// EntityConstraint compiles row filters for one active entity without granting
// whole-entity access to self/department-only roles. The state and grants come
// from the same revision-checked snapshot, including explicit entity denials.
func (a *Authorizer) EntityConstraint(ctx context.Context, tenantID, entityID guid.ID, permission string, subject guid.ID) (authz.DataConstraint, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return authz.DataConstraint{}, err
	}
	if pinned != a {
		return pinned.EntityConstraint(ctx, tenantID, entityID, permission, subject)
	}
	if err := validateEntityAuthorizationRequest(tenantID, entityID, permission, subject); err != nil {
		return authz.DataConstraint{}, err
	}
	action, known := a.registry.Find(permission)
	if !known || !action.DataScoped || action.Scope == "platform" {
		return compileDataConstraint(a.registry, authorizationSnapshot{}, permission), nil
	}
	snapshot, err := a.snapshot(ctx, tenantID, entityID, permission, subject, true, policyx.TrustedFromContext(ctx, a.now()))
	if err != nil {
		return authz.DataConstraint{}, err
	}
	if !contains(snapshot.entityIDs, entityID) {
		return compileDataConstraint(a.registry, authorizationSnapshot{revision: snapshot.revision}, permission), nil
	}
	constraint := compileDataConstraint(a.registry, snapshot, permission)
	if len(constraint.DeniedIDs) > 0 {
		constraint.AllowAll = false
		constraint.ResourceIDs = []string{}
		constraint.OwnerIDs = []string{}
		constraint.DepartmentIDs = []string{}
	}
	return constraint, nil
}

func compileDataConstraint(registry *authz.Registry, snapshot authorizationSnapshot, permission string) authz.DataConstraint {
	constraint := authz.DataConstraint{
		OwnerIDs: []string{}, ResourceIDs: []string{}, DepartmentIDs: []string{},
		Ancestors: []authz.ResourceRef{}, DeniedIDs: []string{}, Revision: snapshot.revision,
	}
	if !snapshot.memberActive {
		return constraint
	}
	constraint.OwnerIDs = guidStrings(snapshot.ownerIDs)
	constraint.DepartmentIDs = guidStrings(snapshot.departmentIDs)
	tenantRequested, tenantAdmin := tenantCapabilities(snapshot.tenantGrants, permission)
	action, _ := registry.Find(permission)
	relationshipsByEntity := allowedRelationshipsByEntity(action, snapshot.relationshipGrants)
	constraint.AllowAll = tenantRequested || tenantAdmin
	ancestorSet := map[guid.ID]bool{}
	for _, grant := range snapshot.scopedGrants {
		if grant.Effect == "allow" && (grant.PermissionCode == permission || isAdministratorPermission(grant.PermissionCode)) {
			ancestorSet[grant.ScopeID] = true
		}
	}
	for id := range ancestorSet {
		constraint.Ancestors = append(constraint.Ancestors, authz.ResourceRef{Type: "entity", ID: id.String()})
	}
	sort.Slice(constraint.Ancestors, func(i, j int) bool { return constraint.Ancestors[i].ID < constraint.Ancestors[j].ID })
	grantsByEntity := scopedByEntity(snapshot.scopedGrants)
	for _, entityID := range snapshot.entityIDs {
		allowed, explicitlyDenied, _ := decideEntity(
			permission, tenantRequested, tenantAdmin, grantsByEntity[entityID], relationshipsByEntity[entityID],
		)
		if allowed {
			if !constraint.AllowAll {
				constraint.ResourceIDs = append(constraint.ResourceIDs, entityID.String())
			}
		} else if explicitlyDenied {
			constraint.DeniedIDs = append(constraint.DeniedIDs, entityID.String())
		}
	}
	return constraint
}

// ExplainEntity returns the effective authorization decision for one entity.
func (a *Authorizer) ExplainEntity(
	ctx context.Context,
	tenantID, entityID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return authz.Explanation{}, err
	}
	if pinned != a {
		return pinned.ExplainEntity(ctx, tenantID, entityID, permission, subject)
	}
	if err := validateEntityAuthorizationRequest(tenantID, entityID, permission, subject); err != nil {
		return authz.Explanation{}, err
	}
	trusted := policyx.TrustedFromContext(ctx, a.now())
	snapshot, err := a.snapshot(ctx, tenantID, entityID, permission, subject, false, trusted)
	if err != nil {
		return authz.Explanation{}, err
	}
	if !contains(snapshot.entityIDs, entityID) {
		return a.inactiveOrMissingEntity(ctx, tenantID, entityID, snapshot.revision)
	}
	return explainAuthorization(a.registry, snapshot, tenantID, entityID, "", 0, permission), nil
}

// ExplainResource returns the effective authorization decision for one
// business resource owned by an entity.
func (a *Authorizer) ExplainResource(
	ctx context.Context,
	tenantID, entityID guid.ID,
	resourceType string,
	resourceID guid.ID,
	permission string,
	subject guid.ID,
) (authz.Explanation, error) {
	pinned, err := a.pinWriter()
	if err != nil {
		return authz.Explanation{}, err
	}
	if pinned != a {
		return pinned.ExplainResource(ctx, tenantID, entityID, resourceType, resourceID, permission, subject)
	}
	if err := validateEntityAuthorizationRequest(tenantID, entityID, permission, subject); err != nil ||
		resourceType == "" || resourceID.Zero() || len(resourceType) > 64 {
		return authz.Explanation{}, fmt.Errorf("%w: invalid business resource authorization request", ErrInvalidArgument)
	}
	trusted := policyx.TrustedFromContext(ctx, a.now())
	trusted.Resource.Type = resourceType
	trusted.Resource.ID = resourceID.String()
	snapshot, err := a.snapshot(ctx, tenantID, entityID, permission, subject, false, trusted)
	if err != nil {
		return authz.Explanation{}, err
	}
	if !contains(snapshot.entityIDs, entityID) {
		return a.inactiveOrMissingEntity(ctx, tenantID, entityID, snapshot.revision)
	}
	return explainAuthorization(a.registry, snapshot, tenantID, entityID, resourceType, resourceID, permission), nil
}

func (a *Authorizer) inactiveOrMissingEntity(
	ctx context.Context,
	tenantID, entityID guid.ID,
	revision int64,
) (authz.Explanation, error) {
	var count int64
	if err := a.db.WithContext(ctx).Table(a.table("iam_entities")).
		Where("tenant_id = ? AND id = ?", tenantID, entityID).Count(&count).Error; err != nil {
		return authz.Explanation{}, fmt.Errorf("get authorization entity state: %w", err)
	}
	if count == 0 {
		return authz.Explanation{}, ErrEntityNotFound
	}
	return authz.Explanation{
		Reason: "inactive_resource", Revision: revision, Matches: []authz.DecisionMatch{},
	}, nil
}

func explainAuthorization(
	registry *authz.Registry,
	snapshot authorizationSnapshot,
	tenantID, entityID guid.ID,
	resourceType string,
	resourceID guid.ID,
	permission string,
) authz.Explanation {
	explanation := authz.Explanation{
		Reason: "no_matching_grant", Revision: snapshot.revision, Matches: []authz.DecisionMatch{},
	}
	if !snapshot.memberActive {
		if !snapshot.tenantActive {
			explanation.Reason = "inactive_tenant"
		} else {
			explanation.Reason = "inactive_membership"
		}
		return explanation
	}
	tenantRequested, tenantAdmin := tenantCapabilities(snapshot.tenantGrants, permission)
	action, _ := registry.Find(permission)
	for _, grant := range snapshot.tenantGrants {
		explanation.Matches = append(explanation.Matches, authz.DecisionMatch{
			PermissionCode: grant.PermissionCode, RoleID: grant.RoleID.String(),
			SourceType: grant.SourceType, SourceID: grant.SourceID.String(),
			ScopeType: "tenant", ScopeID: tenantID.String(), Effect: "allow",
		})
	}
	scoped := scopedByEntity(snapshot.scopedGrants)[entityID]
	for _, grant := range scoped {
		explanation.Matches = append(explanation.Matches, authz.DecisionMatch{
			PermissionCode: grant.PermissionCode, RoleID: grant.RoleID.String(),
			SourceType: "entity_binding", SourceID: grant.ScopeID.String(),
			ScopeType: "entity", ScopeID: grant.ScopeID.String(), Effect: grant.Effect,
			Inherited: grant.Depth > 0,
		})
	}
	relationshipGranted := false
	for _, grant := range snapshot.relationshipGrants {
		entityTarget := resourceType == "" && grant.EntityID.Zero() && grant.ResourceID == entityID
		resourceTarget := resourceType != "" && grant.EntityID == entityID &&
			grant.ResourceType == resourceType && grant.ResourceID == resourceID
		if (!entityTarget && !resourceTarget) || !relationshipAllowed(action, grant) {
			continue
		}
		relationshipGranted = true
		explanation.Matches = append(explanation.Matches, authz.DecisionMatch{
			PermissionCode: permission, SourceType: "relationship", SourceID: grant.Path[0].SubjectID,
			ScopeType: grant.ResourceType, ScopeID: grant.ResourceID.String(), Effect: "allow",
			Relation: grant.Relation, Path: grant.Path,
		})
	}
	explanation.Allowed, _, explanation.Reason = decideEntity(
		permission, tenantRequested, tenantAdmin, scoped, relationshipGranted,
	)
	sort.Slice(explanation.Matches, func(i, j int) bool {
		left, right := explanation.Matches[i], explanation.Matches[j]
		if left.Effect != right.Effect {
			return left.Effect < right.Effect
		}
		if left.PermissionCode != right.PermissionCode {
			return left.PermissionCode < right.PermissionCode
		}
		if left.ScopeID != right.ScopeID {
			return left.ScopeID < right.ScopeID
		}
		return left.RoleID < right.RoleID
	})
	return explanation
}

// CheckEntity reports whether subject has permission on one entity.
func (a *Authorizer) CheckEntity(
	ctx context.Context,
	tenantID, entityID guid.ID,
	permission string,
	subject guid.ID,
) (bool, error) {
	explanation, err := a.ExplainEntity(ctx, tenantID, entityID, permission, subject)
	return explanation.Allowed, err
}

// CheckResource reports whether subject has permission on one business
// resource owned by an entity.
func (a *Authorizer) CheckResource(
	ctx context.Context,
	tenantID, entityID guid.ID,
	resourceType string,
	resourceID guid.ID,
	permission string,
	subject guid.ID,
) (bool, error) {
	explanation, err := a.ExplainResource(ctx, tenantID, entityID, resourceType, resourceID, permission, subject)
	return explanation.Allowed, err
}

func (a *Authorizer) snapshot(
	ctx context.Context,
	tenantID, focusEntityID guid.ID,
	permission string,
	subject guid.ID,
	includeDataScope bool,
	trusted policyx.TrustedContext,
) (authorizationSnapshot, error) {
	requested, _ := requestedPermissions(permission)
	for range policySnapshotAttempts {
		before, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return authorizationSnapshot{}, err
		}
		var snapshot authorizationSnapshot
		snapshot.tenantActive, snapshot.memberActive, err = tenantMembershipState(
			ctx, a.db, a.prefix, tenantID, subject,
		)
		if err != nil {
			return authorizationSnapshot{}, err
		}
		entities := a.db.WithContext(ctx).Table(a.table("iam_entities")).
			Where("tenant_id = ? AND status = ?", tenantID, EntityActive)
		if !focusEntityID.Zero() {
			entities = entities.Where("id = ?", focusEntityID)
		}
		if err := entities.Order("id ASC").Pluck("id", &snapshot.entityIDs).Error; err != nil {
			return authorizationSnapshot{}, fmt.Errorf("list authorization entities: %w", err)
		}
		if snapshot.memberActive {
			snapshot.tenantGrants, err = a.loadTenantGrants(ctx, tenantID, subject, requested, trusted)
			if err != nil {
				return authorizationSnapshot{}, err
			}
			if includeDataScope {
				snapshot.ownerIDs, snapshot.departmentIDs, err = a.loadDataScopeFacts(
					ctx, tenantID, subject, permission, snapshot.tenantGrants,
				)
				if err != nil {
					return authorizationSnapshot{}, err
				}
			}
			snapshot.scopedGrants, err = a.loadScopedGrants(ctx, tenantID, subject, requested, trusted)
			if err != nil {
				return authorizationSnapshot{}, err
			}
			if action, ok := a.registry.Find(permission); ok && len(action.AllowedRelations) > 0 {
				snapshot.relationshipGrants, err = a.loadRelationshipGrants(ctx, tenantID, subject, trusted)
				if err != nil {
					return authorizationSnapshot{}, err
				}
			}
		}
		after, err := a.revisions.Current(ctx, a.db, tenantID)
		if err != nil {
			return authorizationSnapshot{}, err
		}
		if before == after {
			snapshot.revision = after
			return snapshot, nil
		}
	}
	return authorizationSnapshot{}, ErrAuthorizationChanged
}

func (a *Authorizer) loadTenantGrants(
	ctx context.Context,
	tenantID, subject guid.ID,
	requested []string,
	trusted policyx.TrustedContext,
) ([]tenantGrant, error) {
	if tenantID.Zero() || subject.Zero() {
		return nil, errors.New("tenant and subject are required")
	}
	if len(requested) == 0 {
		return []tenantGrant{}, nil
	}
	now := trusted.Time.UTC().Truncate(time.Millisecond)
	p := a.prefix
	grants := make([]tenantGrant, 0)
	cte, args := effectiveRolesCTE(p, tenantID, subject, now)
	err := a.db.WithContext(ctx).Raw(cte+`
SELECT DISTINCT rp.permission_code, er.role_id, er.source_type, er.source_id,
       COALESCE(ds.scope_type, 'all') AS data_scope, rp.condition_json
FROM `+p+`iam_role_permissions rp
JOIN effective_roles er ON er.role_id = rp.role_id
LEFT JOIN `+p+`iam_role_data_scopes ds
  ON ds.tenant_id = rp.tenant_id AND ds.role_id = rp.role_id
WHERE rp.tenant_id = ? AND rp.permission_code IN ?`,
		append(args, tenantID, requested)...,
	).Scan(&grants).Error
	if err != nil {
		return nil, fmt.Errorf("evaluate tenant permissions: %w", err)
	}
	dynamicGroupIDs, err := matchingDynamicGroupIDs(ctx, a.db, a.prefix, tenantID, subject)
	if err != nil {
		return nil, err
	}
	if len(dynamicGroupIDs) > 0 {
		dynamicGrants := make([]tenantGrant, 0)
		err = a.db.WithContext(ctx).Table(a.table("iam_group_role_bindings")+" AS b").
			Select("rp.permission_code, b.role_id, 'group' AS source_type, b.group_id AS source_id, "+
				"COALESCE(ds.scope_type, 'all') AS data_scope, rp.condition_json").
			Joins("JOIN "+a.table("iam_role_permissions")+" AS rp ON rp.tenant_id = b.tenant_id AND rp.role_id = b.role_id").
			Joins("LEFT JOIN "+a.table("iam_role_data_scopes")+" AS ds ON ds.tenant_id = b.tenant_id AND ds.role_id = b.role_id").
			Where("b.tenant_id = ? AND b.group_id IN ? AND rp.permission_code IN ?", tenantID, dynamicGroupIDs, requested).
			Scan(&dynamicGrants).Error
		if err != nil {
			return nil, fmt.Errorf("evaluate dynamic group permissions: %w", err)
		}
		grants = append(grants, dynamicGrants...)
	}
	return matchingTenantGrants(grants, trusted)
}

// Effective role membership is shared by endpoint grants and field policies.
// Field restrictions also apply to assigned roles with no endpoint grants.
func effectiveRolesCTE(p string, tenantID, subject guid.ID, now time.Time) (string, []any) {
	return `
WITH effective_roles(role_id, source_type, source_id) AS (
    SELECT m.role_id, 'role_member', m.principal_id
    FROM ` + p + `iam_role_members m
    JOIN ` + p + `iam_tenant_members tm
      ON tm.tenant_id = m.tenant_id AND tm.principal_id = m.principal_id AND tm.status = ?
    WHERE m.tenant_id = ? AND m.principal_id = ?
    UNION
    SELECT grants.role_id, 'temporary_role', grants.id
    FROM ` + p + `iam_temporary_role_grants grants
    JOIN ` + p + `iam_tenant_members tm
      ON tm.tenant_id = grants.tenant_id AND tm.principal_id = grants.principal_id AND tm.status = ?
    WHERE grants.tenant_id = ? AND grants.principal_id = ?
      AND grants.starts_at <= ? AND grants.ends_at > ?
    UNION
    SELECT b.role_id, 'group', b.group_id
    FROM ` + p + `iam_group_role_bindings b
    JOIN ` + p + `iam_groups g
      ON g.tenant_id = b.tenant_id AND g.id = b.group_id AND g.status = 'active' AND g.group_type = 'static'
    JOIN ` + p + `iam_group_members gm
      ON gm.tenant_id = b.tenant_id AND gm.group_id = b.group_id
    JOIN ` + p + `iam_tenant_members tm
      ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND gm.principal_id = ?
      AND (gm.starts_at IS NULL OR gm.starts_at <= ?)
      AND (gm.ends_at IS NULL OR gm.ends_at > ?)
    UNION
    SELECT b.role_id, 'position', b.position_id
    FROM ` + p + `iam_position_role_bindings b
    JOIN ` + p + `iam_positions pos
      ON pos.tenant_id = b.tenant_id AND pos.id = b.position_id AND pos.status = 'active'
    JOIN ` + p + `iam_position_members pm
      ON pm.tenant_id = b.tenant_id AND pm.position_id = b.position_id
    JOIN ` + p + `iam_tenant_members tm
      ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND pm.principal_id = ?
      AND (pm.starts_at IS NULL OR pm.starts_at <= ?)
      AND (pm.ends_at IS NULL OR pm.ends_at > ?)
)`, []any{
			MemberActive, tenantID, subject,
			MemberActive, tenantID, subject, now, now,
			MemberActive, tenantID, subject, now, now,
			MemberActive, tenantID, subject, now, now,
		}
}

func (a *Authorizer) loadDataScopeFacts(
	ctx context.Context,
	tenantID, subject guid.ID,
	permission string,
	grants []tenantGrant,
) ([]guid.ID, []guid.ID, error) {
	ownerSet := map[guid.ID]struct{}{}
	selectedRoles := map[guid.ID]struct{}{}
	needDepartment, needDescendants := false, false
	for _, grant := range grants {
		if grant.PermissionCode != permission {
			continue
		}
		switch DataScope(grant.DataScope) {
		case DataScopeSelf:
			ownerSet[subject] = struct{}{}
		case DataScopeDepartment:
			needDepartment = true
		case DataScopeDepartmentAndDescendants:
			needDescendants = true
		case DataScopeSelectedDepartments:
			selectedRoles[grant.RoleID] = struct{}{}
		}
	}
	departmentSet := map[guid.ID]struct{}{}
	if needDepartment || needDescendants {
		var row struct {
			DepartmentID guid.ID `gorm:"column:department_id"`
		}
		err := a.db.WithContext(ctx).Table(a.table("iam_member_departments")+" AS md").
			Select("md.department_id").
			Joins("JOIN "+a.table("iam_departments")+" AS d ON d.tenant_id = md.tenant_id AND d.id = md.department_id AND d.status = 'active'").
			Where("md.tenant_id = ? AND md.principal_id = ?", tenantID, subject).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("get authorization member department: %w", err)
		}
		if !row.DepartmentID.Zero() {
			if needDepartment {
				departmentSet[row.DepartmentID] = struct{}{}
			}
			if needDescendants {
				ids := make([]guid.ID, 0)
				err = a.db.WithContext(ctx).Table(a.table("iam_department_closure")+" AS c").
					Select("c.descendant_id").
					Joins("JOIN "+a.table("iam_departments")+" AS d ON d.tenant_id = c.tenant_id AND d.id = c.descendant_id AND d.status = 'active'").
					Where("c.tenant_id = ? AND c.ancestor_id = ?", tenantID, row.DepartmentID).
					Pluck("c.descendant_id", &ids).Error
				if err != nil {
					return nil, nil, fmt.Errorf("list authorization department descendants: %w", err)
				}
				for _, id := range ids {
					departmentSet[id] = struct{}{}
				}
			}
		}
	}
	if len(selectedRoles) > 0 {
		roleIDs := make([]guid.ID, 0, len(selectedRoles))
		for roleID := range selectedRoles {
			roleIDs = append(roleIDs, roleID)
		}
		ids := make([]guid.ID, 0)
		err := a.db.WithContext(ctx).Table(a.table("iam_role_scope_departments")+" AS rsd").
			Distinct("rsd.department_id").
			Joins("JOIN "+a.table("iam_departments")+" AS d ON d.tenant_id = rsd.tenant_id AND d.id = rsd.department_id AND d.status = 'active'").
			Where("rsd.tenant_id = ? AND rsd.role_id IN ?", tenantID, roleIDs).
			Pluck("rsd.department_id", &ids).Error
		if err != nil {
			return nil, nil, fmt.Errorf("list authorization selected departments: %w", err)
		}
		for _, id := range ids {
			departmentSet[id] = struct{}{}
		}
	}
	return mapKeys(ownerSet), mapKeys(departmentSet), nil
}

func mapKeys(values map[guid.ID]struct{}) []guid.ID {
	result := make([]guid.ID, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func guidStrings(values []guid.ID) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.String())
	}
	return result
}

func (a *Authorizer) loadScopedGrants(
	ctx context.Context,
	tenantID, subject guid.ID,
	requested []string,
	trusted policyx.TrustedContext,
) ([]scopedGrant, error) {
	p := a.prefix
	grants := make([]scopedGrant, 0)
	err := a.db.WithContext(ctx).Raw(`
WITH RECURSIVE expanded(permission_code, role_id, effect, scope_id, condition_json, entity_id, depth) AS (
    SELECT p.permission_code, b.role_id, b.effect, b.scope_id, p.condition_json, e.id, 0
    FROM `+p+`iam_role_bindings b
    JOIN `+p+`iam_tenant_members tm
      ON tm.tenant_id = b.tenant_id AND tm.principal_id = b.principal_id AND tm.status = ?
    JOIN `+p+`iam_role_permissions p
      ON p.tenant_id = b.tenant_id AND p.role_id = b.role_id
    JOIN `+p+`iam_entities e
      ON e.tenant_id = b.tenant_id AND e.id = b.scope_id AND e.status = 'active'
    WHERE b.tenant_id = ? AND b.principal_id = ? AND b.scope_type = 'entity'
      AND (b.expires_at IS NULL OR b.expires_at > ?)
      AND p.permission_code IN ?
    UNION ALL
    SELECT x.permission_code, x.role_id, x.effect, x.scope_id, x.condition_json, e.id, x.depth + 1
    FROM expanded x
    JOIN `+p+`iam_entities e ON e.tenant_id = ? AND e.parent_id = x.entity_id AND e.status = 'active'
    WHERE x.depth < 15
)
SELECT DISTINCT permission_code, role_id, effect, scope_id, condition_json, entity_id, depth
FROM expanded`, MemberActive, tenantID, subject, trusted.Time.UTC().Truncate(time.Millisecond), requested, tenantID).
		Scan(&grants).Error
	if err != nil {
		return nil, fmt.Errorf("evaluate scoped entity permissions: %w", err)
	}
	return matchingScopedGrants(grants, trusted)
}

func matchingTenantGrants(grants []tenantGrant, trusted policyx.TrustedContext) ([]tenantGrant, error) {
	matched := make([]tenantGrant, 0, len(grants))
	for _, grant := range grants {
		ok, err := policyx.EvaluateCondition(json.RawMessage(grant.ConditionJSON), trusted)
		if err != nil {
			return nil, fmt.Errorf("evaluate role permission condition %s/%s: %w", grant.RoleID, grant.PermissionCode, err)
		}
		if ok {
			matched = append(matched, grant)
		}
	}
	return matched, nil
}

func matchingScopedGrants(grants []scopedGrant, trusted policyx.TrustedContext) ([]scopedGrant, error) {
	matched := make([]scopedGrant, 0, len(grants))
	for _, grant := range grants {
		ok, err := policyx.EvaluateCondition(json.RawMessage(grant.ConditionJSON), trusted)
		if err != nil {
			return nil, fmt.Errorf("evaluate scoped role permission condition %s/%s: %w", grant.RoleID, grant.PermissionCode, err)
		}
		if ok {
			matched = append(matched, grant)
		}
	}
	return matched, nil
}

func requestedPermissions(permissions ...string) ([]string, error) {
	requested := make([]string, 0, len(permissions)+1)
	seen := map[string]bool{}
	for _, permission := range append(append([]string{}, permissions...), "tenant_administer") {
		if permission == "" {
			return nil, errors.New("permission is empty")
		}
		if !seen[permission] {
			seen[permission] = true
			requested = append(requested, permission)
		}
	}
	return requested, nil
}

func validateAuthorizationRequest(tenantID guid.ID, permission string, subject guid.ID) error {
	if tenantID.Zero() || permission == "" || subject.Zero() || len(permission) > 128 {
		return fmt.Errorf("%w: tenant, permission, and subject are required", ErrInvalidArgument)
	}
	return nil
}

func validateEntityAuthorizationRequest(
	tenantID, entityID guid.ID,
	permission string,
	subject guid.ID,
) error {
	if entityID.Zero() {
		return fmt.Errorf("%w: entity is required", ErrInvalidArgument)
	}
	return validateAuthorizationRequest(tenantID, permission, subject)
}

func tenantCapabilities(grants []tenantGrant, permission string) (requested, administrator bool) {
	for _, grant := range grants {
		requested = requested || (grant.PermissionCode == permission && grant.DataScope == string(DataScopeAll))
		administrator = administrator || isAdministratorPermission(grant.PermissionCode)
	}
	return requested, administrator
}

func scopedByEntity(grants []scopedGrant) map[guid.ID][]scopedGrant {
	result := make(map[guid.ID][]scopedGrant)
	for _, grant := range grants {
		result[grant.EntityID] = append(result[grant.EntityID], grant)
	}
	return result
}

func decideEntity(
	permission string,
	tenantRequested, tenantAdmin bool,
	grants []scopedGrant,
	relationshipAllow bool,
) (allowed, explicitlyDenied bool, reason string) {
	requestedAllow, administratorAllow := tenantRequested, tenantAdmin
	requestedDeny, administratorDeny := false, false
	for _, grant := range grants {
		requested := grant.PermissionCode == permission
		administrator := isAdministratorPermission(grant.PermissionCode)
		if grant.Effect == "deny" {
			requestedDeny = requestedDeny || requested
			administratorDeny = administratorDeny || administrator
		} else {
			requestedAllow = requestedAllow || requested
			administratorAllow = administratorAllow || administrator
		}
	}
	if requestedDeny {
		return false, true, "explicit_deny"
	}
	if requestedAllow {
		return true, false, "permission_grant"
	}
	if relationshipAllow {
		return true, false, "relationship_grant"
	}
	if administratorDeny && administratorAllow {
		return false, true, "administrator_scope_denied"
	}
	if administratorAllow {
		return true, false, "administrator_grant"
	}
	return false, false, "no_matching_grant"
}

func allowedRelationshipsByEntity(action authz.Action, grants []relationshipGrant) map[guid.ID]bool {
	result := make(map[guid.ID]bool)
	for _, grant := range grants {
		if grant.EntityID.Zero() && relationshipAllowed(action, grant) {
			result[grant.ResourceID] = true
		}
	}
	return result
}

func isAdministratorPermission(permission string) bool { return permission == "tenant_administer" }

func contains(values []guid.ID, expected guid.ID) bool {
	index := sort.Search(len(values), func(i int) bool { return values[i] >= expected })
	return index < len(values) && values[index] == expected
}

var _ AuthorizationEvaluator = (*Authorizer)(nil)
var _ authz.PermissionChecker = (*Authorizer)(nil)
var _ authz.EntityPermissionChecker = (*Authorizer)(nil)
