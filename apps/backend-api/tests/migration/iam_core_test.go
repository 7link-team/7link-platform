package migration_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const coreTestTime = "2026-09-02 00:00:00"

type relationshipTestIDSource struct{ next atomic.Int64 }

func (source *relationshipTestIDSource) Next() (int64, error) { return source.next.Add(1), nil }

func TestIAMCoreMigrationsAcrossEngines(t *testing.T) {
	engines := []databaseEngine{
		{name: "sqlite", dialect: "sqlite", open: testdb.OpenSQLite},
		{name: "mysql", dialect: "mysql", open: testdb.OpenMySQL},
		{name: "postgres", dialect: "postgres", open: testdb.OpenPostgres},
	}
	for _, engine := range engines {
		t.Run(engine.name, func(t *testing.T) {
			for _, prefix := range []string{"", deploymentPrefix} {
				t.Run(prefixTestName(prefix), func(t *testing.T) {
					db := engine.open(t)
					// Rolling core below principal-erasure (v13) runs a guard that probes
					// identity's tombstone trigger, so identity's lifecycle milestone must be
					// present -- the dependency core.MigrationMilestones already declares.
					// MigrateDownTo is the low-level primitive and does not enforce that
					// graph; only app.ExecuteModuleMigrations does, so compose it here.
					identitySource := (&identity.Module{}).MigrationSource()
					identitySource.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, identitySource, engine.dialect))

					source := (&iamcore.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))

					verifyCoreFieldPolicyContracts(t, db, source, prefix, engine.dialect)
					verifyCoreRoleContracts(t, db, prefix)
					verifyCoreMenuContracts(t, db, prefix)
					verifyCoreEntityContracts(t, db, prefix, engine.dialect)
					verifyCoreBindingAndGrantContracts(t, db, prefix)
					verifyCoreRelationshipTraversalIndex(t, db, prefix, engine.dialect)
					verifyCoreRelationshipTraversalMigrationLifecycle(t, db, source, prefix, engine.dialect)
				})
			}
		})
	}
}

func verifyCoreFieldPolicyContracts(t *testing.T, db *sql.DB, source goosex.Source, prefix, dialect string) {
	t.Helper()
	table := prefix + "iam_field_policies"
	insert := func(tenant, version int64, field string) error {
		_, err := db.Exec(fmt.Sprintf("INSERT INTO %s (tenant_id, resource, field_name, policy_json, version, updated_at) VALUES (%d, 'todo', '%s', '{}', %d, '%s')", table, tenant, field, version, coreTestTime))
		return err
	}
	require.NoError(t, insert(1, 1, "title"))
	require.NoError(t, insert(2, 1, "title"), "same field in another tenant is independent")
	assert.Error(t, insert(1, 2, "title"), "policy key is tenant/resource/field")
	assert.Error(t, insert(0, 1, "description"))
	assert.Error(t, insert(-1, 1, "description"))
	assert.Error(t, insert(1, 0, "description"))
	assert.Error(t, insert(1, -1, "description"))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 11))
	assertTableExists(t, db, dialect, table, false)
	require.NoError(t, goosex.Migrate(t.Context(), db, source, dialect))
	assertTableExists(t, db, dialect, table, true)
	assert.Zero(t, tableRowCount(t, db, table))
}

func TestIAMCoreServiceBehaviorOnPostgres(t *testing.T) {
	db, dsn := testdb.OpenPostgresWithDSN(t)
	for _, prefix := range []string{"", deploymentPrefix} {
		t.Run(prefixTestName(prefix), func(t *testing.T) {
			for _, provider := range []interface{ MigrationSource() goosex.Source }{
				&identity.Module{}, &iamcore.Module{}, &organization.Module{},
			} {
				source := provider.MigrationSource()
				source.Prefix = prefix
				require.NoError(t, goosex.Migrate(t.Context(), db, source, "postgres"))
			}
			verifyCoreServiceBehavior(t, "postgres", dsn, prefix)
		})
	}
}

