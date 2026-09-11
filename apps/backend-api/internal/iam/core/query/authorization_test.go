package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The statements pinned below are the authorization engine's queries. Their
// text is pinned verbatim because it is not only a query plan: the CTE shapes,
// the UNION arms and the parameter order are the definition of who holds a
// role, and a change to any of them changes authorization semantics. Such a
// change must be deliberate, made in one place, with this file updated in the
// same commit.
//
// The second half of each case pins the argument list. A placeholder whose
// argument moved is as wrong as a placeholder that disappeared, and the SQL
// text alone would not show it.
// authorizationTablesFor assembles one statement's names the way the wired
// module does: core's own names from its deployment prefix, and the names of
// the tables it joins from the sets their owning modules publish.
func authorizationTablesFor(prefix string) AuthorizationTables {
	directory, err := BindDirectoryTables(publishedSet(prefix, directoryLogicalNames...))
	if err != nil {
		panic(err)
	}
	principal, err := BindPrincipalTable(publishedSet(prefix, "iam_principals"))
	if err != nil {
		panic(err)
	}
	return BindAuthorizationTables(prefix, directory, principal)
}

func TestAuthorizationStatementsPinSQLTextAndParameterOrder(t *testing.T) {
	t.Parallel()
	tables := authorizationTablesFor("sl_")
	requested := []string{"iam_read", "iam_write"}
	now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)

	roles := EffectiveRolesParams{
		AuthorizationTables: tables,
		TenantID:            101,
		Subject:             202,
		Now:                 now,
		MemberStatus:        "active",
	}
	seed := RelationshipWalkNode{Type: "entity", ID: 303, Relation: "parent"}
	against := RelationshipWalkNode{Type: "entity", ID: 404, Relation: "child"}
	walk := RelationshipWalkParams{
		Relationships: tables.Tables.Relationships,
		Dialect:       "sqlite",
		TenantID:      101,
		MaxDepth:      16,
		Seed:          seed,
		Against:       against,
	}
	roleArguments := []any{
		"active", guid.ID(101), guid.ID(202),
		"active", guid.ID(101), guid.ID(202), now, now,
		"active", guid.ID(101), guid.ID(202), now, now,
		"active", guid.ID(101), guid.ID(202), now, now,
	}

	tests := []struct {
		name   string
		render func() (string, []any)
		golden string
		args   []any
	}{
		{
			name: "tenant_grants",
			render: func() (string, []any) {
				return tenantGrantStatement(TenantGrantParams{EffectiveRolesParams: roles, Requested: requested})
			},
			golden: goldenTenantGrants,
			args:   append(append([]any{}, roleArguments...), guid.ID(101), []string{"iam_read", "iam_write"}),
		},
		{
			name: "scoped_grants",
			render: func() (string, []any) {
				return scopedGrantStatement(ScopedGrantParams{
					AuthorizationTables: tables, TenantID: 101, Subject: 202,
					Requested: requested, Now: now, MemberStatus: "active",
				})
			},
			golden: goldenScopedGrants,
			args:   []any{"active", guid.ID(101), guid.ID(202), now, []string{"iam_read", "iam_write"}, guid.ID(101)},
		},
		{
			name: "field_policy_roles",
			render: func() (string, []any) {
				return fieldPolicyRolesStatement(roles)
			},
			golden: goldenFieldPolicyRoles,
			args:   append([]any{}, roleArguments...),
		},
		{
			name: "directory_ancestors",
			render: func() (string, []any) {
				return directoryAncestorRoleIDsStatement(DirectoryAncestorParams{
					Tables: tables.Tables, TenantID: 101, EntityID: 303, Subject: 202, Now: now,
				})
			},
			golden: goldenDirectoryAncestors,
			args:   []any{guid.ID(101), guid.ID(303), guid.ID(101), guid.ID(101), guid.ID(202), now},
		},
		{
			name: "durable_administrators",
			render: func() (string, []any) {
				return durableAdministratorCountStatement(DurableAdministratorParams{
					AuthorizationTables:     tables,
					TenantID:                101,
					Now:                     now,
					AdministratorPermission: "tenant_administer",
					PlatformPermission:      "platform_administer",
				})
			},
			golden: goldenDurableAdministrators,
			args: []any{
				guid.ID(101), "tenant_administer", "platform_administer",
				guid.ID(101), "tenant_administer", "platform_administer", now,
				guid.ID(101), "tenant_administer", "platform_administer", now,
				guid.ID(101),
			},
		},
		{
			name: "relationship_downstream",
			render: func() (string, []any) {
				return relationshipWalkDownstreamStatement(walk)
			},
			golden: goldenRelationshipDownstream,
			args: []any{
				"entity", guid.ID(303), "parent", guid.ID(101), 16,
				"entity", guid.ID(404), "child",
			},
		},
		{
			name: "relationship_upstream",
			render: func() (string, []any) {
				return relationshipWalkUpstreamStatement(walk)
			},
			golden: goldenRelationshipUpstream,
			args:   []any{"entity", guid.ID(303), "parent", guid.ID(101), 16},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement, args := test.render()
			assert.Equal(t, test.golden, statement)
			assert.Equal(t, test.args, args)
		})
	}
}

