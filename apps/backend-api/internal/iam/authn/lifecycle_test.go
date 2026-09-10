package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// authnErasureTime is the instant the seeded rows carry. Erasure does not read
// it, so any fixed value keeps the fixtures readable.
var authnErasureTime = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

func TestClosureCredentialRevocationIsAtomicAndSupportsPasswordlessPrincipals(t *testing.T) {
	for _, passwordless := range []bool{false, true} {
		t.Run(map[bool]string{false: "password", true: "federated-only"}[passwordless], func(t *testing.T) {
			s, h, cookie, in := personalTokenService(t)
			principal := createdPrincipal(t, s, cookie)
			version := int64(1)
			if passwordless {
				require.NoError(t, h.db.Where("principal_id = ?", principal).Delete(&model.Credential{}).Error)
				version = 0
			}
			secret := "pat_test-closure-secret"
			row := model.PersonalToken{ID: 801, TokenHash: tokenHash(secret), PrincipalID: principal, TenantID: 71, Name: "closure", Permissions: `["store_view"]`, CredentialVersion: version, CreatedAt: repositoryTestNow, ExpiresAt: in.ExpiresAt}
			require.NoError(t, h.db.Create(&row).Error)
			challenge, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, principal.String(), "federation", "")
			require.NoError(t, err)
			h.seedPrincipal(t, 902, "unrelated")
			h.seedCredential(t, 902, "other-password-hash")
			err = h.db.Transaction(func(tx *gorm.DB) error {
				if _, err := s.repo.withDB(tx, true).lockPrincipal(t.Context(), principal); err != nil {
					return err
				}
				if err := s.revokePrincipalCredentialsOn(t.Context(), tx, principal); err != nil {
					return err
				}
				return assert.AnError
			})
			require.ErrorIs(t, err, assert.AnError)
			_, err = s.Authenticate(t.Context(), "", cookie)
			require.NoError(t, err)
			ctx := authnext.WithPersonalTokenScope(t.Context(), 71, "store_view")
			_, err = s.Authenticate(ctx, "Bearer "+secret, "")
			require.NoError(t, err)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				if _, err := s.repo.withDB(tx, true).lockPrincipal(t.Context(), principal); err != nil {
					return err
				}
				return s.revokePrincipalCredentialsOn(t.Context(), tx, principal)
			}))
			_, err = s.Authenticate(t.Context(), "", cookie)
			require.Error(t, err)
			_, err = s.Authenticate(ctx, "Bearer "+secret, "")
			require.Error(t, err)
			credential, err := s.repo.getCredential(t.Context(), principal)
			require.NoError(t, err)
			assert.Equal(t, version+1, credential.CredentialVersion)
			if passwordless {
				assert.Empty(t, credential.PasswordHash)
			} else {
				assert.NotEmpty(t, credential.PasswordHash)
			}
			other, err := s.repo.getCredential(t.Context(), 902)
			require.NoError(t, err)
			assert.EqualValues(t, 1, other.CredentialVersion)
			assert.Equal(t, "other-password-hash", other.PasswordHash)
			var proof model.ReauthenticationChallenge
			require.NoError(t, h.db.Where("id_hash = ?", tokenHash(challenge.Proof)).Take(&proof).Error)
			assert.NotNil(t, proof.ConsumedAt)
			require.Error(t, s.revokePrincipalCredentialsOn(t.Context(), nil, principal))
		})
	}
}

func TestClosureCredentialRevocationRollsBackEveryFailedWrite(t *testing.T) {
	for _, table := range []string{"iam_credentials", "iam_sessions", "iam_personal_tokens", "iam_reauth_challenges", "iam_mfa_challenges", "iam_passkey_challenges", "iam_stepup_challenges"} {
		t.Run(table, func(t *testing.T) {
			s, h, cookie, in := personalTokenService(t)
			principal := createdPrincipal(t, s, cookie)
			secret := "pat_atomic-closure-test"
			row := model.PersonalToken{ID: 801, TokenHash: tokenHash(secret), PrincipalID: principal, TenantID: 71, Name: "atomic", Permissions: `["store_view"]`, CredentialVersion: 1, CreatedAt: repositoryTestNow, ExpiresAt: in.ExpiresAt}
			require.NoError(t, h.db.Create(&row).Error)
			challenge, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, principal.String(), "federation", "")
			require.NoError(t, err)
			hit := false
			require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("closure_write_failure", func(db *gorm.DB) {
				if db.Statement.Table == table {
					hit = true
					db.AddError(assert.AnError)
				}
			}))
			err = h.db.Transaction(func(tx *gorm.DB) error {
				if _, err := s.repo.withDB(tx, true).lockPrincipal(t.Context(), principal); err != nil {
					return err
				}
				return s.revokePrincipalCredentialsOn(t.Context(), tx, principal)
			})
			require.NoError(t, h.db.Callback().Update().Remove("closure_write_failure"))
			require.True(t, hit, "the intended storage stage must execute")
			require.ErrorIs(t, err, assert.AnError)
			credential, err := s.repo.getCredential(t.Context(), principal)
			require.NoError(t, err)
			assert.EqualValues(t, 1, credential.CredentialVersion)
			_, err = s.Authenticate(t.Context(), "", cookie)
			require.NoError(t, err)
			_, err = s.Authenticate(authnext.WithPersonalTokenScope(t.Context(), 71, "store_view"), "Bearer "+secret, "")
			require.NoError(t, err)
			var proof model.ReauthenticationChallenge
			require.NoError(t, h.db.Where("id_hash = ?", tokenHash(challenge.Proof)).Take(&proof).Error)
			assert.Nil(t, proof.ConsumedAt, "failure must preserve the proof for a retry")
		})
	}
}

func TestAuthnErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")

	assert.ErrorIs(t, ErasePrincipalData(t.Context(), nil, "", 1, authnErasureTime), errRepositoryUnavailable,
		"erasure without a transaction cannot be part of the caller's atomic unit")
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", 0, authnErasureTime), errRepositoryUnavailable)
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", -1, authnErasureTime), errRepositoryUnavailable)

	// A prefix reaches the statement as an identifier, so a rejected one must
	// not become a table name.
	assert.Error(t, ErasePrincipalData(t.Context(), harness.db, "bad prefix; DROP TABLE x;--", 1, authnErasureTime))
}

func TestAuthnErasePrincipalDataRemovesSecretsAndSparesEveryoneElse(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(prefixName(prefix), func(t *testing.T) {
			harness := newAuthnRepositoryHarness(t, prefix)
			const target, bystander = guid.ID(1), guid.ID(2)
			harness.seedPrincipal(t, target, "target")
			harness.seedPrincipal(t, bystander, "bystander")
			seedAuthnPrincipalData(t, harness, prefix, target, "pending")
			seedAuthnPrincipalData(t, harness, prefix, bystander, "pending")

			require.NoError(t, ErasePrincipalData(t.Context(), harness.db, prefix, target, authnErasureTime))

			for _, table := range []string{"iam_credentials", "iam_recovery_codes", "iam_notification_outbox"} {
				assert.Zero(t, authnRowCount(t, harness, prefix, table, target),
					"%s must not keep the erased principal", table)
				assert.Equal(t, int64(1), authnRowCount(t, harness, prefix, table, bystander),
					"%s must keep an unrelated principal", table)
			}
		})
	}
}

func TestAuthnErasePrincipalDataRefusesWhileANotificationIsDelivering(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	const target = guid.ID(1)
	harness.seedPrincipal(t, target, "target")
	seedAuthnPrincipalData(t, harness, "", target, "delivering")

	// A notification handed to a sender is already outside this transaction. If
	// erasure deleted the row anyway, the recipient's address would survive in
	// the sender rather than here, which is the one place erasure cannot reach.
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", target, authnErasureTime), ErrNotificationInFlight)
	assert.Equal(t, int64(1), authnRowCount(t, harness, "", "iam_credentials", target),
		"a refused erasure must leave the principal intact")
}

func seedAuthnPrincipalData(t *testing.T, harness *authnRepositoryHarness, prefix string, id guid.ID, notificationStatus string) {
	t.Helper()
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_credentials (principal_id, password_hash, password_changed_at, updated_at) VALUES (?, ?, ?, ?)",
		id, "not-a-real-hash", authnErasureTime, authnErasureTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_recovery_codes (principal_id, code_hash, created_at) VALUES (?, ?, ?)",
		id, "not-a-real-code-digest", authnErasureTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_notification_outbox"+
			" (id, principal_id, kind, recipient, payload_ciphertext, status, available_at, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		int64(id)*100, id, "verification", "person@example.test", "not-real-ciphertext",
		notificationStatus, authnErasureTime, authnErasureTime, authnErasureTime,
	).Error)
}

func authnRowCount(t *testing.T, harness *authnRepositoryHarness, prefix, table string, id guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, harness.db.Table(prefix+table).Where("principal_id = ?", id).Count(&count).Error)
	return count
}

func prefixName(prefix string) string {
	if prefix == "" {
		return "plain"
	}
	return "prefixed"
}
