package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// None of the values below could unlock anything: a test that carried a
// plausible authenticator secret or passkey would be a credential in the
// repository, which is the thing this module exists to avoid.
const (
	notARealEnrollmentSecret = "not-a-real-enrollment-secret"
	notARealPasskeyIDHash    = "not-a-real-passkey-id-hash"
	notARealCredentialBlob   = "not-a-real-webauthn-credential"
	notARealOutboxPayload    = "not-a-real-notification-payload"
)

// rewrapFixture is the state a deployment is left in when it adds a replacement
// key and leaves the old one listed: every sealed column still opens, and
// nothing has said which rows are the reason the old key cannot come out.
type rewrapFixture struct {
	service     *Service
	harness     *authnRepositoryHarness
	principalID guid.ID
	outboxID    guid.ID
	lastExpiry  time.Time
}

// newRewrapFixture seals all four columns under the key that is about to be
// retired, then rotates the ring underneath them. The order matters: sealing
// after the rotation would produce rows already on the active key, and a test
// where there is nothing to convert cannot tell a working rewrap from one that
// does nothing at all.
func newRewrapFixture(t *testing.T) *rewrapFixture {
	t.Helper()
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "Admin", "correct horse battery staple")
	fixture := &rewrapFixture{
		service: service, harness: harness, principalID: principalID, outboxID: guid.ID(4242),
	}

	totpSecret, err := service.encryptMFASecret(principalID, rotationTestPlaintextSecret)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table("iam_credentials").
		Where("principal_id = ?", principalID).
		UpdateColumn("totp_secret", totpSecret).Error)

	enrollment, err := service.encryptMFASecret(principalID, notARealEnrollmentSecret)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.MFAEnrollment{
		PrincipalID: principalID, SecretCiphertext: enrollment,
		CreatedAt: repositoryTestNow, ExpiresAt: repositoryTestNow.Add(10 * time.Minute),
	}).Error)

	credential, err := service.encryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, notARealPasskeyIDHash), []byte(notARealCredentialBlob),
	)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.Passkey{
		IDHash: notARealPasskeyIDHash, PrincipalID: principalID, Name: "Laptop",
		CredentialCiphertext: credential, CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)

	payload, err := service.encryptAuthnData(
		notificationCipher, fixture.outboxID.String(), []byte(notARealOutboxPayload),
	)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.NotificationOutbox{
		ID: fixture.outboxID, Kind: "password-recovery", Recipient: "admin@example.test",
		PayloadCiphertext: payload, Status: "pending", AvailableAt: repositoryTestNow,
		CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)

	fixture.seedDerived(t)
	service.mfaKeys = newMFATestKeyring(t, replacementAuthnKey(), retiredAuthnKey())
	return fixture
}

// seedDerived plants the rows a rewrap can only count. Each store gets one row
// that no longer matters -- used, consumed, expired -- because a report that
// counted those would tell an operator to keep a key alive for digests nothing
// will ever present again.
func (f *rewrapFixture) seedDerived(t *testing.T) {
	t.Helper()
	used := repositoryTestNow
	// The derived stores are counted against the wall clock rather than the
	// service's frozen one, so their expiries have to be real offsets from now.
	now := time.Now().UTC()
	f.lastExpiry = now.Add(2 * time.Hour)

	for index, code := range []model.RecoveryCode{
		{CodeHash: "not-a-real-recovery-code-hash-1"},
		{CodeHash: "not-a-real-recovery-code-hash-2"},
		{CodeHash: "not-a-real-recovery-code-hash-3", UsedAt: &used},
	} {
		code.PrincipalID = f.principalID
		code.CreatedAt = repositoryTestNow.Add(time.Duration(index) * time.Second)
		require.NoError(t, f.harness.db.Create(&code).Error)
	}

	for _, token := range []model.PasswordRecoveryToken{
		{TokenHMAC: "not-a-real-recovery-token-hmac-1", ExpiresAt: now.Add(time.Hour)},
		{TokenHMAC: "not-a-real-recovery-token-hmac-2", ExpiresAt: f.lastExpiry},
		{TokenHMAC: "not-a-real-recovery-token-hmac-3", ExpiresAt: now.Add(-time.Hour)},
		{TokenHMAC: "not-a-real-recovery-token-hmac-4", ExpiresAt: now.Add(time.Hour), ConsumedAt: &used},
	} {
		token.PrincipalID = f.principalID
		token.CreatedAt = repositoryTestNow
		require.NoError(t, f.harness.db.Create(&token).Error)
	}

	require.NoError(t, f.harness.db.Create(&model.EmailVerificationToken{
		TokenHMAC: "not-a-real-verification-token-hmac", PrincipalID: f.principalID,
		Email: "admin@example.test", CreatedAt: repositoryTestNow,
		ExpiresAt: now.Add(30 * time.Minute), Code: "000000",
	}).Error)
}