// TestRelationshipTraversalExpressionsMatchDatabaseDialects keeps the recursive
// walk portable. A recursive CTE compares the anchor's literal with the table's
// column, so a text type that differs from PostgreSQL's needs an explicit cast,
// and PostgreSQL additionally compares with the collation its index was built
// with.
func TestRelationshipTraversalExpressionsMatchDatabaseDialects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		dialect                                        string
		textParameter, entityLiteral, integerParameter string
	}{
		{"sqlite", "CAST(? AS TEXT)", "CAST('entity' AS TEXT)", "CAST(? AS BIGINT)"},
		{"mysql", "CAST(? AS CHAR(16))", "CAST('entity' AS CHAR(16))", "CAST(? AS SIGNED)"},
		{"postgres", `CAST(? AS VARCHAR(16)) COLLATE "C"`, `CAST('entity' AS VARCHAR(16)) COLLATE "C"`, "CAST(? AS BIGINT)"},
	}
	for _, test := range tests {
		t.Run(test.dialect, func(t *testing.T) {
			textParameter, entityLiteral, integerParameter := relationshipTraversalExpressions(test.dialect)
			assert.Equal(t, test.textParameter, textParameter)
			assert.Equal(t, test.entityLiteral, entityLiteral)
			assert.Equal(t, test.integerParameter, integerParameter)
		})
	}
}

const goldenTenantGrants = `
WITH effective_roles(role_id, source_type, source_id) AS (
    SELECT m.role_id, 'role_member', m.principal_id
    FROM sl_iam_role_members m
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = m.tenant_id AND tm.principal_id = m.principal_id AND tm.status = ?
    WHERE m.tenant_id = ? AND m.principal_id = ?
    UNION
    SELECT grants.role_id, 'temporary_role', grants.id
    FROM sl_iam_temporary_role_grants grants
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = grants.tenant_id AND tm.principal_id = grants.principal_id AND tm.status = ?
    WHERE grants.tenant_id = ? AND grants.principal_id = ?
      AND grants.starts_at <= ? AND grants.ends_at > ?
    UNION
    SELECT b.role_id, 'group', b.group_id
    FROM sl_iam_group_role_bindings b
    JOIN sl_iam_groups g
      ON g.tenant_id = b.tenant_id AND g.id = b.group_id AND g.status = 'active' AND g.group_type = 'static'
    JOIN sl_iam_group_members gm
      ON gm.tenant_id = b.tenant_id AND gm.group_id = b.group_id
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND gm.principal_id = ?
      AND (gm.starts_at IS NULL OR gm.starts_at <= ?)
      AND (gm.ends_at IS NULL OR gm.ends_at > ?)
    UNION
    SELECT b.role_id, 'position', b.position_id
    FROM sl_iam_position_role_bindings b
    JOIN sl_iam_positions pos
      ON pos.tenant_id = b.tenant_id AND pos.id = b.position_id AND pos.status = 'active'
    JOIN sl_iam_position_members pm
      ON pm.tenant_id = b.tenant_id AND pm.position_id = b.position_id
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND pm.principal_id = ?
      AND (pm.starts_at IS NULL OR pm.starts_at <= ?)
      AND (pm.ends_at IS NULL OR pm.ends_at > ?)
)
SELECT DISTINCT rp.permission_code, er.role_id, er.source_type, er.source_id,
       COALESCE(ds.scope_type, 'all') AS data_scope, rp.condition_json
FROM sl_iam_role_permissions rp
JOIN effective_roles er ON er.role_id = rp.role_id
LEFT JOIN sl_iam_role_data_scopes ds
  ON ds.tenant_id = rp.tenant_id AND ds.role_id = rp.role_id
WHERE rp.tenant_id = ? AND rp.permission_code IN ?`

