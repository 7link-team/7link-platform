package query

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// The authorization engine asks four questions of the database: which roles a
// subject holds, which entity-scoped bindings those roles carry, which roles
// reach a directory node, and whether a tenant still has a durable
// administrator. Each question is one statement, and the engines that ask it --
// the authorizer, the field-policy evaluator and the administrator guard --
// share both the statements and the row shapes they scan into.
//
// The statements live here rather than in the module root because a prefix
// applied to half of a JOIN is invisible to a test that runs without one, and
// because the deployment's table names are already resolved in this package.
// Every statement is built by one expression that names the tables it reads, so
// changing which table a statement reads is a change to that expression alone.

// EffectiveRolesParams parameterizes the effective-role CTE. The CTE is shared
// by the tenant-grant statement and by the field-policy statement, which is why
// it is not inlined into either: a role that reaches one must reach the other.
type EffectiveRolesParams struct {
	AuthorizationTables
	TenantID guid.ID
	Subject  guid.ID
	Now      time.Time
	// MemberStatus is the membership status a role assignment requires of the
	// principal. Every branch of the CTE filters on it, so a status that does
	// not exist yields no roles rather than every role.
	MemberStatus string
	CurrentRead  bool
}

// CurrentTenantSource locks base rows inside derived queries. Locking the outer
// UNION/CTE does not make its sources current reads on MySQL.
func CurrentTenantSource(table gormx.TableName, owner guid.ID) string {
	return fmt.Sprintf("(SELECT * FROM %s WHERE tenant_id = %d FOR UPDATE)", table.String(), owner)
}

// TenantGrantParams parameterizes the statement that reads the permissions a
// subject holds in a tenant, through assigned roles, temporary grants, static
// groups.
type TenantGrantParams struct {
	EffectiveRolesParams
	Requested []string
}

// ScopedGrantParams parameterizes the statement that expands entity-scoped role
// bindings down the entity tree.
type ScopedGrantParams struct {
	AuthorizationTables
	TenantID     guid.ID
	Subject      guid.ID
	Requested    []string
	Now          time.Time
	MemberStatus string
}

// DirectoryAncestorParams parameterizes the statement that walks an entity's
// ancestors and reads the role bindings attached to any of them.
type DirectoryAncestorParams struct {
	Tables   TableNames
	TenantID guid.ID
	EntityID guid.ID
	Subject  guid.ID
	Now      time.Time
}

// RelationshipWalkNode is one (type, id, relation) node of a relationship walk.
type RelationshipWalkNode struct {
	Type     string
	ID       guid.ID
	Relation string
}

// RelationshipWalkParams parameterizes one direction of the relationship
// hierarchy walk.
type RelationshipWalkParams struct {
	Relationships gormx.TableName
	// Dialect selects the cast expressions the recursive term needs. A
	// recursive CTE compares the anchor's literals with the table's columns, and
	// the two must have the same type and collation on every engine.
	Dialect  string
	TenantID guid.ID
	MaxDepth int
	// Seed is the node the walk starts from.
	Seed RelationshipWalkNode
	// Against is the node the downstream walk must not reach. The upstream walk
	// only measures depth and leaves it zero.
	Against RelationshipWalkNode
}

// DurableAdministratorParams parameterizes the statement that counts the
// principals who still administer a tenant.
type DurableAdministratorParams struct {
	AuthorizationTables
	TenantID guid.ID
	Now      time.Time
	// AdministratorPermission and PlatformPermission are the two permission
	// codes that make a principal a durable administrator. They are passed in
	// rather than written down here because they are a policy the module root
	// owns.
	AdministratorPermission string
	PlatformPermission      string
}

// TenantGrant is one permission a subject holds in a tenant, with the role that
// carries it and the assignment that produced it.
type TenantGrant struct {
	PermissionCode string  `gorm:"column:permission_code"`
	RoleID         guid.ID `gorm:"column:role_id"`
	SourceType     string  `gorm:"column:source_type"`
	SourceID       guid.ID `gorm:"column:source_id"`
	DataScope      string  `gorm:"column:data_scope"`
	ConditionJSON  string  `gorm:"column:condition_json"`
}

