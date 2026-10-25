package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const provisioningTestTime = "2026-09-02 00:00:00"

func TestIAMProvisioningMigrationsAcrossEngines(t *testing.T) {
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
					verifyProvisioningBaseNeedsOrganization(t, db, engine.dialect, prefix+"base_")
					verifyProvisioningConstraintDataLifecycle(t, db, engine.dialect, prefix+"data_")
					verifyProvisioningActorDataLifecycle(t, db, engine.dialect, prefix+"actor_", engine.name != "mysql")

					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := provisioningMigrationModules()
					assertProvisioningUp(t, gormDB, modules, prefix)
					verifyProvisioningCoordinationRollback(t, db, gormDB, engine.dialect, prefix)
					verifyProvisioningContracts(t, db, prefix)
					verifyProvisioningRollbackGraph(t, db, gormDB, modules, engine.dialect, prefix)
				})
			}
		})
	}
}

func provisioningMigrationModules() []module.Module {
	// Deliberately not dependency ordered: the milestone graph owns ordering.
	return []module.Module{&provisioning.Module{}, &organization.Module{}, &identity.Module{}, &iamcore.Module{}}
}

// The guard must refuse each unsafe state independently, before any column or
// row disappears. This helper also runs against the real MySQL gate because
// its failed DDL transaction leaves the guard table behind.
func verifyProvisioningCoordinationRollback(t *testing.T, db *sql.DB, gormDB *gorm.DB, dialect, prefix string) {
	t.Helper()
	source := (&provisioning.Module{}).MigrationSource()
	source.Prefix = prefix
	seedProvisioningRows(t, db, prefix)
	for _, state := range []struct {
		name     string
		token    int64
		retiring bool
	}{
		{name: "unfinished_operation", token: 42},
		{name: "retirement_in_progress", retiring: true},
	} {
		require.NoError(t, execProvisioningSQL(t, db,
			"UPDATE %siam_scim_targets SET operation_token = %d, retiring = %t, operation_kind = 'push', operation_resource_type = 'Group', operation_resource_id = 500, operation_started_at = '%s', sync_resource_type = 'Group', sync_resource_id = 499, sync_failures = 2, sync_after_ms = 123 WHERE id = 400",
			prefix, state.token, state.retiring, provisioningTestTime))
		require.Error(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 5), state.name)
		status, err := goosex.Inspect(t.Context(), db, source, dialect)
		require.NoError(t, err)
		assert.EqualValues(t, 6, status.Current, state.name)
		assert.Equal(t, goosex.StateClean, status.State, state.name)
		var token, resourceID, cursorID, failures, after int64
		var retiring bool
		var kind, resourceType, cursorType, name, bearer string
		var started any
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT operation_token, retiring, operation_kind, operation_resource_type, operation_resource_id, operation_started_at, sync_resource_type, sync_resource_id, sync_failures, sync_after_ms, name, bearer_token_ciphertext FROM "+prefix+"iam_scim_targets WHERE id = 400").
			Scan(&token, &retiring, &kind, &resourceType, &resourceID, &started, &cursorType, &cursorID, &failures, &after, &name, &bearer))
		assert.Equal(t, state.token, token)
		assert.Equal(t, state.retiring, retiring)
		assert.Equal(t, "push", kind)
		assert.Equal(t, "Group", resourceType)
		assert.EqualValues(t, 500, resourceID)
		assert.NotNil(t, started)
		assert.Equal(t, "Group", cursorType)
		assert.EqualValues(t, 499, cursorID)
		assert.EqualValues(t, 2, failures)
		assert.EqualValues(t, 123, after)
		assert.Equal(t, "Target", name)
		assert.Equal(t, "Cipher", bearer)
		for _, table := range provisioningTables(prefix) {
			assert.Equal(t, 1, tableRowCount(t, db, table), state.name+": "+table)
		}
	}
	// A normally populated, quiescent target must remain roll-backable; only
	// clearing all targets would miss the positive COUNT = 0 branch.
	require.NoError(t, execProvisioningSQL(t, db, "UPDATE %siam_scim_targets SET operation_token = 0, retiring = FALSE WHERE id = 400", prefix))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 5))
	status, err := goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, err)
	assert.EqualValues(t, 5, status.Current)
	assert.False(t, gormDB.Migrator().HasColumn(prefix+"iam_scim_targets", "operation_token"))
	assert.False(t, gormDB.Migrator().HasColumn(prefix+"iam_scim_targets", "retiring"))
	assertTableExists(t, db, dialect, prefix+"scim_coordination_rollback_guard", false)
	for _, table := range provisioningTables(prefix) {
		assert.Equal(t, 1, tableRowCount(t, db, table), "safe down preserves "+table)
	}
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 6))
	status, err = goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, err)
	assert.EqualValues(t, 6, status.Current)
	var token int64
	var retiring bool
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT operation_token, retiring FROM "+prefix+"iam_scim_targets WHERE id = 400").Scan(&token, &retiring))
	assert.Zero(t, token)
	assert.False(t, retiring)
	for _, table := range provisioningTables(prefix) {
		assert.Equal(t, 1, tableRowCount(t, db, table), "safe up preserves "+table)
	}
	require.NoError(t, execProvisioningSQL(t, db, "DELETE FROM %siam_tenants WHERE id = 1", prefix))
}