const goldenScopedGrants = `
WITH RECURSIVE expanded(permission_code, role_id, effect, scope_id, condition_json, entity_id, depth) AS (
    SELECT p.permission_code, b.role_id, b.effect, b.scope_id, p.condition_json, e.id, 0
    FROM sl_iam_role_bindings b
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = b.tenant_id AND tm.principal_id = b.principal_id AND tm.status = ?
    JOIN sl_iam_role_permissions p
      ON p.tenant_id = b.tenant_id AND p.role_id = b.role_id
    JOIN sl_iam_entities e
      ON e.tenant_id = b.tenant_id AND e.id = b.scope_id AND e.status = 'active'
    WHERE b.tenant_id = ? AND b.principal_id = ? AND b.scope_type = 'entity'
      AND (b.expires_at IS NULL OR b.expires_at > ?)
      AND p.permission_code IN ?
    UNION ALL
    SELECT x.permission_code, x.role_id, x.effect, x.scope_id, x.condition_json, e.id, x.depth + 1
    FROM expanded x
    JOIN sl_iam_entities e ON e.tenant_id = ? AND e.parent_id = x.entity_id AND e.status = 'active'
    WHERE x.depth < 15
)
SELECT DISTINCT permission_code, role_id, effect, scope_id, condition_json, entity_id, depth
FROM expanded`

const goldenFieldPolicyRoles = `
WITH effective_roles(role_id, source_type, source_id) AS (
    SELECT m.role_id, 'role_member', m.principal_id
    FROM sl_iam_role_members m
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = m.tenant_id AND tm.principal_id = m.principal_id AND tm.status = ?
    WHERE m.tenant_id = ? AND m.principal_id = ?
    UNION
    SELECT grants.role_id, 'temporary_role', grants.id
    FROM sl_iam_temporary_role_grants grants
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = grants.tenant_id AND tm.principal_id = grants.principal_id AND tm.status = ?
    WHERE grants.tenant_id = ? AND grants.principal_id = ?
      AND grants.starts_at <= ? AND grants.ends_at > ?
    UNION
    SELECT b.role_id, 'group', b.group_id
    FROM sl_iam_group_role_bindings b
    JOIN sl_iam_groups g
      ON g.tenant_id = b.tenant_id AND g.id = b.group_id AND g.status = 'active' AND g.group_type = 'static'
    JOIN sl_iam_group_members gm
      ON gm.tenant_id = b.tenant_id AND gm.group_id = b.group_id
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND gm.principal_id = ?
      AND (gm.starts_at IS NULL OR gm.starts_at <= ?)
      AND (gm.ends_at IS NULL OR gm.ends_at > ?)
    UNION
    SELECT b.role_id, 'position', b.position_id
    FROM sl_iam_position_role_bindings b
    JOIN sl_iam_positions pos
      ON pos.tenant_id = b.tenant_id AND pos.id = b.position_id AND pos.status = 'active'
    JOIN sl_iam_position_members pm
      ON pm.tenant_id = b.tenant_id AND pm.position_id = b.position_id
    JOIN sl_iam_tenant_members tm
      ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id AND tm.status = ?
    WHERE b.tenant_id = ? AND pm.principal_id = ?
      AND (pm.starts_at IS NULL OR pm.starts_at <= ?)
      AND (pm.ends_at IS NULL OR pm.ends_at > ?)
) SELECT DISTINCT role_id FROM effective_roles`

