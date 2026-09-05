package authn

import (
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const resetTestPassword = "not-a-real-reset-password"

// seedLiveAuthentication puts a principal into the state a break-glass reset is
// actually run against: locked out, holding a live session and refresh token,
// and with a recovery link already sitting in a mailbox somewhere.
func seedLiveAuthentication(t *testing.T, harness *authnRepositoryHarness, principalID guid.ID) {
	t.Helper()
	prefix := harness.runtime.TablePrefix()
	lockedUntil := repositoryTestNow.Add(time.Hour)
	require.NoError(t, harness.db.Table(prefix+"iam_credentials").
		Where("principal_id = ?", principalID).
		Updates(map[string]any{"failed_attempts": 5, "locked_until": lockedUntil}).Error)

	session := model.Session{
		IDHash: "live-session", PrincipalID: principalID,
		CreatedAt: repositoryTestNow, AuthTime: repositoryTestNow, LastSeenAt: repositoryTestNow,
		ACR: 1, AMR: "pwd",
		ExpiresAt:         repositoryTestNow.Add(24 * time.Hour),
		AbsoluteExpiresAt: repositoryTestNow.Add(72 * time.Hour),
	}
	require.NoError(t, harness.db.Table(prefix+session.TableName()).Create(&session).Error)
	require.NoError(t, harness.db.Table(prefix+"iam_refresh_tokens").Create(map[string]any{
		"id_hash": "live-refresh", "principal_id": principalID, "revoked_at": nil,
	}).Error)

	token := model.PasswordRecoveryToken{
		TokenHMAC: "outstanding-recovery", PrincipalID: principalID,
		CreatedAt: repositoryTestNow, ExpiresAt: repositoryTestNow.Add(time.Hour),
	}
	require.NoError(t, harness.db.Table(prefix+token.TableName()).Create(&token).Error)
}

// countUnrevoked reports how many rows in table are still usable by the
// principal, which is what ending every session has to mean in storage.
func countUnrevoked(t *testing.T, harness *authnRepositoryHarness, table, column string, principalID guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+table).
		Where("principal_id = ? AND "+column+" IS NULL", principalID).Count(&count).Error)
	return count
}

// seedResettableAdministrator creates the principal and its first credential
// through the same capability the bootstrap command uses, so what these tests
// reset is what the bootstrap actually wrote.
func seedResettableAdministrator(t *testing.T, harness *authnRepositoryHarness, principalID guid.ID) {
	t.Helper()
	harness.seedPrincipal(t, principalID, "admin")
	require.NoError(t, harness.repository.CreateBootstrapCredentialOn(
		t.Context(), harness.db, principalID, bootstrapTestPassword, repositoryTestNow))
}