// ScopedGrant is one entity-scoped role binding, either on the entity itself
// (Depth zero) or inherited from an ancestor of it.
type ScopedGrant struct {
	PermissionCode string  `gorm:"column:permission_code"`
	RoleID         guid.ID `gorm:"column:role_id"`
	Effect         string  `gorm:"column:effect"`
	ScopeID        guid.ID `gorm:"column:scope_id"`
	EntityID       guid.ID `gorm:"column:entity_id"`
	Depth          int     `gorm:"column:depth"`
	ConditionJSON  string  `gorm:"column:condition_json"`
}

// RelationshipWalkSummary is what a relationship walk measures: how deep it
// went, and whether it reached the node it was told to look for.
type RelationshipWalkSummary struct {
	MaxDepth      int `gorm:"column:max_depth"`
	ReachesSource int `gorm:"column:reaches_source"`
}

// LoadTenantGrants reads the tenant-level grants of one subject.
func LoadTenantGrants(ctx context.Context, db *gorm.DB, params TenantGrantParams) ([]TenantGrant, error) {
	statement, args := tenantGrantStatement(params)
	grants := make([]TenantGrant, 0)
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&grants).Error; err != nil {
		return nil, fmt.Errorf("evaluate tenant permissions: %w", err)
	}
	return grants, nil
}

// LoadScopedGrants reads the entity-scoped and inherited grants of one subject.
func LoadScopedGrants(ctx context.Context, db *gorm.DB, params ScopedGrantParams) ([]ScopedGrant, error) {
	statement, args := scopedGrantStatement(params)
	grants := make([]ScopedGrant, 0)
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&grants).Error; err != nil {
		return nil, fmt.Errorf("evaluate scoped entity permissions: %w", err)
	}
	return grants, nil
}

// FieldPolicyRoles reads the role IDs a subject holds, including the roles that
// carry no permission at all, because a field policy binds to a role rather
// than to a permission.
func FieldPolicyRoles(ctx context.Context, db *gorm.DB, params EffectiveRolesParams) ([]guid.ID, error) {
	statement, args := fieldPolicyRolesStatement(params)
	roles := []guid.ID{}
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&roles).Error; err != nil {
		return nil, fmt.Errorf("load field policy roles: %w", err)
	}
	return roles, nil
}

// DirectoryAncestorRoleIDs reads the role IDs bound to an entity or to any of
// its ancestors.
func DirectoryAncestorRoleIDs(ctx context.Context, db *gorm.DB, params DirectoryAncestorParams) ([]guid.ID, error) {
	statement, args := directoryAncestorRoleIDsStatement(params)
	roles := []guid.ID{}
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// RelationshipWalkDownstream walks from the seed node towards the resources it
// reaches, and reports how deep it went and whether it reached Against.
func RelationshipWalkDownstream(ctx context.Context, db *gorm.DB, params RelationshipWalkParams) (RelationshipWalkSummary, error) {
	statement, args := relationshipWalkDownstreamStatement(params)
	summary := RelationshipWalkSummary{}
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&summary).Error; err != nil {
		return RelationshipWalkSummary{}, fmt.Errorf("walk IAM relationship descendants: %w", err)
	}
	return summary, nil
}

// RelationshipWalkUpstream walks from the seed node towards the subjects that
// reach it and reports how deep it went.
func RelationshipWalkUpstream(ctx context.Context, db *gorm.DB, params RelationshipWalkParams) (RelationshipWalkSummary, error) {
	statement, args := relationshipWalkUpstreamStatement(params)
	summary := RelationshipWalkSummary{}
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&summary).Error; err != nil {
		return RelationshipWalkSummary{}, fmt.Errorf("walk IAM relationship ancestors: %w", err)
	}
	return summary, nil
}

// CountDurableAdministrators counts the active principals who administer a
// tenant through a role, a static group.
//
// It is one statement on purpose. The administrator guard measures before and
// after an operation inside the caller's transaction, and two measurements taken
// by two statements could fall on either side of another writer's commit: the
// guard would then see an administrator that the first measurement never saw,
// or miss one the second never saw.
func CountDurableAdministrators(ctx context.Context, db *gorm.DB, params DurableAdministratorParams) (int64, error) {
	statement, args := durableAdministratorCountStatement(params)
	var count int64
	if err := db.WithContext(ctx).Raw(statement, args...).Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("count durable tenant administrators: %w", err)
	}
	return count, nil
}