const goldenDirectoryAncestors = `WITH RECURSIVE ancestors(id, parent_id, depth) AS (
SELECT id, parent_id, 0 FROM sl_iam_entities WHERE tenant_id = ? AND id = ? AND status = 'active'
UNION ALL
SELECT e.id, e.parent_id, ancestors.depth + 1 FROM sl_iam_entities e
JOIN ancestors ON e.id = ancestors.parent_id
WHERE e.tenant_id = ? AND e.status = 'active' AND ancestors.depth < 15
)
SELECT DISTINCT b.role_id FROM sl_iam_role_bindings b JOIN ancestors ON b.scope_id = ancestors.id
WHERE b.tenant_id = ? AND b.principal_id = ? AND b.scope_type = 'entity' AND b.effect = 'allow'
AND (b.expires_at IS NULL OR b.expires_at > ?)`

const goldenDurableAdministrators = `
SELECT COUNT(DISTINCT administrators.principal_id)
FROM (
    SELECT members.principal_id
    FROM sl_iam_role_members AS members
    JOIN sl_iam_role_permissions AS permissions
      ON permissions.tenant_id = members.tenant_id AND permissions.role_id = members.role_id
    WHERE members.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
    UNION
    SELECT group_members.principal_id
    FROM sl_iam_group_role_bindings AS bindings
    JOIN sl_iam_role_permissions AS permissions
      ON permissions.tenant_id = bindings.tenant_id AND permissions.role_id = bindings.role_id
    JOIN sl_iam_groups AS grp
      ON grp.tenant_id = bindings.tenant_id AND grp.id = bindings.group_id
     AND grp.status = 'active' AND grp.group_type = 'static'
    JOIN sl_iam_group_members AS group_members
      ON group_members.tenant_id = bindings.tenant_id AND group_members.group_id = bindings.group_id
    WHERE bindings.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND (group_members.starts_at IS NULL OR group_members.starts_at <= ?)
      AND group_members.ends_at IS NULL
    UNION
    SELECT position_members.principal_id
    FROM sl_iam_position_role_bindings AS bindings
    JOIN sl_iam_role_permissions AS permissions
      ON permissions.tenant_id = bindings.tenant_id AND permissions.role_id = bindings.role_id
    JOIN sl_iam_positions AS positions
      ON positions.tenant_id = bindings.tenant_id AND positions.id = bindings.position_id
     AND positions.status = 'active'
    JOIN sl_iam_position_members AS position_members
      ON position_members.tenant_id = bindings.tenant_id AND position_members.position_id = bindings.position_id
    WHERE bindings.tenant_id = ? AND permissions.permission_code IN (?, ?) AND permissions.condition_json = ''
      AND (position_members.starts_at IS NULL OR position_members.starts_at <= ?)
      AND position_members.ends_at IS NULL
) AS administrators
JOIN sl_iam_principals AS principals
  ON principals.id = administrators.principal_id AND principals.status = 'active'
JOIN sl_iam_tenant_members AS tenant_members
  ON tenant_members.tenant_id = ?
 AND tenant_members.principal_id = administrators.principal_id
 AND tenant_members.status = 'active'`

const goldenRelationshipDownstream = `
WITH RECURSIVE walk(node_type, node_id, node_relation, depth) AS (
    SELECT CAST(? AS TEXT) AS node_type,
           CAST(? AS BIGINT) AS node_id,
           CAST(? AS TEXT) AS node_relation,
           0 AS depth
    UNION
    SELECT CAST('entity' AS TEXT), rel.resource_id, rel.relation, walk.depth + 1
    FROM sl_iam_relationships AS rel
    JOIN walk ON rel.subject_type = walk.node_type
             AND rel.subject_id = walk.node_id
             AND rel.subject_relation = walk.node_relation
    WHERE rel.tenant_id = ? AND walk.depth < ?
)
SELECT COALESCE(MAX(depth), 0) AS max_depth,
       COALESCE(MAX(CASE WHEN node_type = ? AND node_id = CAST(? AS BIGINT) AND node_relation = ? THEN 1 ELSE 0 END), 0) AS reaches_source
FROM walk`