func (f *rewrapFixture) rewrap(t *testing.T, mode keyringx.RewrapMode) keyringx.RewrapResult {
	t.Helper()
	result, err := (&Module{Service: f.service}).RewrapSecrets(t.Context(), f.harness.db, mode)
	require.NoError(t, err)
	return result
}

func (f *rewrapFixture) storedTOTPSecret(t *testing.T) string {
	t.Helper()
	var row model.Credential
	require.NoError(t, f.harness.db.Where("principal_id = ?", f.principalID).First(&row).Error)
	return row.TOTPSecret
}

// TestAuthnRewrapSurveysEverySealedColumnAndWritesNothing covers the question an
// operator has before they touch anything: what would break if the retired key
// came out today. Every column has to appear, because one that is silently
// skipped reads as one that has nothing to convert.
func TestAuthnRewrapSurveysEverySealedColumnAndWritesNothing(t *testing.T) {
	fixture := newRewrapFixture(t)
	before := fixture.storedTOTPSecret(t)

	survey := fixture.rewrap(t, keyringx.SurveyOnly)

	require.Len(t, survey.Sealed, 4)
	assert.Equal(t, []string{
		"iam_credentials.totp_secret",
		"iam_mfa_enrollments.secret_ciphertext",
		"iam_passkeys.credential_ciphertext",
		"iam_notification_outbox.payload_ciphertext",
	}, []string{
		survey.Sealed[0].Store, survey.Sealed[1].Store,
		survey.Sealed[2].Store, survey.Sealed[3].Store,
	})
	for _, sealed := range survey.Sealed {
		assert.Equal(t, int64(1), sealed.Retired, sealed.Store)
		assert.Equal(t, int64(0), sealed.Rewrapped, sealed.Store)
	}
	assert.Equal(t, int64(4), survey.Pending())
	assert.Equal(t, int64(0), survey.Unreadable())
	assert.Equal(t, before, fixture.storedTOTPSecret(t), "a survey that writes is not a survey")
}

