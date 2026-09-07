package cmd

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRewrapCLIConfig writes the deployment this command is meant for: browser
// authentication switched on, so the module holds a key at all, and a retired
// key still listed beside the active one, which is the state a rotation leaves
// behind and the only one in which the question has an interesting answer.
func newRewrapCLIConfig(t *testing.T) (configPath, passwordPath string) {
	t.Helper()
	directory := t.TempDir()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	// Obviously not key material, and deliberately different from the active
	// key: naming one key twice is refused at startup rather than deduplicated.
	retired := base64.RawStdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))

	configPath = filepath.Join(directory, "config.yaml")
	configData := fmt.Appendf(nil, `name: secret-rewrap-cli-test
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
  web:
    enabled: true
    post_login_url: https://app.example.test/home
    allowed_return_urls:
      - https://app.example.test/home
    allowed_origins:
      - https://app.example.test
    session_ttl: 1h
    idle_ttl: 15m
  mfa:
    encryption_key: %s
    previous_encryption_keys:
      - %s
`, filepath.ToSlash(filepath.Join(directory, "cli.sqlite")), key, key, retired)
	require.NoError(t, os.WriteFile(configPath, configData, 0o600))

	passwordPath = filepath.Join(directory, "password")
	require.NoError(t, os.WriteFile(passwordPath, []byte(bootstrapTestPassword+"\n"), 0o600))
	return configPath, passwordPath
}

// prepareRewrapCLIDatabase brings the deployment to the state the command runs
// against: migrated, with one administrator, which is also the row every
// derived digest below hangs off.
func prepareRewrapCLIDatabase(t *testing.T) string {
	t.Helper()
	configPath, passwordPath := newRewrapCLIConfig(t)
	_, err := executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	_, err = executeCommand(
		"iam", "bootstrap-platform-admin",
		"--config", configPath, "--login", "admin", "--password-file", passwordPath,
	)
	require.NoError(t, err)
	return configPath
}

// keepDryRunFromLeaking restores the survey flag after a test that sets it.
// A persistent flag stays set for the life of the process, so without this the
// first test to ask for a survey would silently turn every later rewrap in this
// package into one -- and a run that wrote nothing would still report success.
func keepDryRunFromLeaking(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		require.NoError(t, iamCmd.PersistentFlags().Set("dry-run", "false"))
	})
}