const goldenRelationshipUpstream = `
WITH RECURSIVE walk(node_type, node_id, node_relation, depth) AS (
    SELECT CAST(? AS TEXT) AS node_type,
           CAST(? AS BIGINT) AS node_id,
           CAST(? AS TEXT) AS node_relation,
           0 AS depth
    UNION
    SELECT rel.subject_type, rel.subject_id, rel.subject_relation, walk.depth + 1
    FROM sl_iam_relationships AS rel
    JOIN walk ON walk.node_type = 'entity'
             AND rel.resource_id = walk.node_id
             AND rel.relation = walk.node_relation
    WHERE rel.tenant_id = ? AND walk.depth < ?
)
SELECT COALESCE(MAX(depth), 0) AS max_depth, 0 AS reaches_source
FROM walk`

// foreignTableDDL creates the tables the authorization statements read but this
// module does not own. Only the columns the statements name are declared, so a
// statement that starts naming a new column fails the execution test below
// rather than silently returning nothing.
var foreignTableDDL = []string{
	`CREATE TABLE iam_principals (id INTEGER PRIMARY KEY, status TEXT NOT NULL)`,
	`CREATE TABLE iam_groups (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, status TEXT NOT NULL, group_type TEXT NOT NULL)`,
	`CREATE TABLE iam_group_members (tenant_id INTEGER NOT NULL, group_id INTEGER NOT NULL, principal_id INTEGER NOT NULL, starts_at TIMESTAMP, ends_at TIMESTAMP)`,
	`CREATE TABLE iam_group_role_bindings (tenant_id INTEGER NOT NULL, group_id INTEGER NOT NULL, role_id INTEGER NOT NULL)`,
	`CREATE TABLE iam_positions (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, status TEXT NOT NULL)`,
	`CREATE TABLE iam_position_members (tenant_id INTEGER NOT NULL, position_id INTEGER NOT NULL, principal_id INTEGER NOT NULL, starts_at TIMESTAMP, ends_at TIMESTAMP)`,
	`CREATE TABLE iam_position_role_bindings (tenant_id INTEGER NOT NULL, position_id INTEGER NOT NULL, role_id INTEGER NOT NULL)`,
	`CREATE TABLE iam_departments (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, status TEXT NOT NULL)`,
	`CREATE TABLE iam_department_closure (tenant_id INTEGER NOT NULL, ancestor_id INTEGER NOT NULL, descendant_id INTEGER NOT NULL)`,
	`CREATE TABLE iam_member_departments (tenant_id INTEGER NOT NULL, principal_id INTEGER NOT NULL, department_id INTEGER NOT NULL)`,
	`CREATE TABLE iam_role_data_scopes (tenant_id INTEGER NOT NULL, role_id INTEGER NOT NULL, scope_type TEXT NOT NULL)`,
	`CREATE TABLE iam_role_scope_departments (tenant_id INTEGER NOT NULL, role_id INTEGER NOT NULL, department_id INTEGER NOT NULL)`,
}

// sqliteDatabase opens a private in-memory database whose schema lives as long
// as its single connection. Sharing the cache with one connection keeps the
// tables visible to every query the pool hands out without leaving a file
// behind for the test to clean up.
func sqliteDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	return db
}

// authorizationDatabase opens a database carrying every table the authorization
// statements name. The core tables come from their models; the ones this module
// only joins are declared above.
func authorizationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db := sqliteDatabase(t)
	require.NoError(t, db.AutoMigrate(
		&model.Entity{}, &model.Relationship{}, &model.RoleBinding{}, &model.RoleMember{},
		&model.RolePermission{}, &model.TemporaryRoleGrant{}, &model.TenantMember{},
	))
	for _, statement := range foreignTableDDL {
		require.NoError(t, db.Exec(statement).Error, statement)
	}
	return db
}