func verifyCoreServiceBehavior(t *testing.T, datasourceType, dsn, prefix string) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: datasourceType, DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, router.Close()) }()
	runtime := appenv.NewRuntime(
		appenv.NewIdentity("iam-core-service-test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	ids := &relationshipTestIDSource{}
	ids.next.Store(91_000)
	repository := iamcore.NewRepository(runtime, ids)
	authorizer := iamcore.NewAuthorizer(runtime)
	require.NoError(t, authorizer.BindDirectoryAuthorizationSources(
		organization.NewDirectoryAuthorizationSourceStore(runtime),
	))
	service := iamcore.NewService(
		authz.DefaultRegistry(), repository, authorizer,
		func(context.Context, *gorm.DB, iamaudit.DomainEvent) error { return nil },
	)

	now := time.Date(2026, 9, 2, 12, 0, 0, 123_000_000, time.UTC)
	tenantID, principalID := guid.ID(90_001), guid.ID(90_002)
	db := router.Write()
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (?, ?, ?, 'active', 1, ?, ?)",
		tenantID, "relationship-traversal-tenant", "Relationship Traversal Tenant", now, now,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?)",
		principalID, "relationship-traversal-principal", "Relationship Traversal Principal", now, now,
	).Error)
	_, err = service.PutTenantMember(
		t.Context(), tenantID, principalID, "Relationship Member", "relationship@example.test", 0, iamcore.MemberActive,
	)
	require.NoError(t, err)
	verifyMemberAuthorizationSourceExpiries(t, db, service, repository, prefix, tenantID, principalID, now)
	first, err := service.CreateEntity(t.Context(), iamcore.Entity{
		TenantID: tenantID, Type: "store", Name: "First", Status: iamcore.EntityActive,
	})
	require.NoError(t, err)
	second, err := service.CreateEntity(t.Context(), iamcore.Entity{
		TenantID: tenantID, Type: "store", Name: "Second", Status: iamcore.EntityActive,
	})
	require.NoError(t, err)

	_, changed, err := service.PutRelationship(t.Context(), iamcore.Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "owner", ResourceType: "store", ResourceID: first.ID,
	})
	require.NoError(t, err)
	assert.True(t, changed)
	_, changed, err = service.PutRelationship(t.Context(), iamcore.Relationship{
		TenantID: tenantID, SubjectType: "entity", SubjectID: first.ID, SubjectRelation: "owner",
		Relation: "viewer", ResourceType: "store", ResourceID: second.ID,
	})
	require.NoError(t, err)
	assert.True(t, changed)
	var relationshipsBefore int64
	require.NoError(t, db.Table(prefix+"iam_relationships").Count(&relationshipsBefore).Error)
	_, _, err = service.PutRelationship(t.Context(), iamcore.Relationship{
		TenantID: tenantID, SubjectType: "entity", SubjectID: second.ID, SubjectRelation: "viewer",
		Relation: "owner", ResourceType: "store", ResourceID: first.ID,
	})
	assert.ErrorIs(t, err, iamcore.ErrRelationshipHierarchy)
	var relationshipsAfter int64
	require.NoError(t, db.Table(prefix+"iam_relationships").Count(&relationshipsAfter).Error)
	assert.Equal(t, relationshipsBefore, relationshipsAfter)

	revisions, err := iamcore.NewRevisionStore(prefix)
	require.NoError(t, err)
	current, err := revisions.Current(t.Context(), db, tenantID)
	require.NoError(t, err)
	require.Positive(t, current)
	require.NoError(t, revisions.Bump(t.Context(), db, tenantID, now.Add(time.Second)))
	require.NoError(t, revisions.Bump(t.Context(), db, tenantID, now.Add(2*time.Second)))
	advanced, err := revisions.Current(t.Context(), db, tenantID)
	require.NoError(t, err)
	assert.Equal(t, current+2, advanced)
}