// seedDerivedDigestsCLI writes the values no rewrap can convert. They are
// one-way digests rather than ciphertext, so plain strings are exactly what
// these tables hold and no key is involved in putting them there.
// It returns the expiry it wrote so a caller can assert on that instant
// rather than on today's date: the two differ for the ninety minutes before
// midnight UTC, which used to fail this suite once a day.
func seedDerivedDigestsCLI(t *testing.T, configPath string, recoveryCodes int, verification bool) time.Time {
	t.Helper()
	db, err := (gormx.Datasource{
		Type: "sqlite", Writable: true,
		DSN: filepath.ToSlash(filepath.Join(filepath.Dir(configPath), "cli.sqlite")),
	}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	var principalID int64
	require.NoError(t, db.Raw("SELECT id FROM iam_principals LIMIT 1").Scan(&principalID).Error)
	require.NotZero(t, principalID, "the administrator bootstrap must have written a principal")

	now := time.Now().UTC()
	for index := range recoveryCodes {
		require.NoError(t, db.Exec(
			"INSERT INTO iam_recovery_codes (principal_id, code_hash, created_at) VALUES (?, ?, ?)",
			principalID, fmt.Sprintf("not-a-real-code-digest-%d", index), now,
		).Error)
	}
	if verification {
		require.NoError(t, db.Exec(
			"INSERT INTO iam_email_verification_tokens"+
				" (token_hmac, principal_id, email, created_at, expires_at, code, attempts)"+
				" VALUES (?, ?, ?, ?, ?, ?, 0)",
			"not-a-real-verification-digest", principalID, "ops@example.test",
			now, now.Add(90*time.Minute), "000000",
		).Error)
		return now.Add(90 * time.Minute)
	}
	return time.Time{}
}

// TestRewrapSecretsCommandIsWiredIntoTheIAMGroup keeps the command where an
// operator already looks, and keeps the survey a flag rather than a separate
// command: the two runs have to be the same code path, or a survey stops being
// evidence about what the real run would do.
func TestRewrapSecretsCommandIsWiredIntoTheIAMGroup(t *testing.T) {
	assert.Same(t, iamCmd, rewrapSecretsCmd.Parent())
	assert.NotNil(t, iamCmd.PersistentFlags().Lookup("dry-run"))
	assert.Contains(t, rewrapSecretsCmd.Long, "--dry-run",
		"the operator has to learn the safe first step from the command, not from the source")
	assert.Nil(t, rewrapSecretsCmd.Flags().Lookup("password"),
		"a rewrap reseals what is already stored and never handles a credential")
}

// TestRewrapSecretsCommandNamesTheModeItRanIn covers the distinction the whole
// report rests on. A survey and a rewrap print the same counts, so without the
// mode line a survey pasted into a ticket becomes proof that a rewrap ran.
func TestRewrapSecretsCommandNamesTheModeItRanIn(t *testing.T) {
	keepDryRunFromLeaking(t)
	configPath := prepareRewrapCLIDatabase(t)

	// The default first, so the safe-by-omission question is answered here: an
	// operator who forgets the flag gets the real pass, and the report says so.
	output, err := executeCommand("iam", "rewrap-secrets", "--config", configPath)
	require.NoError(t, err)
	assert.Contains(t, output, "mode: rewrap")
	assert.Contains(t, output, "rewrapped: 0")

	output, err = executeCommand("iam", "rewrap-secrets", "--config", configPath, "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, output, "mode: survey")
	assert.Contains(t, output, "retired_keys_still_needed: false")
	assert.NotContains(t, output, "clear_after:",
		"a date with nothing outstanding is a schedule for work that does not exist")
	// Every sealed column has to be named. A store that silently failed to run
	// would be indistinguishable from one with nothing left to convert.
	assert.Contains(t, output, "sealed: authn iam_credentials.totp_secret total=0")
	assert.Contains(t, output, "sealed: authn iam_passkeys.credential_ciphertext total=0")
	assert.Contains(t, output, "derived: authn iam_recovery_codes.code_hash outstanding=0")
}

// TestRewrapSecretsCommandRefusesToPrintADateThatReadsAsPermission is the line
// an operator acts on. Recovery codes never expire, so a date beside them would
// be read as "wait until then and delete the key", and the account whose codes
// were still live is the one that never comes back.
func TestRewrapSecretsCommandRefusesToPrintADateThatReadsAsPermission(t *testing.T) {
	keepDryRunFromLeaking(t)
	t.Run("a store that never expires", func(t *testing.T) {
		configPath := prepareRewrapCLIDatabase(t)
		seedDerivedDigestsCLI(t, configPath, 2, true)

		output, err := executeCommand("iam", "rewrap-secrets", "--config", configPath, "--dry-run")
		require.NoError(t, err)
		assert.Contains(t, output, "outstanding: 3")
		assert.Contains(t, output, "retired_keys_still_needed: true",
			"nothing is left to reseal, and the answer is still no")
		assert.Contains(t, output, "clear_after: never (")
		assert.Contains(t, output, "derived: authn iam_recovery_codes.code_hash outstanding=2 expires=never")
	})

	t.Run("everything outstanding expires", func(t *testing.T) {
		configPath := prepareRewrapCLIDatabase(t)
		expiry := seedDerivedDigestsCLI(t, configPath, 0, true)

		output, err := executeCommand("iam", "rewrap-secrets", "--config", configPath, "--dry-run")
		require.NoError(t, err)
		assert.Contains(t, output, "outstanding: 1")
		assert.Contains(t, output, "retired_keys_still_needed: true")
		assert.Contains(t, output, "clear_after: "+expiry.Format("2006-01-02"),
			"a store that does expire earns a date, because waiting is a real plan")
		assert.NotContains(t, output, "clear_after: never")
	})
}

// TestRewrapSecretsCommandReportsAnUnmigratedSchemaAsItsOwnStatus keeps an
// operator pointed at the missing step instead of a raw driver error, and keeps
// "no rows depend on the retired key" from being printed by a run that never
// reached the tables.
func TestRewrapSecretsCommandReportsAnUnmigratedSchemaAsItsOwnStatus(t *testing.T) {
	keepDryRunFromLeaking(t)
	configPath, _ := newRewrapCLIConfig(t)

	_, err := executeCommand("iam", "rewrap-secrets", "--config", configPath, "--dry-run")
	require.Error(t, err)
	assert.Equal(t, exitSchemaNotReady, exitCode(err))
}
