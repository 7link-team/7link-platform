package migration_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const organizationTestTime = "2026-09-02 00:00:00"

func TestIAMOrganizationMigrationsAcrossEngines(t *testing.T) {
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
					verifyOrganizationBaseNeedsCoreForConstraints(t, db, engine.dialect, prefix+"base_")

					gormDB := openMilestoneGORM(t, db, engine.dialect)
					modules := organizationMigrationModules()
					assertOrganizationUp(t, gormDB, modules, prefix)
					verifyOrganizationContracts(t, db, engine.dialect, prefix)
					verifyOrganizationRollbackGraph(t, db, gormDB, modules, engine.dialect, prefix)
				})
			}
		})
	}
}

func organizationMigrationModules() []module.Module {
	// Deliberately not dependency ordered: the milestone graph owns ordering.
	return []module.Module{&oauth.Module{}, &organization.Module{}, &iamcore.Module{}, &identity.Module{}}
}

func verifyOrganizationBaseNeedsCoreForConstraints(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&organization.Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 7))
	err := goosex.MigrateUpTo(t.Context(), db, source, dialect, 8)
	assert.Error(t, err, "organization constraints must not run before the core base")
	status, inspectErr := goosex.Inspect(t.Context(), db, source, dialect)
	require.NoError(t, inspectErr)
	assert.Equal(t, int64(7), status.Current)
	require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 0))
}

func assertOrganizationUp(t *testing.T, db *gorm.DB, modules []module.Module, prefix string) {
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
	assert.Equal(t, "retire-positions", milestones[iamcore.Name])
	assert.Equal(t, "avatars", milestones[identity.Name])
	assert.Equal(t, "retire-positions", milestones[organization.Name])
	assert.Equal(t, "grant-incarnation", milestones[oauth.Name])
}

func verifyOrganizationContracts(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	verifyOAuthQueryIndexLifecycle(t, db, dialect, prefix)
	seedOrganizationParents(t, db, prefix)
	verifyDepartmentContracts(t, db, dialect, prefix)
	verifyPositionAndGroupContracts(t, db, prefix)
	verifyOrganizationCrossModuleConstraints(t, db, prefix)
	verifyInvitationAndOAuthContracts(t, db, prefix)
}

func verifyOAuthQueryIndexLifecycle(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	source := (&oauth.Module{}).MigrationSource()
	source.Prefix = prefix
	index := prefix + "idx_iam_refresh_principal_active"
	want := []string{"principal_id", "revoked_at"}
	for range 2 {
		assert.Equal(t, want, coreIndexColumns(t, db, prefix+"iam_refresh_tokens", index, dialect))
		require.NoError(t, goosex.MigrateDownTo(t.Context(), db, source, dialect, 5))
		assert.Empty(t, coreIndexColumns(t, db, prefix+"iam_refresh_tokens", index, dialect))
		require.NoError(t, goosex.MigrateUpTo(t.Context(), db, source, dialect, 6))
	}
	assert.Equal(t, want, coreIndexColumns(t, db, prefix+"iam_refresh_tokens", index, dialect))
}

