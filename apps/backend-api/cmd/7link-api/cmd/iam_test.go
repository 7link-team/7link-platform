package cmd

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"7link.com/platform/internal/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootstrapTestPassword is long enough to clear the store's minimum and is
// obviously not a credential anyone deploys.
const bootstrapTestPassword = "not-a-real-bootstrap-password"

// TestIamCommandKeepsThePasswordOffTheCommandLine pins the security property
// the whole command shape exists for: process arguments are readable by every
// local user and shell history keeps them, so there must be no way to pass the
// password as one.
func TestIamCommandKeepsThePasswordOffTheCommandLine(t *testing.T) {
	assert.Same(t, rootCmd, iamCmd.Parent())
	assert.Same(t, iamCmd, bootstrapPlatformAdminCmd.Parent())
	assert.NotNil(t, iamCmd.PersistentFlags().Lookup("config"))
	assert.NotNil(t, iamCmd.PersistentFlags().Lookup("login"))
	assert.NotNil(t, iamCmd.PersistentFlags().Lookup("password-file"))
	assert.Nil(t, iamCmd.PersistentFlags().Lookup("password"),
		"the password must never be accepted as a command-line argument")
	assert.Nil(t, bootstrapPlatformAdminCmd.Flags().Lookup("password"))
}

func newPasswordCommand(stdin string) *cobra.Command {
	command := &cobra.Command{}
	command.SetIn(strings.NewReader(stdin))
	return command
}

func writePasswordFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

// TestReadBootstrapPasswordAcceptsBothOperatorChannels covers the two supported
// ways in, and pins that a file and standard input yield the same value.
func TestReadBootstrapPasswordAcceptsBothOperatorChannels(t *testing.T) {
	fromStdin, err := readBootstrapPassword(newPasswordCommand(bootstrapTestPassword+"\n"), "")
	require.NoError(t, err)
	assert.Equal(t, bootstrapTestPassword, fromStdin)

	path := writePasswordFile(t, bootstrapTestPassword+"\n")
	fromFile, err := readBootstrapPassword(newPasswordCommand(""), path)
	require.NoError(t, err)
	assert.Equal(t, fromStdin, fromFile)

	// A path made only of spaces is an empty setting, not a filename.
	fromBlankPath, err := readBootstrapPassword(newPasswordCommand(bootstrapTestPassword), "   ")
	require.NoError(t, err)
	assert.Equal(t, bootstrapTestPassword, fromBlankPath)
}

// TestTrimPasswordNewlineRemovesTheEditorsLineBreakAndNothingElse is the whole
// contract of the helper: an ordinary editor appends a line break the operator
// never typed, but every other byte may genuinely be part of the password.
func TestTrimPasswordNewlineRemovesTheEditorsLineBreakAndNothingElse(t *testing.T) {
	for name, testCase := range map[string]struct{ raw, want string }{
		"unix line break":       {"secret\n", "secret"},
		"windows line break":    {"secret\r\n", "secret"},
		"no line break":         {"secret", "secret"},
		"only the last break":   {"secret\n\n", "secret\n"},
		"surrounding spaces":    {"  secret  \n", "  secret  "},
		"internal line break":   {"one\ntwo\n", "one\ntwo"},
		"lone carriage return":  {"secret\r", "secret"},
		"empty":                 {"", ""},
		"break is the whole of": {"\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			password, err := trimPasswordNewline(testCase.raw)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, password)
		})
	}
}

// TestReadBootstrapPasswordBoundsWhatItWillRead keeps a pointed-at log or
// database file failing as the mistake it is instead of streaming into memory.
func TestReadBootstrapPasswordBoundsWhatItWillRead(t *testing.T) {
	atLimit := strings.Repeat("p", maxPasswordFileSize)
	password, err := readBootstrapPassword(newPasswordCommand(""), writePasswordFile(t, atLimit))
	require.NoError(t, err)
	assert.Len(t, password, maxPasswordFileSize)

	overLimit := strings.Repeat("p", maxPasswordFileSize+1)
	_, err = readBootstrapPassword(newPasswordCommand(""), writePasswordFile(t, overLimit))
	require.ErrorContains(t, err, "exceeds")
	assert.NotContains(t, err.Error(), overLimit, "the rejection must not echo the value")

	_, err = readBootstrapPassword(newPasswordCommand(overLimit), "")
	assert.ErrorContains(t, err, "exceeds")
}

// TestReadBootstrapPasswordRejectsSourcesItCannotTrust names each refusal, so
// an operator who mistypes a path is told which mistake they made.
func TestReadBootstrapPasswordRejectsSourcesItCannotTrust(t *testing.T) {
	directory := t.TempDir()

	_, err := readBootstrapPassword(newPasswordCommand(""), filepath.Join(directory, "missing"))
	assert.ErrorContains(t, err, "open password file")

	_, err = readBootstrapPassword(newPasswordCommand(""), directory)
	assert.ErrorContains(t, err, "is not a regular file")
}

// TestCheckPasswordFileModeRejectsWhatOtherLocalUsersCanRead is a permission
// gate, not a formality: a 0644 password file hands the platform administrator
// credential to every account on the host.
func TestCheckPasswordFileModeRejectsWhatOtherLocalUsersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Go synthesizes a permission mode on Windows rather than reporting the
		// access-control list, so the check would reject every file it saw.
		assert.NoError(t, checkPasswordFileMode("pw", 0o666))
		return
	}
	assert.NoError(t, checkPasswordFileMode("pw", 0o600))
	assert.NoError(t, checkPasswordFileMode("pw", 0o400))

	for name, mode := range map[string]os.FileMode{
		"group readable": 0o640,
		"other readable": 0o604,
		"world readable": 0o644,
	} {
		t.Run(name, func(t *testing.T) {
			err := checkPasswordFileMode("pw", mode)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "0600", "the message must say what to do about it")
		})
	}
}

