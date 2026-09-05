package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetTestPassword is the replacement the reset command installs. It differs
// from the bootstrap password so a test asserting on the outcome cannot pass by
// accident when nothing was replaced.
const resetTestPassword = "not-a-real-replacement-password"

// TestResetAdminPasswordCommandKeepsThePasswordOffTheCommandLine holds the
// reset to the same shape as the bootstrap. A recovery command is exactly the
// one an operator runs under pressure, so it must not offer a shortcut that
// writes the password into shell history.
func TestResetAdminPasswordCommandKeepsThePasswordOffTheCommandLine(t *testing.T) {
	assert.Same(t, iamCmd, resetAdminPasswordCmd.Parent())
	assert.Nil(t, resetAdminPasswordCmd.Flags().Lookup("password"),
		"the password must never be accepted as a command-line argument")
	assert.Contains(t, resetAdminPasswordCmd.Long, "does not change multi-factor enrolment",
		"the operator has to learn the limit from the command, not from the source")
}

// writeResetPasswordFile puts the replacement next to the bootstrap password
// the CLI fixture already wrote, so both runs read from the same directory an
// operator would use.
func writeResetPasswordFile(t *testing.T, alongside string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(alongside), "reset-password")
	require.NoError(t, os.WriteFile(path, []byte(resetTestPassword+"\n"), 0o600))
	return path
}

// TestResetAdminPasswordCommandReplacesTheBootstrappedPassword drives the
// recovery an operator actually performs: the administrator exists, the
// password is gone, and one command on the server makes the account usable
// again.
func TestResetAdminPasswordCommandReplacesTheBootstrappedPassword(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	_, err = executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.NoError(t, err)

	output, err := executeCommand(
		"iam", "reset-admin-password",
		"--config", configPath, "--login", "admin",
		"--password-file", writeResetPasswordFile(t, passwordPath),
	)
	require.NoError(t, err)
	assert.Contains(t, output, "outcome: password-reset")
	assert.Contains(t, output, "login: admin")
	for _, forbidden := range []string{"password:", "hash", "secret"} {
		assert.NotContains(t, output, forbidden,
			"the terminal output may be shared or logged")
	}
}

// TestResetAdminPasswordCommandReportsAnAdministratorThatWasNeverCreated keeps
// a deployment script from retrying a reset when the real next step is the
// bootstrap command.
func TestResetAdminPasswordCommandReportsAnAdministratorThatWasNeverCreated(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)

	_, err = executeCommand(
		"iam", "reset-admin-password",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.ErrorIs(t, err, app.ErrPlatformAdminNotBootstrapped)
	assert.Equal(t, exitNotBootstrapped, exitCode(err))
}

// TestResetAdminPasswordCommandReportsAnUnmigratedSchema keeps the operator
// pointed at the missing step rather than at a raw driver error.
func TestResetAdminPasswordCommandReportsAnUnmigratedSchema(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)

	_, err := executeCommand(
		"iam", "reset-admin-password",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.ErrorIs(t, err, app.ErrPlatformAdminSchemaNotReady)
	assert.Equal(t, exitSchemaNotReady, exitCode(err))
}

// TestResetAdminPasswordCommandRefusesAPasswordFileItCannotRead proves the file
// is inspected before the application is built, so a mistyped path costs
// nothing and touches no database.
func TestResetAdminPasswordCommandRefusesAPasswordFileItCannotRead(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)

	_, err := executeCommand(
		"iam", "reset-admin-password",
		"--config", configPath, "--login", "admin",
		"--password-file", filepath.Join(filepath.Dir(passwordPath), "absent"),
	)
	require.ErrorContains(t, err, "open password file")
	assert.Equal(t, exitFailure, exitCode(err))
}