func seedOrganizationParents(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (1, 'tenant-one', 'Tenant One', 'active', 1, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_tenants (id, slug, name, status, version, created_at, updated_at) VALUES (2, 'tenant-two', 'Tenant Two', 'active', 1, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_roles (tenant_id, id, name, name_key, created_at, updated_at) VALUES (1, 10, 'Operator', 'operator', '%s', '%s'), (1, 11, 'Invited', 'invited', '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_tenant_members (tenant_id, principal_id, status, created_at, updated_at) VALUES (1, 20, 'active', '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_entities (tenant_id, id, type, name, name_key, status, metadata, created_at, updated_at) VALUES (1, 50, 'company', 'Entity One', 'entity one', 'active', '{}', '%s', '%s'), (1, 51, 'company', 'Entity Two', 'entity two', 'active', '{}', '%s', '%s'), (2, 60, 'company', 'Entity Three', 'entity three', 'active', '{}', '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime, organizationTestTime, organizationTestTime, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (500, 'oauth-user', 'OAuth User', 'active', '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
}

func verifyDepartmentContracts(t *testing.T, db *sql.DB, dialect, prefix string) {
	t.Helper()
	departments := prefix + "iam_departments"
	insert := func(id, entityID int, parent, name, nameKey string) error {
		return execOrganizationSQL(t, db,
			"INSERT INTO %s (tenant_id, id, entity_id, parent_id, name, name_key, status, created_at, updated_at) VALUES (1, %d, %d, %s, '%s', '%s', 'active', '%s', '%s')",
			departments, id, entityID, parent, name, nameKey, organizationTestTime, organizationTestTime)
	}
	require.NoError(t, insert(100, 50, "NULL", "Root", "root"))
	assert.Error(t, insert(101, 50, "NULL", "root", "root"), "normalized root names must be unique")
	require.NoError(t, insert(102, 50, "NULL", "Other", "other"))
	require.NoError(t, insert(103, 51, "NULL", "Second Entity", "second entity"))
	require.NoError(t, insert(110, 50, "100", "Child", "child"))
	assert.Error(t, insert(111, 50, "100", "child", "child"), "normalized sibling names must be unique")
	require.NoError(t, insert(112, 50, "102", "Child", "child"))
	assert.Error(t, insert(113, 50, "999", "Orphan", "orphan"), "parent departments must exist in the same tenant")
	assert.Error(t, insert(114, 51, "100", "Cross Entity", "cross entity"), "parent departments must belong to the same entity")
	assert.Error(t, insert(115, 999, "NULL", "Missing Entity", "missing entity"), "department entities must exist in the same tenant")
	if dialect == "mysql" {
		assert.Error(t, insert(0, 50, "NULL", "Zero", "zero"),
			"MySQL's generated root key requires positive department identifiers")
	}
}

func verifyPositionAndGroupContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	positions := prefix + "iam_positions"
	groups := prefix + "iam_groups"
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, code, name, status, created_at, updated_at) VALUES (1, 200, 'engineer', 'Engineer', 'active', '%s', '%s')",
		positions, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, code, name, status, created_at, updated_at) VALUES (1, 201, 'engineer', 'Duplicate', 'active', '%s', '%s')",
		positions, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, group_type, rule_json, status, created_at, updated_at) VALUES (1, 300, 'Dynamic', 'dynamic', 'dynamic', '{}', 'active', '%s', '%s')",
		groups, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, name, name_key, group_type, status, created_at, updated_at) VALUES (1, 301, 'Bad', 'bad', 'DYNAMIC', 'active', '%s', '%s')",
		groups, organizationTestTime, organizationTestTime), "group enums must be case-sensitive")
	tooLong := strings.Repeat("x", 1025)
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(
		"INSERT INTO %s (tenant_id, id, name, name_key, group_type, description, status, created_at, updated_at) VALUES (1, 302, 'Large', 'large', 'static', '%s', 'active', '%s', '%s')",
		groups, tooLong, organizationTestTime, organizationTestTime))
	assert.Error(t, err, "group descriptions retain the source 1024-byte limit")
}

func verifyOrganizationCrossModuleConstraints(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_position_members (tenant_id, position_id, principal_id, created_at, updated_at) VALUES (1, 200, 999, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_group_members (tenant_id, group_id, principal_id, created_at, updated_at) VALUES (1, 300, 999, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_group_role_bindings (tenant_id, role_id, group_id, created_at) VALUES (1, 999, 300, '%s')",
		prefix, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_position_role_bindings (tenant_id, role_id, position_id, created_at) VALUES (1, 999, 200, '%s')",
		prefix, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_member_departments (tenant_id, principal_id, department_id, updated_at) VALUES (1, 999, 100, '%s')",
		prefix, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_role_data_scopes (tenant_id, role_id, scope_type, updated_at) VALUES (1, 999, 'all', '%s')",
		prefix, organizationTestTime))

	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_position_members (tenant_id, position_id, principal_id, created_at, updated_at) VALUES (1, 200, 20, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_group_members (tenant_id, group_id, principal_id, created_at, updated_at) VALUES (1, 300, 20, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"UPDATE %siam_position_members SET starts_at = '2026-09-03 00:00:00', ends_at = '2026-09-02 00:00:00' WHERE tenant_id = 1 AND position_id = 200",
		prefix), "membership windows must be ordered when both endpoints are present")
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_group_role_bindings (tenant_id, role_id, group_id, created_at) VALUES (1, 10, 300, '%s')",
		prefix, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_position_role_bindings (tenant_id, role_id, position_id, created_at) VALUES (1, 10, 200, '%s')",
		prefix, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_member_departments (tenant_id, principal_id, department_id, updated_at) VALUES (1, 20, 100, '%s')",
		prefix, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_role_data_scopes (tenant_id, role_id, scope_type, updated_at) VALUES (1, 10, 'all', '%s')",
		prefix, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db, "DELETE FROM %siam_groups WHERE tenant_id = 1 AND id = 300", prefix))
	assert.Error(t, execOrganizationSQL(t, db, "DELETE FROM %siam_positions WHERE tenant_id = 1 AND id = 200", prefix))
	require.NoError(t, execOrganizationSQL(t, db,
		"DELETE FROM %siam_tenant_members WHERE tenant_id = 1 AND principal_id = 20", prefix))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_position_members"))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_group_members"))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_member_departments"))
}