// TestAuthnRewrapMovesEverySealedColumnOntoTheActiveKey is the pass that lets
// the answer become yes. Reading each value back afterwards is the whole point:
// a rewrap that resealed under different associated data than the reading path
// uses would report success and lock every one of these people out.
func TestAuthnRewrapMovesEverySealedColumnOntoTheActiveKey(t *testing.T) {
	fixture := newRewrapFixture(t)

	result := fixture.rewrap(t, keyringx.RewrapStale)

	require.Len(t, result.Sealed, 4)
	for _, sealed := range result.Sealed {
		assert.Equal(t, int64(1), sealed.Rewrapped, sealed.Store)
		assert.Equal(t, int64(0), sealed.Skipped, sealed.Store)
	}
	assert.Equal(t, int64(0), result.Pending())

	secret, err := fixture.service.decryptMFASecret(fixture.principalID, fixture.storedTOTPSecret(t))
	require.NoError(t, err)
	assert.Equal(t, rotationTestPlaintextSecret, secret)

	var enrollment model.MFAEnrollment
	require.NoError(t, fixture.harness.db.
		Where("principal_id = ?", fixture.principalID).First(&enrollment).Error)
	opened, err := fixture.service.decryptMFASecret(fixture.principalID, enrollment.SecretCiphertext)
	require.NoError(t, err)
	assert.Equal(t, notARealEnrollmentSecret, opened)

	var passkey model.Passkey
	require.NoError(t, fixture.harness.db.
		Where("id_hash = ?", notARealPasskeyIDHash).First(&passkey).Error)
	credential, err := fixture.service.decryptAuthnData(
		passkeyCipher, passkeyAAD(fixture.principalID, notARealPasskeyIDHash),
		passkey.CredentialCiphertext,
	)
	require.NoError(t, err)
	assert.Equal(t, notARealCredentialBlob, string(credential))

	var outbox model.NotificationOutbox
	require.NoError(t, fixture.harness.db.
		Where("id = ?", fixture.outboxID).First(&outbox).Error)
	payload, err := fixture.service.decryptAuthnData(
		notificationCipher, fixture.outboxID.String(), outbox.PayloadCiphertext,
	)
	require.NoError(t, err)
	assert.Equal(t, notARealOutboxPayload, string(payload))

	// The confirming pass is what an operator actually acts on, and it has to
	// come out of the same code path rather than from the report above.
	confirmation := fixture.rewrap(t, keyringx.SurveyOnly)
	for _, sealed := range confirmation.Sealed {
		assert.Equal(t, int64(1), sealed.Current, sealed.Store)
	}
	assert.Equal(t, int64(0), confirmation.Pending())
}

// TestAuthnRewrapReportsWhatItCannotReseal is the half of the report that keeps
// the other half from being dangerous. Recovery codes are digests of secrets
// this deployment does not hold, so no pass converts them -- and unlike the
// tokens, they never expire, which is the difference between "wait until
// Tuesday" and "this will not clear on its own".
func TestAuthnRewrapReportsWhatItCannotReseal(t *testing.T) {
	fixture := newRewrapFixture(t)

	result := fixture.rewrap(t, keyringx.RewrapStale)

	require.Len(t, result.Derived, 3)
	codes := result.Derived[0]
	assert.Equal(t, "iam_recovery_codes.code_hash", codes.Store)
	assert.Equal(t, int64(2), codes.Outstanding, "a used code cannot be presented again")
	assert.True(t, codes.Until.IsZero(), "recovery codes never expire, and saying so is the finding")
	assert.NotEmpty(t, codes.Reissue, "a store that never expires has to say what ends it")

	recovery := result.Derived[1]
	assert.Equal(t, "iam_password_recovery_tokens.token_hmac", recovery.Store)
	assert.Equal(t, int64(2), recovery.Outstanding,
		"an expired or consumed link will never be presented, so it cannot hold a key open")
	assert.WithinDuration(t, fixture.lastExpiry, recovery.Until, time.Second)

	verification := result.Derived[2]
	assert.Equal(t, "iam_email_verification_tokens.token_hmac", verification.Store)
	assert.Equal(t, int64(1), verification.Outstanding)
	assert.False(t, verification.Until.IsZero())

	assert.Equal(t, int64(5), result.Outstanding(),
		"a rewrap converts nothing here, so a completed pass changes none of these numbers")
}

// TestAuthnRewrapSeparatesNothingSealedFromNothingRunning keeps two very
// different states from producing the same report. A deployment without browser
// authentication seals nothing, and an empty report is the truth; a module that
// never initialized cannot say what it holds, and reporting zero for it would be
// a guess dressed as an answer.
func TestAuthnRewrapSeparatesNothingSealedFromNothingRunning(t *testing.T) {
	service, harness := newLocalService(t)
	service.mfaKeys = nil

	result, err := (&Module{Service: service}).RewrapSecrets(t.Context(), harness.db, keyringx.RewrapStale)
	require.NoError(t, err)
	assert.Empty(t, result.Sealed)
	assert.Empty(t, result.Derived)
	assert.Equal(t, int64(0), result.Pending())

	_, err = (&Module{}).RewrapSecrets(t.Context(), harness.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)

	var absent *Module
	_, err = absent.RewrapSecrets(t.Context(), harness.db, keyringx.SurveyOnly)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
}