// TestBootstrapExitCodeNamesADistinctOperatorResponse is the command's scripted
// contract. Each status calls for a different next step, so a deployment step
// must not have to parse stderr to tell them apart.
func TestBootstrapExitCodeNamesADistinctOperatorResponse(t *testing.T) {
	for name, testCase := range map[string]struct {
		err  error
		want int
	}{
		"administrator not bootstrapped": {app.ErrPlatformAdminNotBootstrapped, exitNotBootstrapped},
		"conflicting administrator":      {app.ErrPlatformAdminConflict, exitConflict},
		"schema not migrated":            {app.ErrPlatformAdminSchemaNotReady, exitSchemaNotReady},
		"transient unavailability":       {app.ErrPlatformAdminUnavailable, exitTransient},
		"anything else":                  {errors.New("boom"), exitFailure},
		"wrapped conflict": {
			fmt.Errorf("bootstrap: %w", app.ErrPlatformAdminConflict), exitConflict,
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.want, platformAdminExitCode(testCase.err))
			assert.Equal(t, testCase.want, exitCode(withExitCode(platformAdminExitCode(testCase.err), testCase.err)),
				"the status must survive the trip out through RunE")
		})
	}

	assert.NotEqual(t, exitAlreadyBootstrapped, exitConflict)
	assert.NotEqual(t, exitSchemaNotReady, exitTransient)
	assert.NotEqual(t, exitNotBootstrapped, exitConflict,
		"bootstrap first and fix the state are different next steps")
}

// TestWriteBootstrapReportCarriesNoSecretMaterial guards the one place the
// command writes to a terminal an operator may be sharing or logging.
func TestWriteBootstrapReportCarriesNoSecretMaterial(t *testing.T) {
	output := &bytes.Buffer{}
	writeBootstrapReport(output, app.PlatformAdminReport{
		Outcome: app.PlatformAdminCreated, PrincipalID: 8100, Login: "admin",
	})

	rendered := output.String()
	assert.Contains(t, rendered, "outcome: created")
	assert.Contains(t, rendered, "principal_id: 8100")
	assert.Contains(t, rendered, "login: admin")
	for _, forbidden := range []string{"password", "hash", "secret", "credential"} {
		assert.NotContains(t, rendered, forbidden)
	}
}

// newBootstrapCLIConfig writes the configuration the real command reads. The
// keys are deterministic and obviously synthetic; nothing here is deployable.
func newBootstrapCLIConfig(t *testing.T) (configPath, passwordPath string) {
	t.Helper()
	directory := t.TempDir()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)

	configPath = filepath.Join(directory, "config.yaml")
	configData := fmt.Appendf(nil, `name: platform-admin-cli-test
database:
  main:
    type: sqlite
    dsn: %s
    writable: true
    readable: true
authn:
  enabled: true
  issuer: https://iam.example.test
  access_token_ttl: 1h
  signing_key: %s
  mfa:
    encryption_key: %s
`, filepath.ToSlash(filepath.Join(directory, "cli.sqlite")), key, key)
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	passwordPath = filepath.Join(directory, "password")
	require.NoError(t, os.WriteFile(passwordPath, []byte(bootstrapTestPassword+"\n"), 0o600))
	return configPath, passwordPath
}

// TestBootstrapPlatformAdminCommandCreatesThenReportsAlreadyBootstrapped drives
// the command the way a deployment script does. The rerun is the interesting
// half: an idempotent step has to be able to run twice without a second
// administrator appearing and without reading a failure.
func TestBootstrapPlatformAdminCommandCreatesThenReportsAlreadyBootstrapped(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)

	arguments := []string{
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	}
	output, err := executeCommand(arguments...)
	require.NoError(t, err)
	assert.Contains(t, output, "outcome: created")
	assert.Contains(t, output, "login: admin")

	output, err = executeCommand(arguments...)
	require.Error(t, err)
	assert.ErrorIs(t, err, errAlreadyBootstrapped)
	assert.Equal(t, exitAlreadyBootstrapped, exitCode(err),
		"a rerun is a distinct status, not an ordinary failure")
	assert.Contains(t, output, "outcome: already-bootstrapped")
}

// TestBootstrapPlatformAdminCommandReportsAnUnmigratedSchema keeps the operator
// pointed at the missing step rather than at a raw driver error.
func TestBootstrapPlatformAdminCommandReportsAnUnmigratedSchema(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)

	_, err := executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, app.ErrPlatformAdminSchemaNotReady)
	assert.Equal(t, exitSchemaNotReady, exitCode(err))
}

// TestBootstrapPlatformAdminCommandRefusesAPasswordFileItCannotRead proves the
// file is inspected before the application is built, so a mistyped path costs
// nothing and touches no database.
func TestBootstrapPlatformAdminCommandRefusesAPasswordFileItCannotRead(t *testing.T) {
	configPath, passwordPath := newBootstrapCLIConfig(t)

	_, err := executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin",
		"--password-file", filepath.Join(filepath.Dir(passwordPath), "absent"),
	)
	require.ErrorContains(t, err, "open password file")
	assert.Equal(t, exitFailure, exitCode(err))
}

// TestIamCommandWithoutASubcommandShowsItsHelp keeps the group command from
// looking like a no-op success.
func TestIamCommandWithoutASubcommandShowsItsHelp(t *testing.T) {
	output, err := executeCommand("iam")
	require.NoError(t, err)
	assert.Contains(t, output, "bootstrap-platform-admin")
	assert.Contains(t, output, "reset-admin-password")
}