func verifyMemberAuthorizationSourceExpiries(
	t *testing.T,
	db *gorm.DB,
	service *iamcore.Service,
	repository *iamcore.Repository,
	prefix string,
	tenantID, principalID guid.ID,
	now time.Time,
) {
	t.Helper()
	windowNow := time.Now().UTC().Truncate(time.Millisecond)
	createRole := func(name, permission string) iamcore.Role {
		role, err := service.CreateRole(t.Context(), tenantID, name, "")
		require.NoError(t, err)
		_, err = service.GrantPermission(t.Context(), tenantID, role.ID, permission)
		require.NoError(t, err)
		return role
	}
	temporaryRole := createRole("Temporary source", "menu_view")
	groupRole := createRole("Group source", "group_view")
	positionRole := createRole("Position source", "position_view")
	entityRole := createRole("Entity source", "entity_view")

	temporaryEnd := windowNow.Add(2 * time.Hour)
	require.NoError(t, repository.GrantTemporaryRole(
		t.Context(), tenantID, 99_001, temporaryRole.ID, principalID, principalID,
		windowNow.Add(-time.Hour), temporaryEnd,
	))
	groupEnd := windowNow.Add(3 * time.Hour)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_groups (tenant_id, id, name, name_key, group_type, rule_json, description, status, sort_order, version, created_at, updated_at) VALUES (?, 92001, 'Reviewers', 'reviewers', 'static', '{}', '', 'active', 0, 1, ?, ?)",
		tenantID, now, now,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_group_members (tenant_id, group_id, principal_id, starts_at, ends_at, created_at, updated_at) VALUES (?, 92001, ?, ?, ?, ?, ?)",
		tenantID, principalID, windowNow.Add(-time.Hour), groupEnd, now, now,
	).Error)
	_, err := service.AddDirectoryBinding(t.Context(), tenantID, groupRole.ID, iamcore.DirectoryAssigneeGroup, 92_001)
	require.NoError(t, err)

	positionEnd := windowNow.Add(4 * time.Hour)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_positions (tenant_id, id, code, name, status, sort_order, version, created_at, updated_at) VALUES (?, 93001, 'reviewer', 'Reviewer', 'active', 0, 1, ?, ?)",
		tenantID, now, now,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO "+prefix+"iam_position_members (tenant_id, position_id, principal_id, starts_at, ends_at, created_at, updated_at) VALUES (?, 93001, ?, ?, ?, ?, ?)",
		tenantID, principalID, windowNow.Add(-time.Hour), positionEnd, now, now,
	).Error)
	_, err = service.AddDirectoryBinding(t.Context(), tenantID, positionRole.ID, iamcore.DirectoryAssigneePosition, 93_001)
	require.NoError(t, err)

	entity, err := service.CreateEntity(t.Context(), iamcore.Entity{
		TenantID: tenantID, Type: "store", Name: "Expiry scope", Status: iamcore.EntityActive,
	})
	require.NoError(t, err)
	entityEnd := windowNow.Add(5 * time.Hour)
	_, _, err = service.PutEntityRoleBinding(
		t.Context(), tenantID, entity.ID, entityRole.ID, principalID, iamcore.BindingAllow, entityEnd,
	)
	require.NoError(t, err)

	snapshot, err := service.ListMemberAuthorizationSources(t.Context(), tenantID, principalID)
	require.NoError(t, err)
	expiries := map[iamcore.MemberAuthorizationSourceType]time.Time{}
	for _, source := range snapshot.Sources {
		if source.ExpiresAt != nil {
			expiries[source.Type] = *source.ExpiresAt
		}
	}
	assert.Equal(t, temporaryEnd, expiries[iamcore.AuthorizationSourceTemporaryRole])
	assert.Equal(t, groupEnd, expiries[iamcore.AuthorizationSourceStaticGroup])
	assert.Equal(t, positionEnd, expiries[iamcore.AuthorizationSourcePosition])
	assert.Equal(t, entityEnd, expiries[iamcore.AuthorizationSourceEntityBinding])
}

func verifyCoreRelationshipTraversalIndex(t *testing.T, db *sql.DB, prefix, dialect string) {
	t.Helper()
	columns := coreIndexColumns(t, db, prefix+"iam_relationships", prefix+"idx_iam_relationships_reverse", dialect)
	assert.Equal(t, []string{"tenant_id", "resource_id", "relation"}, columns)
}

func verifyCoreRelationshipTraversalMigrationLifecycle(
	t *testing.T,
	db *sql.DB,
	source goosex.Source,
	prefix, dialect string,
) {
	t.Helper()
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 10))
	assert.Empty(t, coreIndexColumns(
		t, db, prefix+"iam_relationships", prefix+"idx_iam_relationships_reverse", dialect,
	))
	assert.Equal(t, []string{"tenant_id", "resource_type", "resource_id", "relation"}, coreIndexColumns(
		t, db, prefix+"iam_relationships", prefix+"idx_iam_relationships_resource", dialect,
	))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 11))
	verifyCoreRelationshipTraversalIndex(t, db, prefix, dialect)
}

