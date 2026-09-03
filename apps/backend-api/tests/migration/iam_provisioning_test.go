package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	iamcore "7link.com/platform/internal/iam/core"
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

					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := provisioningMigrationModules()
					assertProvisioningUp(t, gormDB, modules, prefix)
					verifyProvisioningContracts(t, db, prefix)
					verifyProvisioningRollbackGraph(t, db, gormDB, modules, engine.dialect, prefix)
				})
			}
		})
	}
}

func provisioningMigrationModules() []module.Module {
	// Deliberately not dependency ordered: the milestone graph owns ordering.
	return []module.Module{&provisioning.Module{}, &organization.Module{}, &iamcore.Module{}}
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
	assert.Equal(t, "base", milestones[iamcore.Name])
	assert.Equal(t, "constraints", milestones[organization.Name])
	assert.Equal(t, "constraints", milestones[provisioning.Name])
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

	for _, name := range []string{provisioning.Name, organization.Name, iamcore.Name} {
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

func execProvisioningSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}