// TestResetBootstrapCredentialRotatesTheCredentialAndEndsEveryLiveGrant is the
// whole reason the reset is not just an update: an operator runs it because the
// stored password is no longer trusted, so everything issued against it has to
// stop working in the same transaction.
func TestResetBootstrapCredentialRotatesTheCredentialAndEndsEveryLiveGrant(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credreset_")
	const principalID guid.ID = 4200
	seedResettableAdministrator(t, harness, principalID)
	seedLiveAuthentication(t, harness, principalID)

	now := repositoryTestNow.Add(2 * time.Hour)
	replaced, err := harness.repository.ResetBootstrapCredentialOn(
		t.Context(), harness.db, principalID, resetTestPassword, now)
	require.NoError(t, err)
	assert.True(t, replaced, "an administrator that held a credential reports a replacement, not a repair")

	credential, err := harness.repository.withDB(harness.db, true).getCredential(t.Context(), principalID)
	require.NoError(t, err)
	assert.NotContains(t, credential.PasswordHash, resetTestPassword, "the new password must not be recoverable from storage")
	matched, err := passwordx.Verify(credential.PasswordHash, resetTestPassword)
	require.NoError(t, err)
	assert.True(t, matched, "the operator must be able to sign in with what was supplied")
	stale, err := passwordx.Verify(credential.PasswordHash, bootstrapTestPassword)
	require.NoError(t, err)
	assert.False(t, stale, "the password the reset replaced must stop working")

	assert.EqualValues(t, 2, credential.CredentialVersion,
		"the version bump is what invalidates tokens minted against the old credential")
	assert.Zero(t, credential.FailedAttempts)
	assert.Nil(t, credential.LockedUntil, "a locked account is exactly the case the reset exists for")
	assert.Equal(t, now, credential.PasswordChangedAt.UTC())

	var history []model.PasswordHistory
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_password_history").
		Where("principal_id = ?", principalID).Find(&history).Error)
	require.Len(t, history, 1, "the superseded hash is kept so the reuse rule still applies to it")
	superseded, err := passwordx.Verify(history[0].PasswordHash, bootstrapTestPassword)
	require.NoError(t, err)
	assert.True(t, superseded)

	assert.Zero(t, countUnrevoked(t, harness, "iam_sessions", "revoked_at", principalID),
		"a session opened with the old password must not survive the reset")
	assert.Zero(t, countUnrevoked(t, harness, "iam_refresh_tokens", "revoked_at", principalID))
	assert.Zero(t, countUnrevoked(t, harness, "iam_password_recovery_tokens", "consumed_at", principalID),
		"a recovery link already sent must not still hand out the account afterwards")
}

// TestResetBootstrapCredentialReportsAnAdministratorHoldingNoCredential keeps
// the missing-credential case from reading as a failure. It is a real state the
// bootstrap refuses to touch, and the caller repairs it rather than aborting.
func TestResetBootstrapCredentialReportsAnAdministratorHoldingNoCredential(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credresetnone_")
	const principalID guid.ID = 4300
	harness.seedPrincipal(t, principalID, "admin")

	replaced, err := harness.repository.ResetBootstrapCredentialOn(
		t.Context(), harness.db, principalID, resetTestPassword, repositoryTestNow)
	require.NoError(t, err, "an absent credential is a state to report, not an error to raise")
	assert.False(t, replaced)

	exists, err := harness.repository.HasCredentialOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.False(t, exists, "reporting the absence must not have written a credential on the way")
}

// TestResetBootstrapCredentialRefusesInputItCannotHonor keeps a mistyped
// invocation from reaching storage at all, and keeps the rejection from quoting
// the value it rejected.
func TestResetBootstrapCredentialRefusesInputItCannotHonor(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credresetargs_")
	var missing *Repository

	for name, call := range map[string]func() (bool, error){
		"without repository": func() (bool, error) {
			return missing.ResetBootstrapCredentialOn(t.Context(), harness.db, 1, resetTestPassword, repositoryTestNow)
		},
		"without transaction": func() (bool, error) {
			return harness.repository.ResetBootstrapCredentialOn(t.Context(), nil, 1, resetTestPassword, repositoryTestNow)
		},
		"without principal": func() (bool, error) {
			return harness.repository.ResetBootstrapCredentialOn(t.Context(), harness.db, 0, resetTestPassword, repositoryTestNow)
		},
	} {
		t.Run(name, func(t *testing.T) {
			replaced, err := call()
			assert.Error(t, err)
			assert.False(t, replaced)
		})
	}

	replaced, err := harness.repository.ResetBootstrapCredentialOn(
		t.Context(), harness.db, 1, "short", repositoryTestNow)
	assert.ErrorIs(t, err, ErrBootstrapPassword, "the reset holds the same password rule the bootstrap does")
	assert.False(t, replaced)
	assert.NotContains(t, err.Error(), "short", "the rejection must not quote the value")
}

