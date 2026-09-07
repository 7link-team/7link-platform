package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const governanceTestTime = "2026-09-02 00:00:00"

func TestIAMGovernanceMigrationsAcrossEngines(t *testing.T) {
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
					verifyGovernanceBaseNeedsOrganization(t, db, engine.dialect, prefix+"base_")

					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := governanceMigrationModules()
					assertGovernanceUp(t, gormDB, modules, prefix)
					verifyGovernanceContracts(t, db, prefix)
					verifyGovernanceRollbackGraph(t, db, gormDB, modules, engine.dialect, prefix)
				})
			}
		})
	}
}

func governanceMigrationModules() []module.Module {
	// Deliberately not dependency ordered: the milestone graph owns ordering.
	return []module.Module{&governance.Module{}, &organization.Module{}, &iamcore.Module{}, &identity.Module{}}
}

func verifyGovernanceBaseNeedsOrganization(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&governance.Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 2))
	err := goosex.MigrateUpTo(t.Context(), db, source, dialect, 3)
	assert.Error(t, err, "governance constraints must not run before organization base")
	status, inspectErr := goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, inspectErr)
	assert.Equal(t, int64(2), status.Current)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 0))
}

func assertGovernanceUp(t *testing.T, db *gorm.DB, modules []module.Module, prefix string) {
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
	assert.Equal(t, "principal-erasure", milestones[iamcore.Name])
	assert.Equal(t, "principal-erasure", milestones[organization.Name])
	assert.Equal(t, "principal-erasure", milestones[governance.Name])
}

func verifyGovernanceContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	require.NoError(t, execGovernanceSQL(t, db,
		"INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (1, 'governance', 'Governance', 'active', 1, '%s', '%s')",
		prefix, governanceTestTime, governanceTestTime))

	requests := prefix + "iam_access_requests"
	insertRequest := func(tenant, id int, status, reason string) error {
		return execGovernanceSQL(t, db,
			"INSERT INTO %s (tenant_id, id, requester_id, role_id, role_name, reason, snapshot_json, status, access_expires_at, request_expires_at, created_at, updated_at) VALUES (%d, %d, 10, 20, 'Role', '%s', '{}', '%s', '2026-09-03 00:00:00', '2026-09-03 00:00:00', '%s', '%s')",
			requests, tenant, id, reason, status, governanceTestTime, governanceTestTime)
	}
	assert.Error(t, insertRequest(999, 1, "pending", "needed"), "access requests require an existing tenant")
	require.NoError(t, insertRequest(1, 1, "pending", "needed"))
	assert.Error(t, insertRequest(1, 2, "PENDING", "needed"), "request status is case-sensitive")
	assert.Error(t, insertRequest(1, 3, "pending", strings.Repeat("x", 501)), "request reasons retain the 500-byte limit")

	steps := prefix + "iam_approval_steps"
	require.NoError(t, execGovernanceSQL(t, db,
		"INSERT INTO %s (tenant_id, request_id, step, decision) VALUES (1, 1, 1, 'pending')", steps))
	assertNullableGovernanceState(t, db, requests, steps)

	reviews := prefix + "iam_access_reviews"
	items := prefix + "iam_access_review_items"
	assert.Error(t, execGovernanceSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, owner_id, status, due_at, created_at, updated_at) VALUES (999, 1, 'Review', 10, 'open', '2026-09-03 00:00:00', '%s', '%s')",
		reviews, governanceTestTime, governanceTestTime), "access reviews require an existing tenant")
	require.NoError(t, execGovernanceSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, owner_id, status, due_at, created_at, updated_at) VALUES (1, 1, 'Review', 10, 'open', '2026-09-03 00:00:00', '%s', '%s')",
		reviews, governanceTestTime, governanceTestTime))

	grantTypes := []string{"permanent", "temporary", "group", "position", "entity", "dynamic_group", "relationship"}
	for index, grantType := range grantTypes {
		require.NoError(t, execGovernanceSQL(t, db,
			"INSERT INTO %s (tenant_id, review_id, id, principal_id, principal_name, role_id, role_name, grant_type, grant_created_at, decision) VALUES (1, 1, %d, 10, 'Principal', 20, 'Role', '%s', '%s', 'pending')",
			items, index+1, grantType, governanceTestTime), "grant type %s", grantType)
	}
	assert.Error(t, execGovernanceSQL(t, db,
		"INSERT INTO %s (tenant_id, review_id, id, principal_id, principal_name, role_id, role_name, grant_type, grant_created_at, decision) VALUES (1, 1, 99, 10, 'Principal', 20, 'Role', 'GROUP', '%s', 'pending')",
		items, governanceTestTime), "grant types are case-sensitive")
	var absent int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN grant_id IS NULL AND grant_expires_at IS NULL AND decided_by IS NULL AND decided_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE id = 1",
		items)).Scan(&absent))
	assert.Equal(t, 1, absent)

	require.NoError(t, execGovernanceSQL(t, db, "DELETE FROM %siam_tenants WHERE id = 1", prefix))
	for _, table := range []string{requests, steps, reviews, items} {
		assert.Zero(t, tableRowCount(t, db, table), "tenant deletion cascades %s", table)
	}
}

func assertNullableGovernanceState(t *testing.T, db *sql.DB, requests, steps string) {
	t.Helper()
	var absent int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN decided_by IS NULL AND decided_at IS NULL AND revoked_by IS NULL AND revoked_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE id = 1",
		requests)).Scan(&absent))
	assert.Equal(t, 1, absent)
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN decided_by IS NULL AND decided_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE request_id = 1",
		steps)).Scan(&absent))
	assert.Equal(t, 1, absent)
}

func verifyGovernanceRollbackGraph(
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
	assert.ErrorContains(t, err, `"governance.constraints"`)

	for _, name := range []string{governance.Name, organization.Name, iamcore.Name} {
		_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
			Action: app.MigrationDownTo, Module: name, To: 0,
		}, prefix)
		require.NoError(t, err, "roll back %s on %s", name, dialect)
	}
	assertTableExists(t, sqlDB, dialect, prefix+"iam_access_requests", false)
	assertGovernanceUp(t, db, modules, prefix)
}

func execGovernanceSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}