func coreIndexColumns(t *testing.T, db *sql.DB, table, index, dialect string) []string {
	t.Helper()
	var query string
	var args []any
	switch dialect {
	case "postgres":
		query = `
SELECT attribute.attname
FROM pg_index AS definition
JOIN pg_class AS index_class ON index_class.oid = definition.indexrelid
JOIN pg_class AS table_class ON table_class.oid = definition.indrelid
JOIN pg_namespace AS namespace ON namespace.oid = table_class.relnamespace
JOIN LATERAL unnest(definition.indkey) WITH ORDINALITY AS key(attnum, position) ON TRUE
JOIN pg_attribute AS attribute
  ON attribute.attrelid = table_class.oid AND attribute.attnum = key.attnum
WHERE namespace.nspname = current_schema()
  AND table_class.relname = $1
  AND index_class.relname = $2
  AND key.position <= definition.indnkeyatts
ORDER BY key.position`
		args = []any{table, index}
	case "mysql":
		query = `
SELECT column_name
FROM information_schema.statistics
WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
ORDER BY seq_in_index`
		args = []any{table, index}
	default:
		query = `SELECT name FROM pragma_index_info(?) ORDER BY seqno`
		args = []any{index}
	}

	rows, err := db.QueryContext(t.Context(), query, args...)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	var columns []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		columns = append(columns, column)
	}
	require.NoError(t, rows.Err())
	return columns
}

func verifyCoreRoleContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	roles := prefix + "iam_roles"
	permissions := prefix + "iam_role_permissions"

	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 1, 'Admin', 'admin', '%s', '%s')",
		roles, coreTestTime, coreTestTime))
	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 2, 'admin', 'admin', '%s', '%s')",
		roles, coreTestTime, coreTestTime), "normalized role names must be unique within a tenant")
	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 3, 'Accented', '%s', '%s', '%s')",
		roles, "\u00e1dmin", coreTestTime, coreTestTime), "name keys must not fold accents")

	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, role_id, permission_code, created_at) VALUES (1, 999, 'user_view', '%s')",
		permissions, coreTestTime), "role permissions must reference a role in the same tenant")
	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, role_id, permission_code, created_at) VALUES (1, 1, 'user_view', '%s')",
		permissions, coreTestTime))
	require.NoError(t, execCoreSQL(t, db, "DELETE FROM %s WHERE tenant_id = 1 AND id = 1", roles))
	assert.Zero(t, coreRowCount(t, db, permissions), "deleting a role must cascade its permissions")
}

func verifyCoreMenuContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	menus := prefix + "iam_menus"
	insert := func(id int, parent, route string) error {
		return execCoreSQL(t, db,
			"INSERT INTO %s (tenant_id, id, parent_id, label, route, status, created_at, updated_at) VALUES (1, %d, %s, 'Menu', %s, 'active', '%s', '%s')",
			menus, id, parent, route, coreTestTime, coreTestTime)
	}
	require.NoError(t, insert(100, "NULL", "NULL"))
	require.NoError(t, insert(101, "NULL", "NULL"), "multiple grouping menus may have a NULL route")
	require.NoError(t, insert(102, "NULL", "'/Admin'"))
	require.NoError(t, insert(103, "NULL", "'/admin'"), "routes are binary protocol identifiers")
	assert.Error(t, insert(104, "NULL", "'/Admin'"), "non-NULL routes must be unique within a tenant")
	require.NoError(t, insert(105, "100", "'/child'"))
	assert.Error(t, execCoreSQL(t, db, "DELETE FROM %s WHERE tenant_id = 1 AND id = 100", menus),
		"a parent menu with children must be protected by RESTRICT")
}