// effectiveRolesCTE returns the effective_roles CTE and its arguments. The CTE
// is written as a UNION of the four ways a role reaches a principal, so a
// duplicate assignment cannot grant a permission twice.
func effectiveRolesCTE(params EffectiveRolesParams) (string, []any) {
	tables := params.Tables
	directory := params.Directory
	source := func(table gormx.TableName) string {
		if params.CurrentRead {
			return CurrentTenantSource(table, params.TenantID)
		}
		return table.String()
	}
	return `
WITH effective_roles(role_id, source_type, source_id) AS (
    SELECT m.role_id, 'role_member', m.principal_id
    FROM ` + source(tables.RoleMembers) + ` m
    JOIN ` + source(tables.TenantMembers) + ` tm
      ON tm.tenant_id = m.tenant_id AND tm.principal_id = m.principal_id AND tm.status = ?
    WHERE m.tenant_id = ? AND m.principal_id = ?
      AND (m.expires_at IS NULL OR m.expires_at > ?)
    UNION
    SELECT grants.role_id, 'temporary_role', grants.id
    FROM ` + source(tables.TemporaryRoleGrants) + ` grants
    JOIN ` + source(tables.TenantMembers) + ` tm
      ON tm.tenant_id = grants.tenant_id AND tm.principal_id = grants.principal_id AND tm.status = ?
    WHERE grants.tenant_id = ? AND grants.principal_id = ?
      AND grants.starts_at <= ? AND grants.ends_at > ?
    UNION
    SELECT b.role_id, 'group', b.group_id
    FROM ` + source(directory.GroupRoleBindings) + ` b
    JOIN ` + source(directory.Groups) + ` g
      ON g.tenant_id = b.tenant_id AND g.id = b.group_id AND g.status = 'active' AND g.group_type = 'static'
    JOIN ` + source(directory.GroupMembers) + ` gm
      ON gm.tenant_id = b.tenant_id AND gm.group_id = b.group_id
    JOIN ` + source(tables.TenantMembers) + ` tm
      ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND gm.principal_id = ?
      AND (gm.starts_at IS NULL OR gm.starts_at <= ?)
      AND (gm.ends_at IS NULL OR gm.ends_at > ?)
)`, []any{
			params.MemberStatus, params.TenantID, params.Subject, params.Now,
			params.MemberStatus, params.TenantID, params.Subject, params.Now, params.Now,
			params.MemberStatus, params.TenantID, params.Subject, params.Now, params.Now,
		}
}

// tenantGrantStatement renders the statement LoadTenantGrants executes.
func tenantGrantStatement(params TenantGrantParams) (string, []any) {
	cte, args := effectiveRolesCTE(params.EffectiveRolesParams)
	tables := params.Tables
	directory := params.Directory
	source := func(table gormx.TableName) string {
		if params.CurrentRead {
			return CurrentTenantSource(table, params.TenantID)
		}
		return table.String()
	}
	return cte + `
SELECT DISTINCT rp.permission_code, er.role_id, er.source_type, er.source_id,
       COALESCE(ds.scope_type, 'all') AS data_scope, rp.condition_json
FROM ` + source(tables.RolePermissions) + ` rp
JOIN effective_roles er ON er.role_id = rp.role_id
LEFT JOIN ` + source(directory.RoleDataScopes) + ` ds
  ON ds.tenant_id = rp.tenant_id AND ds.role_id = rp.role_id
WHERE rp.tenant_id = ? AND rp.permission_code IN ?`,
		append(args, params.TenantID, params.Requested)
}