func verifyInvitationAndOAuthContracts(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	invitations := prefix + "iam_invitations"
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, email, email_key, token_hmac, status, expires_at, created_at, updated_at) VALUES (1, 400, 'a@example.com', 'a@example.com', 'ABC', 'pending', '2026-09-03 00:00:00', '%s', '%s')",
		invitations, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, email, email_key, token_hmac, status, expires_at, created_at, updated_at) VALUES (1, 401, 'b@example.com', 'b@example.com', 'abc', 'pending', '2026-09-03 00:00:00', '%s', '%s')",
		invitations, organizationTestTime, organizationTestTime), "invitation tokens use binary comparison")
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %s (tenant_id, id, email, email_key, token_hmac, status, expires_at, created_at, updated_at) VALUES (1, 402, 'c@example.com', 'c@example.com', 'DEF', 'PENDING', '2026-09-03 00:00:00', '%s', '%s')",
		invitations, organizationTestTime, organizationTestTime))
	var absent int
	require.NoError(t, db.QueryRowContext(t.Context(), fmt.Sprintf(
		"SELECT CASE WHEN department_id IS NULL AND accepted_by IS NULL AND accepted_at IS NULL AND revoked_at IS NULL THEN 1 ELSE 0 END FROM %s WHERE id = 400",
		invitations)).Scan(&absent))
	assert.Equal(t, 1, absent)
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_invitation_roles (tenant_id, invitation_id, role_id) VALUES (1, 400, 999)", prefix))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_invitation_roles (tenant_id, invitation_id, role_id) VALUES (1, 400, 11)", prefix))
	assert.Error(t, execOrganizationSQL(t, db,
		"DELETE FROM %siam_roles WHERE tenant_id = 1 AND id = 11", prefix),
		"invitation roles preserve source RESTRICT semantics")

	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_oauth_clients (id, tenant_id, name, created_at, updated_at) VALUES (600, 1, 'Client', '%s', '%s'), (601, 999, 'Orphan Tenant Client', '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime, organizationTestTime, organizationTestTime))
	require.NoError(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_oauth_consents (principal_id, client_id, tenant_id, created_at, last_used_at) VALUES (500, 600, 1, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime))
	assert.Error(t, execOrganizationSQL(t, db,
		"INSERT INTO %siam_oauth_consents (principal_id, client_id, tenant_id, created_at, last_used_at) VALUES (500, 601, 999, '%s', '%s')",
		prefix, organizationTestTime, organizationTestTime), "OAuth consent tenants must exist")
	require.NoError(t, execOrganizationSQL(t, db,
		"DELETE FROM %siam_roles WHERE tenant_id = 1 AND id = 10", prefix))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_group_role_bindings"))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_position_role_bindings"))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_role_data_scopes"))
	require.NoError(t, execOrganizationSQL(t, db, "DELETE FROM %siam_tenants WHERE id = 1", prefix))
	assert.Zero(t, tableRowCount(t, db, prefix+"iam_oauth_consents"))
}

func verifyOrganizationRollbackGraph(
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
	assert.ErrorContains(t, err, `cannot roll back migration milestones "organization.base", "organization.constraints"`)
	assert.ErrorContains(t, err, `"oauth.constraints"`)

	_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: iamcore.Name, To: 0,
	}, prefix)
	assert.ErrorContains(t, err, `"organization.constraints"`)

	// Retire the core dependency on organization before removing its base.
	_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
		Action: app.MigrationDownTo, Module: iamcore.Name, To: 16,
	}, prefix)
	require.NoError(t, err)
	for _, name := range []string{oauth.Name, organization.Name, iamcore.Name, identity.Name} {
		_, err = app.ExecuteModuleMigrations(t.Context(), db, modules, app.MigrationRequest{
			Action: app.MigrationDownTo, Module: name, To: 0,
		}, prefix)
		require.NoError(t, err, "roll back %s on %s", name, dialect)
	}
	assertTableExists(t, sqlDB, dialect, prefix+"iam_tenants", false)
	assertOrganizationUp(t, db, modules, prefix)
}

func execOrganizationSQL(t *testing.T, db *sql.DB, format string, args ...any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), fmt.Sprintf(format, args...))
	return err
}