func verifyCoreEntityContracts(t *testing.T, db *sql.DB, prefix, dialect string) {
	t.Helper()
	entities := prefix + "iam_entities"
	relationships := prefix + "iam_relationships"
	resourceRelationships := prefix + "iam_resource_relationships"
	insert := func(id int, parent, entityType, name, nameKey string) error {
		return execCoreSQL(t, db,
			"INSERT INTO %s (tenant_id, id, parent_id, type, name, name_key, status, created_at, updated_at) VALUES (1, %d, %s, '%s', '%s', '%s', 'active', '%s', '%s')",
			entities, id, parent, entityType, name, nameKey, coreTestTime, coreTestTime)
	}
	require.NoError(t, insert(10, "NULL", "company", "Root", "root"))
	assert.Error(t, insert(11, "NULL", "company", "root", "root"),
		"root siblings with the same normalized name must conflict")
	require.NoError(t, insert(20, "NULL", "company", "Other", "other"))
	require.NoError(t, insert(11, "10", "store", "Branch", "branch"))
	assert.Error(t, insert(12, "10", "store", "branch", "branch"),
		"children under the same parent must use distinct normalized names")
	require.NoError(t, insert(13, "20", "store", "Branch", "branch"),
		"the same normalized child name is valid under another parent")
	require.NoError(t, insert(30, "NULL", "company", "Target", "target"))

	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, subject_type, subject_id, relation, resource_type, resource_id, created_at) VALUES (1, 'principal', 100, 'owner', 'store', 30, '%s')",
		relationships, coreTestTime), "a relationship resource type must match its entity")
	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, subject_type, subject_id, relation, resource_type, resource_id, created_at) VALUES (1, 'principal', 100, 'owner', 'company', 30, '%s')",
		relationships, coreTestTime))
	assert.Error(t, execCoreSQL(t, db,
		"UPDATE %s SET type = 'store' WHERE tenant_id = 1 AND id = 30", entities),
		"an entity type referenced by a relationship is immutable")
	assert.Error(t, execCoreSQL(t, db,
		"DELETE FROM %s WHERE tenant_id = 1 AND id = 30", entities),
		"relationship targets must preserve the source RESTRICT semantics")
	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, entity_id, subject_type, subject_id, relation, resource_type, resource_id, created_at) VALUES (1, 999, 'principal', 100, 'viewer', 'store', 200, '%s')",
		resourceRelationships, coreTestTime), "resource relationships must reference their owning entity")

	if dialect == "mysql" {
		assert.Error(t, insert(0, "NULL", "company", "Zero", "zero"),
			"MySQL's root sentinel requires positive entity identifiers")
	}
}

func verifyCoreBindingAndGrantContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	roles := prefix + "iam_roles"
	bindings := prefix + "iam_role_bindings"
	revisions := prefix + "iam_policy_revisions"
	temporaryGrants := prefix + "iam_temporary_role_grants"

	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 100, 'Operator', 'operator', '%s', '%s')",
		roles, coreTestTime, coreTestTime))
	require.NoError(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, role_id, principal_id, scope_type, scope_id, effect, created_at) VALUES (1, 100, 200, 'entity', 300, 'allow', '%s')",
		bindings, coreTestTime))
	var expiresIsNull int
	require.NoError(t, db.QueryRowContext(t.Context(),
		"SELECT CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END FROM "+bindings+" WHERE tenant_id = 1 AND role_id = 100",
	).Scan(&expiresIsNull))
	assert.Equal(t, 1, expiresIsNull, "an absent binding expiry must be NULL")
	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, role_id, principal_id, scope_type, scope_id, effect, created_at) VALUES (1, 100, 201, 'ENTITY', 300, 'allow', '%s')",
		bindings, coreTestTime), "scope CHECK values must be case-sensitive")
	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, revision, updated_at) VALUES (1, -1, '%s')",
		revisions, coreTestTime), "policy revisions cannot be negative")
	assert.Error(t, execCoreSQL(t, db,
		"INSERT INTO %s (tenant_id, id, role_id, principal_id, source_type, source_id, starts_at, ends_at, created_by, created_at) VALUES (1, 1, 100, 200, 'manual', 1, '%s', '2026-09-03 00:00:00', 300, '%s')",
		temporaryGrants, coreTestTime, coreTestTime), "temporary grants only accept access-request sources")
}

func execCoreSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}

func coreRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count))
	return count
}