// TestResetBootstrapCredentialSurfacesStorageFailures walks each write the
// reset depends on. Every one of them has to abort the reset, because a partial
// reset is the state that leaves an operator locked out believing otherwise.
func TestResetBootstrapCredentialSurfacesStorageFailures(t *testing.T) {
	for index, table := range []string{
		"iam_credentials", "iam_password_history", "iam_sessions", "iam_password_recovery_tokens",
	} {
		t.Run(table, func(t *testing.T) {
			// Goose bounds a table prefix at 21 characters, so the per-case
			// schema is numbered rather than named after the dropped table.
			harness := newAuthnRepositoryHarness(t, fmt.Sprintf("credresetbroken%d_", index))
			const principalID guid.ID = 4400
			seedResettableAdministrator(t, harness, principalID)
			require.NoError(t, harness.db.Exec("DROP TABLE "+harness.runtime.TablePrefix()+table).Error)

			replaced, err := harness.repository.ResetBootstrapCredentialOn(
				t.Context(), harness.db, principalID, resetTestPassword, repositoryTestNow)
			assert.Error(t, err, "an unwritable %s must not be reported as a completed reset", table)
			assert.False(t, replaced)
		})
	}
}

// TestResetBootstrapCredentialRefusesWhenTheRowVanishesUnderIt covers the race
// the reset cannot repair: the credential was read, then stopped existing
// before the replacement landed. Reporting success there would tell an operator
// a password works when nothing stores it.
func TestResetBootstrapCredentialRefusesWhenTheRowVanishesUnderIt(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credresetlost_")
	const principalID guid.ID = 4500
	seedResettableAdministrator(t, harness, principalID)

	// RAISE(IGNORE) drops the row from the statement without failing it, which
	// is the one way to reproduce a zero-row update that is not an error.
	require.NoError(t, harness.db.Exec(
		"CREATE TRIGGER skip_credential_update BEFORE UPDATE ON "+
			harness.runtime.TablePrefix()+"iam_credentials BEGIN SELECT RAISE(IGNORE); END",
	).Error)

	replaced, err := harness.repository.ResetBootstrapCredentialOn(
		t.Context(), harness.db, principalID, resetTestPassword, repositoryTestNow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disappeared during reset")
	assert.False(t, replaced)
}

// TestBootstrapCredentialStoreResetReportsUnavailableBeforeWiring covers the
// guard inside the closure, which is handed out during ProvideServices and so
// can be called on a module whose collaborators never arrived.
func TestBootstrapCredentialStoreResetReportsUnavailableBeforeWiring(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credresetunwired_")
	for name, unwired := range map[string]*Module{
		"nil module":        nil,
		"no runtime":        {ids: newRepositoryTestIDs(1), appender: harness.repository.audit},
		"no id generator":   {runtime: harness.runtime, appender: harness.repository.audit},
		"no audit appender": {runtime: harness.runtime, ids: newRepositoryTestIDs(1)},
	} {
		t.Run(name, func(t *testing.T) {
			replaced, err := unwired.bootstrapCredentialStore().Reset(
				t.Context(), harness.db, 1, resetTestPassword, repositoryTestNow)
			assert.ErrorIs(t, err, authnext.ErrUnavailable)
			assert.False(t, replaced)
		})
	}
}

// TestBootstrapCredentialStoreResetDelegatesToTheWiredRepository proves the
// capability the command resolves is the same write path exercised above.
func TestBootstrapCredentialStoreResetDelegatesToTheWiredRepository(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "credresetwired_")
	const principalID guid.ID = 4600
	seedResettableAdministrator(t, harness, principalID)
	store := (&Module{
		runtime:  harness.runtime,
		ids:      newRepositoryTestIDs(9200),
		appender: harness.repository.audit,
	}).bootstrapCredentialStore()

	replaced, err := store.Reset(t.Context(), harness.db, principalID, resetTestPassword, repositoryTestNow)
	require.NoError(t, err)
	assert.True(t, replaced)

	credential, err := harness.repository.withDB(harness.db, true).getCredential(t.Context(), principalID)
	require.NoError(t, err)
	matched, err := passwordx.Verify(credential.PasswordHash, resetTestPassword)
	require.NoError(t, err)
	assert.True(t, matched)
}
