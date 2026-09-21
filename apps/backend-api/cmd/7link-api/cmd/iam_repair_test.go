package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/maintenance"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedOrphanTenantCLI writes the tenant the command exists for straight into
// the database the CLI fixture points at: a row with no member and no role, the
// shape tenants took before creation and owner bootstrap became one
// transaction. No command can produce one any more.
func seedOrphanTenantCLI(t *testing.T, configPath, slug string) {
	t.Helper()
	db, err := (gormx.Datasource{
		Type: "sqlite", Writable: true,
		DSN: filepath.ToSlash(filepath.Join(filepath.Dir(configPath), "cli.sqlite")),
	}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	now := time.Now().UTC()
	require.NoError(t, db.Exec(
		"INSERT INTO iam_tenants (id, slug, name, status, version, created_at, updated_at)"+
			" VALUES (?, ?, ?, 'active', 1, ?, ?)",
		90210, slug, "Acme", now, now,
	).Error)
}

// TestRepairTenantOwnerCommandIsWiredIntoTheIAMGroup keeps the recovery command
// discoverable where an operator already looks, and keeps its refusal documented
// on the command rather than only in the source.
func TestRepairTenantOwnerCommandIsWiredIntoTheIAMGroup(t *testing.T) {
	assert.Same(t, iamCmd, repairTenantOwnerCmd.Parent())
	assert.Contains(t, repairTenantOwnerCmd.Long, "create-only",
		"the operator has to learn the refusal from the command, not from the source")
	for _, flag := range []string{"tenant-slug", "owner-login"} {
		assert.NotNil(t, iamCmd.PersistentFlags().Lookup(flag),
			"the repair takes both of its targets as flags")
	}
	assert.Nil(t, repairTenantOwnerCmd.Flags().Lookup("password"),
		"the repair installs an existing principal and never handles a credential")
}

// TestRepairTenantOwnerCommandMakesAnUnreachableTenantAdministrable drives the
// recovery an operator actually performs: a tenant no route will admit becomes
// one somebody can sign in to and administer.
func TestRepairTenantOwnerCommandMakesAnUnreachableTenantAdministrable(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	_, err = executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.NoError(t, err)
	seedOrphanTenantCLI(t, configPath, "acme")

	output, err := executeCommand(
		"iam", "repair-tenant-owner",
		"--config", configPath, "--tenant-slug", "acme", "--owner-login", "admin",
	)
	require.NoError(t, err)
	assert.Contains(t, output, "outcome: owner-repaired")
	assert.Contains(t, output, "tenant_slug: acme")
	assert.Contains(t, output, "owner_login: admin")
	assert.Contains(t, output, "administrator_role_id: ")

	// The rerun is the interesting half: an operator unsure the first run took
	// must read a refusal, not a second owner.
	_, err = executeCommand(
		"iam", "repair-tenant-owner",
		"--config", configPath, "--tenant-slug", "acme", "--owner-login", "admin",
	)
	require.ErrorIs(t, err, maintenance.ErrTenantRepairConflict)
	assert.Equal(t, exitConflict, exitCode(err))
}

// TestRepairTenantOwnerCommandReportsAMistypedTargetWithItsOwnStatus keeps a
// deployment script from retrying a repair when the real next step is to fix
// the argument.
func TestRepairTenantOwnerCommandReportsAMistypedTargetWithItsOwnStatus(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	_, err = executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.NoError(t, err)

	_, err = executeCommand(
		"iam", "repair-tenant-owner",
		"--config", configPath, "--tenant-slug", "absent", "--owner-login", "admin",
	)
	require.ErrorIs(t, err, maintenance.ErrTenantRepairNotFound)
	assert.Equal(t, exitNotBootstrapped, exitCode(err))
}

// TestRepairTenantOwnerCommandReportsAnUnmigratedSchema keeps the operator
// pointed at the missing step rather than at a raw driver error.
func TestRepairTenantOwnerCommandReportsAnUnmigratedSchema(t *testing.T) {
	configPath, _ := newBootstrapCLIConfig(t)

	_, err := executeCommand(
		"iam", "repair-tenant-owner",
		"--config", configPath, "--tenant-slug", "acme", "--owner-login", "admin",
	)
	require.ErrorIs(t, err, maintenance.ErrPlatformAdminSchemaNotReady)
	assert.Equal(t, exitSchemaNotReady, exitCode(err))
}