func verifyProvisioningBaseNeedsOrganization(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&provisioning.Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 2))
	err := goosex.MigrateUpTo(t.Context(), db, source, dialect, 3)
	assert.Error(t, err, "provisioning constraints must not run before organization base")
	status, inspectErr := goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, inspectErr)
	assert.Equal(t, int64(2), status.Current)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 0))
}

func verifyProvisioningConstraintDataLifecycle(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	organizationSource := (&organization.Module{}).MigrationSource()
	organizationSource.Prefix = prefix
	provisioningSource := (&provisioning.Module{}).MigrationSource()
	provisioningSource.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, organizationSource, dialect, 7))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, provisioningSource, dialect, 2))
	seedProvisioningRows(t, db, prefix)

	for _, target := range []int64{3, 2, 3} {
		if target == 3 {
			require.NoError(t, goosex.MigrateUpTo(t.Context(), db, provisioningSource, dialect, target))
		} else {
			require.NoError(t, goosex.MigrateDownTo(t.Context(), db, provisioningSource, dialect, target))
		}
		for _, table := range provisioningTables(prefix) {
			assert.Equal(t, 1, tableRowCount(t, db, table), "%s data survives constraint rebuild", table)
		}
	}
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, provisioningSource, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, organizationSource, dialect, 0))
}

func assertProvisioningUp(t *testing.T, db *gorm.DB, modules []module.Module, prefix string) {
	t.Helper()
	reports, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationUp,
	}, prefix)
	require.NoError(t, err)
	require.Len(t, reports, len(modules))
	milestones := make(map[string]string, len(reports))
	for _, report := range reports {
		milestones[report.Module] = report.Milestone
		assert.Equal(t, goosex.StateClean, report.Status.State)
	}
	assert.Equal(t, "menu-contributions", milestones[iamcore.Name])
	assert.Equal(t, "administration-scopes", milestones[organization.Name])
	assert.Equal(t, "phone", milestones[identity.Name])
	assert.Equal(t, "outbound-coordination", milestones[provisioning.Name])
}

func verifyProvisioningContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	seedProvisioningTenant(t, db, prefix)
	directories := prefix + "iam_scim_directories"
	credentials := prefix + "iam_scim_credentials"
	resources := prefix + "iam_scim_resources"
	targets := prefix + "iam_scim_targets"
	targetResources := prefix + "iam_scim_target_resources"

	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, name_key, status, created_at, updated_at) VALUES (90, 999, 'Orphan', 'orphan', 'active', '%s', '%s')",
		directories, provisioningTestTime, provisioningTestTime), "directories require an existing tenant")
	require.NoError(t, insertProvisioningDirectory(t, db, directories, 100, "Directory", "directory", "active"))
	assert.Error(t, insertProvisioningDirectory(t, db, directories, 101, "Bad", "bad", "ACTIVE"),
		"directory status is case-sensitive")
	assert.Error(t, insertProvisioningDirectory(t, db, directories, 102, strings.Repeat("é", 65), "too-long", "active"),
		"directory names retain the source 128-byte limit")

	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, directory_id, name, token_hash, created_at) VALUES (200, 100, 'Primary', 'HashValue', '%s')",
		credentials, provisioningTestTime))
	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, directory_id, name, token_hash, created_at) VALUES (201, 100, '%s', 'HashValue', '%s')",
		credentials, strings.Repeat("é", 65), provisioningTestTime), "credential names retain the source 128-byte limit")
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (directory_id, resource_type, resource_id, external_id, external_key, created_at, updated_at) VALUES (100, 'User', 300, 'ABC', 'ABC', '%s', '%s')",
		resources, provisioningTestTime, provisioningTestTime))
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (directory_id, resource_type, resource_id, external_id, external_key, created_at, updated_at) VALUES (100, 'User', 301, 'abc', 'abc', '%s', '%s')",
		resources, provisioningTestTime, provisioningTestTime), "SCIM external keys compare as binary identifiers")
	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (directory_id, resource_type, resource_id, external_key, created_at, updated_at) VALUES (100, 'user', 302, 'bad', '%s', '%s')",
		resources, provisioningTestTime, provisioningTestTime), "resource types are case-sensitive")
	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (directory_id, resource_type, resource_id, external_id, external_key, created_at, updated_at) VALUES (100, 'User', 303, '%s', 'oversized', '%s', '%s')",
		resources, strings.Repeat("x", 513), provisioningTestTime, provisioningTestTime),
		"SCIM external identifiers retain the source 512-byte limit")

	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, name_key, base_url, bearer_token_ciphertext, status, created_at, updated_at) VALUES (390, 999, 'Orphan', 'orphan', 'https://example.test/scim', 'Cipher', 'active', '%s', '%s')",
		targets, provisioningTestTime, provisioningTestTime), "targets require an existing tenant")
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, name_key, base_url, bearer_token_ciphertext, status, created_at, updated_at) VALUES (400, 1, 'Target', 'target', 'https://example.test/scim', 'Cipher', 'active', '%s', '%s')",
		targets, provisioningTestTime, provisioningTestTime))
	assert.Error(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, name_key, base_url, bearer_token_ciphertext, status, created_at, updated_at) VALUES (401, 1, '%s', 'too-long', 'https://example.test/scim', 'Cipher', 'active', '%s', '%s')",
		targets, strings.Repeat("é", 65), provisioningTestTime, provisioningTestTime),
		"target names retain the source 128-byte limit")
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %s (target_id, resource_type, resource_id, external_id, created_at, updated_at) VALUES (400, 'Group', 500, 'RemoteGroup', '%s', '%s')",
		targetResources, provisioningTestTime, provisioningTestTime))

	assertProvisioningNullableState(t, db, credentials, resources, targetResources)
	require.NoError(t, execProvisioningSQL(t, db, "DELETE FROM %siam_tenants WHERE id = 1", prefix))
	for _, table := range provisioningTables(prefix) {
		assert.Zero(t, tableRowCount(t, db, table), "tenant deletion cascades %s", table)
	}
}

func seedProvisioningRows(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	seedProvisioningTenant(t, db, prefix)
	require.NoError(t, insertProvisioningDirectory(t, db, prefix+"iam_scim_directories", 100, "Directory", "directory", "active"))
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %siam_scim_credentials (id, directory_id, name, token_hash, created_at) VALUES (200, 100, 'Primary', 'HashValue', '%s')",
		prefix, provisioningTestTime))
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %siam_scim_resources (directory_id, resource_type, resource_id, external_key, created_at, updated_at) VALUES (100, 'User', 300, 'user-300', '%s', '%s')",
		prefix, provisioningTestTime, provisioningTestTime))
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %siam_scim_targets (id, tenant_id, name, name_key, base_url, bearer_token_ciphertext, status, created_at, updated_at) VALUES (400, 1, 'Target', 'target', 'https://example.test/scim', 'Cipher', 'active', '%s', '%s')",
		prefix, provisioningTestTime, provisioningTestTime))
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %siam_scim_target_resources (target_id, resource_type, resource_id, created_at, updated_at) VALUES (400, 'Group', 500, '%s', '%s')",
		prefix, provisioningTestTime, provisioningTestTime))
}

func seedProvisioningTenant(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	require.NoError(t, execProvisioningSQL(t, db,
		"INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (1, 'provisioning', 'Provisioning', 'active', 1, '%s', '%s')",
		prefix, provisioningTestTime, provisioningTestTime))
}

func insertProvisioningDirectory(t *testing.T, db *sql.DB, table string, id int, name, nameKey, status string) error {
	t.Helper()
	return execProvisioningSQL(t, db,
		"INSERT INTO %s (id, tenant_id, name, name_key, status, created_at, updated_at) VALUES (%d, 1, '%s', '%s', '%s', '%s', '%s')",
		table, id, name, nameKey, status, provisioningTestTime, provisioningTestTime)
}

func assertProvisioningNullableState(t *testing.T, db *sql.DB, credentials, resources, targetResources string) {
	t.Helper()
	var absent int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN expires_at IS NULL AND last_used_at IS NULL AND revoked_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE id = 200",
		credentials)).Scan(&absent))
	assert.Equal(t, 1, absent)
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN deleted_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE resource_id = 300",
		resources)).Scan(&absent))
	assert.Equal(t, 1, absent)
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN deleted_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE resource_id = 500",
		targetResources)).Scan(&absent))
	assert.Equal(t, 1, absent)
}