// scopedGrantStatement renders the statement LoadScopedGrants executes. The
// recursive term walks the entity tree downwards, so a binding on an ancestor
// grants on its descendants without a row per descendant.
func scopedGrantStatement(params ScopedGrantParams) (string, []any) {
	tables := params.Tables
	return `
WITH RECURSIVE expanded(permission_code, role_id, effect, scope_id, condition_json, entity_id, depth) AS (
    SELECT p.permission_code, b.role_id, b.effect, b.scope_id, p.condition_json, e.id, 0
    FROM ` + tables.RoleBindings.String() + ` b
    JOIN ` + tables.TenantMembers.String() + ` tm
      ON tm.tenant_id = b.tenant_id AND tm.principal_id = b.principal_id AND tm.status = ?
    JOIN ` + tables.RolePermissions.String() + ` p
      ON p.tenant_id = b.tenant_id AND p.role_id = b.role_id
    JOIN ` + tables.Entities.String() + ` e
      ON e.tenant_id = b.tenant_id AND e.id = b.scope_id AND e.status = 'active'
    WHERE b.tenant_id = ? AND b.principal_id = ? AND b.scope_type = 'entity'
      AND (b.expires_at IS NULL OR b.expires_at > ?)
      AND p.permission_code IN ?
    UNION ALL
    SELECT x.permission_code, x.role_id, x.effect, x.scope_id, x.condition_json, e.id, x.depth + 1
    FROM expanded x
    JOIN ` + tables.Entities.String() + ` e ON e.tenant_id = ? AND e.parent_id = x.entity_id AND e.status = 'active'
    WHERE x.depth < 15
)
SELECT DISTINCT permission_code, role_id, effect, scope_id, condition_json, entity_id, depth
FROM expanded`, []any{
			params.MemberStatus, params.TenantID, params.Subject,
			params.Now, params.Requested, params.TenantID,
		}
}

// fieldPolicyRolesStatement renders the statement FieldPolicyRoles executes.
func fieldPolicyRolesStatement(params EffectiveRolesParams) (string, []any) {
	cte, args := effectiveRolesCTE(params)
	return cte + ` SELECT DISTINCT role_id FROM effective_roles`, args
}

// directoryAncestorRoleIDsStatement renders the statement
// DirectoryAncestorRoleIDs executes. The walk stops at the root of the tenant
// and at fifteen levels, which is the same depth the entity tree itself is
// allowed to reach.
func directoryAncestorRoleIDsStatement(params DirectoryAncestorParams) (string, []any) {
	entities := params.Tables.Entities.String()
	return `WITH RECURSIVE ancestors(id, parent_id, depth) AS (
SELECT id, parent_id, 0 FROM ` + entities + ` WHERE tenant_id = ? AND id = ? AND status = 'active'
UNION ALL
SELECT e.id, e.parent_id, ancestors.depth + 1 FROM ` + entities + ` e
JOIN ancestors ON e.id = ancestors.parent_id
WHERE e.tenant_id = ? AND e.status = 'active' AND ancestors.depth < 15
)
SELECT DISTINCT b.role_id FROM ` + params.Tables.RoleBindings.String() + ` b JOIN ancestors ON b.scope_id = ancestors.id
WHERE b.tenant_id = ? AND b.principal_id = ? AND b.scope_type = 'entity' AND b.effect = 'allow'
AND (b.expires_at IS NULL OR b.expires_at > ?)`, []any{
			params.TenantID, params.EntityID, params.TenantID,
			params.TenantID, params.Subject, params.Now,
		}
}

// relationshipTraversalExpressions returns the casts the recursive relationship
// walk needs for its anchor. A recursive CTE requires the anchor and the
// recursive term to agree on type, and PostgreSQL additionally compares text
// with the collation the anchor was declared with.
func relationshipTraversalExpressions(dialect string) (textParameter, entityLiteral, integerParameter string) {
	textParameter, entityLiteral = "CAST(? AS TEXT)", "CAST('entity' AS TEXT)"
	integerParameter = "CAST(? AS BIGINT)"
	switch dialect {
	case "mysql":
		textParameter, entityLiteral = "CAST(? AS CHAR(16))", "CAST('entity' AS CHAR(16))"
		integerParameter = "CAST(? AS SIGNED)"
	case "postgres":
		textParameter = `CAST(? AS VARCHAR(16)) COLLATE "C"`
		entityLiteral = `CAST('entity' AS VARCHAR(16)) COLLATE "C"`
	}
	return textParameter, entityLiteral, integerParameter
}

