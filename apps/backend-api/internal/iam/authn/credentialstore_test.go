package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// credentialStoreFixture wires the module far enough to build the capability:
// the closures construct their repository at call time from what Wire supplies,
// so the module needs its collaborators but not a Service.
func credentialStoreFixture(t *testing.T) (*authnRepositoryHarness, identity.PrincipalCredentialStore) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "sl_")
	instance := NewModule(harness.runtime, authnModuleConfig(localAuthnConfig()))
	instance.ids = newRepositoryTestIDs(7000)
	instance.appender = harness.repository.audit
	return harness, instance.principalCredentialStore()
}

func (h *authnRepositoryHarness) countWhere(t *testing.T, table, condition string, args ...any) int64 {
	t.Helper()
	var total int64
	require.NoError(t, h.db.Table(h.runtime.TablePrefix()+table).Where(condition, args...).Count(&total).Error)
	return total
}

func seedCredentialTokens(t *testing.T, harness *authnRepositoryHarness, principalID guid.ID) {
	t.Helper()
	require.NoError(t, harness.db.Table("sl_iam_password_recovery_tokens").
		Create(&model.PasswordRecoveryToken{
			PrincipalID: principalID, TokenHMAC: "recovery-hash",
			ExpiresAt: repositoryTestNow.Add(time.Hour), CreatedAt: repositoryTestNow,
		}).Error)
	require.NoError(t, harness.db.Table("sl_iam_email_verification_tokens").
		Create(&model.EmailVerificationToken{
			PrincipalID: principalID, Email: "alice@example.test", TokenHMAC: "verify-hash",
			ExpiresAt: repositoryTestNow.Add(time.Hour), CreatedAt: repositoryTestNow,
		}).Error)
}

func seedLiveSession(t *testing.T, harness *authnRepositoryHarness, principalID guid.ID) {
	t.Helper()
	require.NoError(t, harness.db.Table("sl_iam_sessions").Create(&model.Session{
		IDHash: "session-hash", PrincipalID: principalID, CreatedAt: repositoryTestNow,
		AuthTime: repositoryTestNow, LastSeenAt: repositoryTestNow,
		ExpiresAt:         repositoryTestNow.Add(time.Hour),
		AbsoluteExpiresAt: repositoryTestNow.Add(24 * time.Hour),
	}).Error)
}

// TestPrincipalCredentialStoreWritesWhatItOwns covers the effect of each
// closure. The statements live in authn because authn owns these tables;
// identity only decides when they run.
func TestPrincipalCredentialStoreWritesWhatItOwns(t *testing.T) {
	harness, store := credentialStoreFixture(t)
	harness.seedPrincipal(t, 1, "alice")
	ctx := t.Context()

	require.NoError(t, store.CreateHashed(ctx, harness.db, 1, "argon2-hash", repositoryTestNow))
	credential, err := harness.repository.getCredential(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "argon2-hash", credential.PasswordHash)
	assert.EqualValues(t, 1, credential.CredentialVersion)

	found, err := store.IncrementVersion(ctx, harness.db, 1, repositoryTestNow.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, found)
	credential, err = harness.repository.getCredential(ctx, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, credential.CredentialVersion)

	// A principal with no credential at all is a state identity distinguishes:
	// a federation-only principal legitimately has none.
	harness.seedPrincipal(t, 2, "federated")
	found, err = store.IncrementVersion(ctx, harness.db, 2, repositoryTestNow)
	require.NoError(t, err)
	assert.False(t, found)

	seedLiveSession(t, harness, 1)
	require.NoError(t, store.RevokeSessions(ctx, harness.db, 1, repositoryTestNow))
	assert.EqualValues(t, 0,
		harness.countWhere(t, "iam_sessions", "principal_id = ? AND revoked_at IS NULL", 1))

	seedCredentialTokens(t, harness, 1)
	require.NoError(t, store.ConsumeCredentialTokens(ctx, harness.db, 1, repositoryTestNow))
	for _, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		assert.EqualValues(t, 0,
			harness.countWhere(t, table, "principal_id = ? AND consumed_at IS NULL", 1), table)
	}
}

// TestPrincipalCredentialStoreStaysInTheCallersTransaction is the property the
// capability exists to keep. identity calls these while holding a principal
// lock; a closure that opened its own connection would leave the credential
// tables written after identity rolled the rest of the closure back, and no
// assertion about identity alone could see it.
func TestPrincipalCredentialStoreStaysInTheCallersTransaction(t *testing.T) {
	harness, store := credentialStoreFixture(t)
	harness.seedPrincipal(t, 1, "alice")
	harness.seedCredential(t, 1, "original-hash")
	seedLiveSession(t, harness, 1)
	seedCredentialTokens(t, harness, 1)

	abandoned := assert.AnError
	err := harness.db.Transaction(func(tx *gorm.DB) error {
		found, err := store.IncrementVersion(t.Context(), tx, 1, repositoryTestNow)
		require.NoError(t, err)
		require.True(t, found)
		require.NoError(t, store.RevokeSessions(t.Context(), tx, 1, repositoryTestNow))
		require.NoError(t, store.ConsumeCredentialTokens(t.Context(), tx, 1, repositoryTestNow))
		return abandoned
	})
	require.ErrorIs(t, err, abandoned)

	credential, err := harness.repository.getCredential(t.Context(), 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, credential.CredentialVersion, "a rolled back bump must not stick")
	assert.EqualValues(t, 1,
		harness.countWhere(t, "iam_sessions", "principal_id = ? AND revoked_at IS NULL", 1),
		"a rolled back revocation must leave the session live")
	for _, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		assert.EqualValues(t, 1,
			harness.countWhere(t, table, "principal_id = ? AND consumed_at IS NULL", 1), table)
	}
}

// TestPrincipalCredentialStoreRefusesWithoutATransactionOrWiring keeps the two
// ways a caller can misuse it explicit: no handle to run on, and a module Wire
// never supplied.
func TestPrincipalCredentialStoreRefusesWithoutATransactionOrWiring(t *testing.T) {
	harness, store := credentialStoreFixture(t)
	ctx := t.Context()

	for name, call := range map[string]func(identity.PrincipalCredentialStore, *gorm.DB) error{
		"create": func(s identity.PrincipalCredentialStore, tx *gorm.DB) error {
			return s.CreateHashed(ctx, tx, 1, "hash", repositoryTestNow)
		},
		"increment": func(s identity.PrincipalCredentialStore, tx *gorm.DB) error {
			_, err := s.IncrementVersion(ctx, tx, 1, repositoryTestNow)
			return err
		},
		"revoke sessions": func(s identity.PrincipalCredentialStore, tx *gorm.DB) error {
			return s.RevokeSessions(ctx, tx, 1, repositoryTestNow)
		},
		"consume tokens": func(s identity.PrincipalCredentialStore, tx *gorm.DB) error {
			return s.ConsumeCredentialTokens(ctx, tx, 1, repositoryTestNow)
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, call(store, nil), authnext.ErrUnavailable,
				"a capability with nowhere to run must refuse rather than open its own connection")

			unwired := NewModule(
				appenv.NewRuntime(appenv.NewIdentity("authn-test", "1")),
				authnModuleConfig(localAuthnConfig()),
			).principalCredentialStore()
			assert.ErrorIs(t, call(unwired, harness.db), authnext.ErrUnavailable)
		})
	}
}