func verifyProvisioningRollbackGraph(
	t *testing.T,
	sqlDB *sql.DB,
	db *gorm.DB,
	modules []module.Module,
	dialect, prefix string,
) {
	t.Helper()
	_, err := app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: organization.Name, To: 0,
	}, prefix)
	assert.ErrorContains(t, err, `"provisioning.constraints"`)
	_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: identity.Name, To: 0,
	}, prefix)
	assert.ErrorContains(t, err, `"provisioning.service_accounts"`)

	// Retire the core dependency on organization before removing its base.
	_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: iamcore.Name, To: 16,
	}, prefix)
	require.NoError(t, err)
	for _, name := range []string{provisioning.Name, organization.Name, iamcore.Name, identity.Name} {
		_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
			Action: app.MigrationDownTo, Module: name, To: 0,
		}, prefix)
		require.NoError(t, err, "roll back %s on %s", name, dialect)
	}
	assertTableExists(t, sqlDB, dialect, prefix+"iam_scim_directories", false)
	assertProvisioningUp(t, db, modules, prefix)
}

func provisioningTables(prefix string) []string {
	return []string{
		prefix + "iam_scim_directories",
		prefix + "iam_scim_credentials",
		prefix + "iam_scim_resources",
		prefix + "iam_scim_targets",
		prefix + "iam_scim_target_resources",
	}
}

func verifyProvisioningActorDataLifecycle(t *testing.T, db *sql.DB, dialect, prefix string, transactions bool) {
	t.Helper()
	organizationSource := (&organization.Module{}).MigrationSource()
	organizationSource.Prefix = prefix
	identitySource := (&identity.Module{}).MigrationSource()
	identitySource.Prefix = prefix
	source := (&provisioning.Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, organizationSource, dialect, 7))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, identitySource, dialect, 2))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 3))
	seedProvisioningRows(t, db, prefix)
	// Existing non-positive account keys distinguish the CHECK from the foreign key.
	for _, id := range []int{700, 0, -1} {
		require.NoError(t, execProvisioningSQL(t, db,
			"INSERT INTO %siam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (%d, 'actor-%d', 'Actor', 'active', '%s', '%s')",
			prefix, id, id, provisioningTestTime, provisioningTestTime))
		require.NoError(t, execProvisioningSQL(t, db,
			"INSERT INTO %siam_service_accounts (principal_id, owner_tenant_id, created_at, updated_at) VALUES (%d, 1, '%s', '%s')",
			prefix, id, provisioningTestTime, provisioningTestTime))
	}
	for cycle := 0; cycle < 2; cycle++ {
		require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 4))
		for _, table := range provisioningTables(prefix) {
			assert.Equal(t, 1, tableRowCount(t, db, table), "upgrade preserves %s", table)
		}
		for _, table := range []string{prefix + "iam_scim_directories", prefix + "iam_scim_targets"} {
			var binding sql.NullInt64
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT service_account_id FROM "+table).Scan(&binding))
			assert.False(t, binding.Valid, "migration must not invent an automation actor")
			for _, id := range []int{0, -1, 799} {
				assert.Error(t, execProvisioningSQL(t, db, "UPDATE %s SET service_account_id = %d", table, id))
			}
			// The in-process MySQL driver returns fakeTransaction with no-op rollback.
			// The real MySQL gate runs these assertions with transactions enabled.
			if transactions {
				tx, err := db.BeginTx(t.Context(), nil)
				require.NoError(t, err)
				_, err = tx.ExecContext(t.Context(), "UPDATE "+table+" SET service_account_id = 700")
				require.NoError(t, err)
				require.NoError(t, tx.Rollback())
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT service_account_id FROM "+table).Scan(&binding))
				assert.False(t, binding.Valid, "binding writes roll back")
			}
			require.NoError(t, execProvisioningSQL(t, db, "UPDATE %s SET service_account_id = 700", table))
			assert.Error(t, execProvisioningSQL(t, db, "DELETE FROM %siam_service_accounts WHERE principal_id = 700", prefix),
				"bound service accounts cannot be physically deleted")
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT service_account_id FROM "+table).Scan(&binding))
			assert.Equal(t, sql.NullInt64{Int64: 700, Valid: true}, binding)
		}
		require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 3))
		for _, table := range provisioningTables(prefix) {
			assert.Equal(t, 1, tableRowCount(t, db, table), "rollback preserves %s", table)
		}
	}
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, identitySource, dialect, 0))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, organizationSource, dialect, 0))
}

func execProvisioningSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}