func TestAuthorizationStatementsExecuteAgainstEveryTableTheyName(t *testing.T) {
	t.Parallel()
	db := authorizationDatabase(t)
	tables := authorizationTablesFor("")
	ctx := context.Background()
	now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	roles := EffectiveRolesParams{
		AuthorizationTables: tables, TenantID: 101, Subject: 202, Now: now, MemberStatus: "active",
	}

	grants, err := LoadTenantGrants(ctx, db, TenantGrantParams{
		EffectiveRolesParams: roles, Requested: []string{"iam_read"},
	})
	require.NoError(t, err)
	assert.Empty(t, grants)

	scoped, err := LoadScopedGrants(ctx, db, ScopedGrantParams{
		AuthorizationTables: tables, TenantID: 101, Subject: 202,
		Requested: []string{"iam_read"}, Now: now, MemberStatus: "active",
	})
	require.NoError(t, err)
	assert.Empty(t, scoped)

	roleIDs, err := FieldPolicyRoles(ctx, db, roles)
	require.NoError(t, err)
	assert.Empty(t, roleIDs)

	inherited, err := DirectoryAncestorRoleIDs(ctx, db, DirectoryAncestorParams{
		Tables: tables.Tables, TenantID: 101, EntityID: 303, Subject: 202, Now: now,
	})
	require.NoError(t, err)
	assert.Empty(t, inherited)

	durable, err := CountDurableAdministrators(ctx, db, DurableAdministratorParams{
		AuthorizationTables: tables, TenantID: 101, Now: now,
		AdministratorPermission: "tenant_administer", PlatformPermission: "platform_administer",
	})
	require.NoError(t, err)
	assert.Zero(t, durable)

	walk := RelationshipWalkParams{
		Relationships: tables.Tables.Relationships, Dialect: "sqlite", TenantID: 101, MaxDepth: 16,
		Seed:    RelationshipWalkNode{Type: "entity", ID: 303, Relation: "parent"},
		Against: RelationshipWalkNode{Type: "entity", ID: 404, Relation: "child"},
	}
	downstream, err := RelationshipWalkDownstream(ctx, db, walk)
	require.NoError(t, err)
	assert.Equal(t, RelationshipWalkSummary{}, downstream)

	walk.Against = RelationshipWalkNode{}
	upstream, err := RelationshipWalkUpstream(ctx, db, walk)
	require.NoError(t, err)
	assert.Equal(t, RelationshipWalkSummary{}, upstream)
}

// TestAuthorizationStatementsReportFailureToRead keeps the error path of every
// executor honest: a table that cannot be read must surface as an error rather
// than as an empty result, which a caller would read as "no permissions".
func TestAuthorizationStatementsReportFailureToRead(t *testing.T) {
	t.Parallel()
	db := sqliteDatabase(t)
	tables := authorizationTablesFor("")
	ctx := context.Background()
	var err error
	now := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	roles := EffectiveRolesParams{
		AuthorizationTables: tables, TenantID: 101, Subject: 202, Now: now, MemberStatus: "active",
	}
	walk := RelationshipWalkParams{
		Relationships: tables.Tables.Relationships, Dialect: "sqlite", TenantID: 101, MaxDepth: 16,
		Seed: RelationshipWalkNode{Type: "entity", ID: 303, Relation: "parent"},
	}

	_, err = LoadTenantGrants(ctx, db, TenantGrantParams{
		EffectiveRolesParams: roles, Requested: []string{"iam_read"},
	})
	assert.ErrorContains(t, err, "evaluate tenant permissions")

	_, err = LoadScopedGrants(ctx, db, ScopedGrantParams{
		AuthorizationTables: tables, TenantID: 101, Subject: 202,
		Requested: []string{"iam_read"}, Now: now, MemberStatus: "active",
	})
	assert.ErrorContains(t, err, "evaluate scoped entity permissions")

	_, err = FieldPolicyRoles(ctx, db, roles)
	assert.ErrorContains(t, err, "load field policy roles")

	_, err = DirectoryAncestorRoleIDs(ctx, db, DirectoryAncestorParams{
		Tables: tables.Tables, TenantID: 101, EntityID: 303, Subject: 202, Now: now,
	})
	assert.Error(t, err)

	_, err = CountDurableAdministrators(ctx, db, DurableAdministratorParams{
		AuthorizationTables: tables, TenantID: 101, Now: now,
		AdministratorPermission: "tenant_administer", PlatformPermission: "platform_administer",
	})
	assert.ErrorContains(t, err, "count durable tenant administrators")

	_, err = RelationshipWalkDownstream(ctx, db, walk)
	assert.ErrorContains(t, err, "walk IAM relationship descendants")

	_, err = RelationshipWalkUpstream(ctx, db, walk)
	assert.ErrorContains(t, err, "walk IAM relationship ancestors")
}
