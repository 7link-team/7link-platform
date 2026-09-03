package migration_test

import (
	"database/sql"
	"fmt"
	"testing"

	iamcore "7link.com/platform/internal/iam/core"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const coreTestTime = "2026-09-02 00:00:00"

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
					source := (&iamcore.Module{}).MigrationSource()
					source.Prefix = prefix
					require.NoError(t, goosex.Migrate(t.Context(), db, source, engine.dialect))

					verifyCoreRoleContracts(t, db, prefix)
					verifyCoreMenuContracts(t, db, prefix)
					verifyCoreEntityContracts(t, db, prefix, engine.dialect)
					verifyCoreBindingAndGrantContracts(t, db, prefix)
				})
			}
		})
	}
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