// relationshipWalkDownstreamStatement renders the statement that walks from an
// entity towards the resources it reaches.
func relationshipWalkDownstreamStatement(params RelationshipWalkParams) (string, []any) {
	textParameter, entityLiteral, integerParameter := relationshipTraversalExpressions(params.Dialect)
	return `
WITH RECURSIVE walk(node_type, node_id, node_relation, depth) AS (
    SELECT ` + textParameter + ` AS node_type,
           ` + integerParameter + ` AS node_id,
           ` + textParameter + ` AS node_relation,
           0 AS depth
    UNION
    SELECT ` + entityLiteral + `, rel.resource_id, rel.relation, walk.depth + 1
    FROM ` + params.Relationships.String() + ` AS rel
    JOIN walk ON rel.subject_type = walk.node_type
             AND rel.subject_id = walk.node_id
             AND rel.subject_relation = walk.node_relation
    WHERE rel.tenant_id = ? AND walk.depth < ?
)
SELECT COALESCE(MAX(depth), 0) AS max_depth,
       COALESCE(MAX(CASE WHEN node_type = ? AND node_id = ` + integerParameter + ` AND node_relation = ? THEN 1 ELSE 0 END), 0) AS reaches_source
FROM walk`, []any{
			params.Seed.Type, params.Seed.ID, params.Seed.Relation,
			params.TenantID, params.MaxDepth,
			params.Against.Type, params.Against.ID, params.Against.Relation,
		}
}

// relationshipWalkUpstreamStatement renders the statement that walks from an
// entity towards the subjects that reach it.
func relationshipWalkUpstreamStatement(params RelationshipWalkParams) (string, []any) {
	textParameter, _, integerParameter := relationshipTraversalExpressions(params.Dialect)
	return `
WITH RECURSIVE walk(node_type, node_id, node_relation, depth) AS (
    SELECT ` + textParameter + ` AS node_type,
           ` + integerParameter + ` AS node_id,
           ` + textParameter + ` AS node_relation,
           0 AS depth
    UNION
    SELECT rel.subject_type, rel.subject_id, rel.subject_relation, walk.depth + 1
    FROM ` + params.Relationships.String() + ` AS rel
    JOIN walk ON walk.node_type = 'entity'
             AND rel.resource_id = walk.node_id
             AND rel.relation = walk.node_relation
    WHERE rel.tenant_id = ? AND walk.depth < ?
)
SELECT COALESCE(MAX(depth), 0) AS max_depth, 0 AS reaches_source
FROM walk`, []any{
			params.Seed.Type, params.Seed.ID, params.Seed.Relation,
			params.TenantID, params.MaxDepth,
		}
}

// An assignment that ends is not durable, which is why this reads IS NULL
// rather than "not yet expired": the guard asks whether the tenant would still
// have an administrator afterwards, and one whose grant lapses next week is an
// answer that expires. The group branch below has required the same of a
// time-bounded membership since it was written.
//
// durableAdministratorCountStatement renders the statement
// CountDurableAdministrators executes.
func durableAdministratorCountStatement(params DurableAdministratorParams) (string, []any) {
	tables := params.Tables
	directory := params.Directory
	return `
SELECT COUNT(DISTINCT administrators.principal_id)
FROM (
    SELECT members.principal_id
    FROM ` + tables.RoleMembers.String() + ` AS members
    JOIN ` + tables.RolePermissions.String() + ` AS permissions
      ON permissions.tenant_id = members.tenant_id AND permissions.role_id = members.role_id
    WHERE members.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND members.expires_at IS NULL
    UNION
    SELECT group_members.principal_id
    FROM ` + directory.GroupRoleBindings.String() + ` AS bindings
    JOIN ` + tables.RolePermissions.String() + ` AS permissions
      ON permissions.tenant_id = bindings.tenant_id AND permissions.role_id = bindings.role_id
    JOIN ` + directory.Groups.String() + ` AS grp
      ON grp.tenant_id = bindings.tenant_id AND grp.id = bindings.group_id
     AND grp.status = 'active' AND grp.group_type = 'static'
    JOIN ` + directory.GroupMembers.String() + ` AS group_members
      ON group_members.tenant_id = bindings.tenant_id AND group_members.group_id = bindings.group_id
    WHERE bindings.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND (group_members.starts_at IS NULL OR group_members.starts_at <= ?)
      AND group_members.ends_at IS NULL
) AS administrators
JOIN ` + params.Principal.String() + ` AS principals
  ON principals.id = administrators.principal_id AND principals.status = 'active'
JOIN ` + tables.TenantMembers.String() + ` AS tenant_members
  ON tenant_members.tenant_id = ?
 AND tenant_members.principal_id = administrators.principal_id
 AND tenant_members.status = 'active'`, []any{
			params.TenantID, params.AdministratorPermission, params.PlatformPermission,
			params.TenantID, params.AdministratorPermission, params.PlatformPermission, params.Now,
			params.TenantID,
		}
}
