package authn

import (
	"encoding/base32"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func authenticatedLocalService(t *testing.T) (*Service, *authnRepositoryHarness, string) {
	t.Helper()
	service, harness := newLocalService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	return service, harness, service.SessionCookie(login.SessionID)
}

func mustTOTP(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, now)
	require.NoError(t, err)
	return code
}

func TestTOTPEnrollmentAndLogin(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	enrollment, err := service.BeginTOTPEnrollment(
		t.Context(), "", cookie, "correct horse battery staple",
	)
	require.NoError(t, err)
	require.NotEmpty(t, enrollment.Secret)
	require.NotEmpty(t, enrollment.ProvisioningURI)

	confirmation, err := service.ConfirmTOTPEnrollment(
		t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)
	require.Len(t, confirmation.RecoveryCodes, service.cfg.MFA.RecoveryCodes)
	assert.Len(t, uniqueStrings(confirmation.RecoveryCodes), service.cfg.MFA.RecoveryCodes)

	status, err := service.MFAStatus(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.True(t, status.TOTPEnabled)
	assert.Equal(t, service.cfg.MFA.RecoveryCodes, status.RecoveryCodesRemaining)

	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	assert.Equal(t, "mfa_required", login.Status)
	require.NotEmpty(t, login.ChallengeID)
	assert.Empty(t, login.SessionID)

	completed, err := service.VerifyLoginMFA(
		t.Context(), login.ChallengeID, mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", completed.Status)
	require.NotEmpty(t, completed.SessionID)
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(completed.SessionID))
	require.NoError(t, err)
	assert.Equal(t, 2, claims.ACR)
	assert.Equal(t, []string{"pwd", "mfa"}, claims.AMR)

	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, mustTOTP(t, enrollment.Secret, service.now()))
	assert.ErrorIs(t, err, ErrMFAChallenge)
}

func TestMFAChallengeAttemptsAndRecoveryCodeConsumption(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	require.NoError(t, err)
	confirmation, err := service.ConfirmTOTPEnrollment(
		t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)

	service.now = func() time.Time { return repositoryTestNow.Add(time.Minute) }
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, "000000")
	assert.ErrorIs(t, err, ErrInvalidMFA)

	completed, err := service.VerifyLoginMFA(t.Context(), login.ChallengeID, confirmation.RecoveryCodes[0])
	require.NoError(t, err)
	require.NotEmpty(t, completed.SessionID)

	next, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	_, err = service.VerifyLoginMFA(t.Context(), next.ChallengeID, confirmation.RecoveryCodes[0])
	assert.ErrorIs(t, err, ErrInvalidMFA)
}

func TestMFAAuditFailureRollsBackEnrollmentAndConfirmation(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	harness.auditErr = errors.New("audit unavailable")
	_, err := service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	assert.ErrorContains(t, err, "audit unavailable")
	principal, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	_, err = harness.repository.activeMFAEnrollment(t.Context(), principal.PrincipalID, service.now())
	assert.Error(t, err)

	harness.auditErr = nil
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	require.NoError(t, err)
	harness.auditErr = errors.New("audit unavailable")
	_, err = service.ConfirmTOTPEnrollment(
		t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
	)
	assert.ErrorContains(t, err, "audit unavailable")
	credential, err := harness.repository.getCredential(t.Context(), principal.PrincipalID)
	require.NoError(t, err)
	assert.Empty(t, credential.TOTPSecret)
	assert.False(t, credential.MFARequired)
	remaining, err := harness.repository.countAvailableRecoveryCodes(t.Context(), principal.PrincipalID)
	require.NoError(t, err)
	assert.Zero(t, remaining)
}

func TestMFAEncryptionBindsPrincipalAndPurpose(t *testing.T) {
	service, _ := newLocalService(t)
	ciphertext, err := service.encryptMFASecret(101, "secret")
	require.NoError(t, err)
	plain, err := service.decryptMFASecret(101, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, "secret", plain)

	_, err = service.decryptMFASecret(102, ciphertext)
	assert.ErrorIs(t, err, errDecryptAuthenticationPayload)
	_, err = service.decryptMFASecret(101, "v2.invalid")
	assert.ErrorIs(t, err, errUnsupportedAuthnCiphertext)
}

func TestTOTPRFC6238Compatibility(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1_111_111_109, "081804"},
		{1_111_111_111, "050471"},
		{1_234_567_890, "005924"},
		{2_000_000_000, "279037"},
	}
	for _, vector := range vectors {
		at := time.Unix(vector.unix, 0).UTC()
		code, err := totp.GenerateCode(secret, at)
		require.NoError(t, err)
		assert.Equal(t, vector.want, code)
		step, valid := validateTOTP(code, secret, at)
		assert.True(t, valid)
		assert.Equal(t, vector.unix/totpPeriod, step)
	}
}

func TestTOTPProvisioningURI(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	service.cfg.MFA.Issuer = "SevenLink Test"
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	require.NoError(t, err)

	parsed, err := url.Parse(enrollment.ProvisioningURI)
	require.NoError(t, err)
	assert.Equal(t, "otpauth", parsed.Scheme)
	assert.Equal(t, "totp", parsed.Host)
	assert.Equal(t, enrollment.Secret, parsed.Query().Get("secret"))
	assert.Equal(t, "SHA1", parsed.Query().Get("algorithm"))
	assert.Equal(t, "6", parsed.Query().Get("digits"))
	assert.Equal(t, "30", parsed.Query().Get("period"))
	assert.Equal(t, service.cfg.MFA.Issuer, parsed.Query().Get("issuer"))
}

func TestMFARecoveryCooldownAndDisabledState(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	future := service.now().Add(time.Hour)
	require.NoError(t, harness.db.Table(harness.repository.table("iam_credentials")).
		Where("principal_id = ?", claims.PrincipalID).Update("recovery_cooldown_until", future).Error)
	_, err = service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	assert.ErrorIs(t, err, authnext.ErrRecoveryCooldown)

	require.NoError(t, harness.db.Table(harness.repository.table("iam_credentials")).
		Where("principal_id = ?", claims.PrincipalID).Update("recovery_cooldown_until", nil).Error)
	assert.ErrorIs(t, service.DisableTOTP(
		t.Context(), "", cookie, "correct horse battery staple", "000000",
	), ErrMFANotEnabled)
}

func TestRecoveryCodeRegenerationAndTOTPDisable(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	enrollment, err := service.BeginTOTPEnrollment(
		t.Context(), "", cookie, "correct horse battery staple",
	)
	require.NoError(t, err)
	confirmation, err := service.ConfirmTOTPEnrollment(
		t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)

	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	challenge, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	login, err := service.VerifyLoginMFA(
		t.Context(), challenge.ChallengeID,
		strings.ToLower(strings.ReplaceAll(confirmation.RecoveryCodes[0], "-", " ")),
	)
	require.NoError(t, err)
	recoveryCookie := service.SessionCookie(login.SessionID)
	status, err := service.MFAStatus(t.Context(), "", recoveryCookie)
	require.NoError(t, err)
	assert.Equal(t, service.cfg.MFA.RecoveryCodes-1, status.RecoveryCodesRemaining)

	_, err = service.RegenerateRecoveryCodes(
		t.Context(), "", cookie, "wrong password", mustTOTP(t, enrollment.Secret, service.now()),
	)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	replacement, err := service.RegenerateRecoveryCodes(
		t.Context(), "", cookie, "correct horse battery staple", mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)
	require.Len(t, replacement.RecoveryCodes, service.cfg.MFA.RecoveryCodes)
	assert.NotContains(t, replacement.RecoveryCodes, confirmation.RecoveryCodes[1])
	_, err = service.Authenticate(t.Context(), "", recoveryCookie)
	assert.ErrorIs(t, err, ErrInvalidSession)

	service.now = func() time.Time { return repositoryTestNow.Add(time.Minute) }
	validCode := mustTOTP(t, enrollment.Secret, service.now())
	invalidCode := "000000"
	if invalidCode == validCode {
		invalidCode = "111111"
	}
	assert.ErrorIs(t, service.DisableTOTP(
		t.Context(), "", cookie, "correct horse battery staple", invalidCode,
	), ErrInvalidMFA)
	require.NoError(t, service.DisableTOTP(
		t.Context(), "", cookie, "correct horse battery staple", validCode,
	))
	status, err = service.MFAStatus(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.False(t, status.TOTPEnabled)
	assert.Zero(t, status.RecoveryCodesRemaining)
	assert.ErrorIs(t, service.DisableTOTP(
		t.Context(), "", cookie, "correct horse battery staple", "000000",
	), ErrMFANotEnabled)
	_, err = service.RegenerateRecoveryCodes(
		t.Context(), "", cookie, "correct horse battery staple", "000000",
	)
	assert.ErrorIs(t, err, ErrMFANotEnabled)
}

func TestMFAInvalidAuthenticationAndStorageFailures(t *testing.T) {
	service, _, _ := authenticatedLocalService(t)
	_, err := service.MFAStatus(t.Context(), "", "")
	assert.Error(t, err)
	_, err = service.BeginTOTPEnrollment(t.Context(), "", "", "password")
	assert.Error(t, err)
	_, err = service.ConfirmTOTPEnrollment(t.Context(), "", "", "123456")
	assert.Error(t, err)
	_, err = service.VerifyLoginMFA(t.Context(), "", "")
	assert.ErrorIs(t, err, ErrMFAChallenge)
	_, err = service.RegenerateRecoveryCodes(t.Context(), "", "", "password", "123456")
	assert.Error(t, err)
	assert.Error(t, service.DisableTOTP(t.Context(), "", "", "password", "123456"))
	assert.Empty(t, currentSessionHash("", service.web.CookieName))
	_, err = service.decryptAuthnData("v1", "principal", "v1.%%%")
	assert.ErrorContains(t, err, "invalid authentication ciphertext")

	t.Run("recovery status", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		claims, err := service.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		bearer, _, err := service.IssueAccessToken(t.Context(), claims.PrincipalID, "", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_recovery_codes").Error)
		_, err = service.MFAStatus(t.Context(), "Bearer "+bearer, "")
		assert.ErrorContains(t, err, "count recovery codes")
	})

	t.Run("enrollment store", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_mfa_enrollments").Error)
		_, err := service.BeginTOTPEnrollment(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		assert.ErrorContains(t, err, "store TOTP enrollment")
	})

	t.Run("enrollment lookup", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_mfa_enrollments").Error)
		_, err := service.ConfirmTOTPEnrollment(t.Context(), "", cookie, "123456")
		assert.ErrorContains(t, err, "confirm TOTP enrollment")
	})

	t.Run("recovery code store", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		enrollment, err := service.BeginTOTPEnrollment(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_recovery_codes").Error)
		_, err = service.ConfirmTOTPEnrollment(
			t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
		)
		assert.ErrorContains(t, err, "confirm TOTP enrollment")
	})

	t.Run("challenge store", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		claims, err := service.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_mfa_challenges").Error)
		_, err = service.createLoginChallenge(
			t.Context(), claims.PrincipalID, service.web.PostLoginURL, service.now(),
		)
		assert.ErrorContains(t, err, "create MFA challenge")
	})

	t.Run("corrupt enrollment", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		enrollment, err := service.BeginTOTPEnrollment(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		require.NoError(t, err)
		claims, err := service.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		require.NoError(t, harness.db.Table("iam_mfa_enrollments").Where("principal_id = ?", claims.PrincipalID).
			Update("secret_ciphertext", "v1.invalid").Error)
		_, err = service.ConfirmTOTPEnrollment(
			t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
		)
		assert.Error(t, err)
	})

	t.Run("disabled principal password check", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		claims, err := service.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", claims.PrincipalID).
			Update("status", "disabled").Error)
		_, _, err = service.verifyCurrentPassword(
			t.Context(), claims.PrincipalID, "correct horse battery staple",
		)
		assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	})
}

func TestMFAEnrollmentAndLoginChallengeFailureStates(t *testing.T) {
	t.Run("invalid and expired enrollment", func(t *testing.T) {
		service, _, cookie := authenticatedLocalService(t)
		service.cfg.MFA.EnrollmentTTL = time.Second
		enrollment, err := service.BeginTOTPEnrollment(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		require.NoError(t, err)
		_, err = service.ConfirmTOTPEnrollment(t.Context(), "", cookie, "000000")
		assert.ErrorIs(t, err, ErrInvalidMFA)
		service.now = func() time.Time { return repositoryTestNow.Add(2 * time.Second) }
		_, err = service.ConfirmTOTPEnrollment(
			t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
		)
		assert.ErrorIs(t, err, ErrMFAEnrollment)
	})

	t.Run("already enabled", func(t *testing.T) {
		service, _, cookie, _ := enabledMFAService(t)
		_, err := service.BeginTOTPEnrollment(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		assert.ErrorIs(t, err, ErrMFAAlreadyOn)
	})

	t.Run("attempt limit and expiration", func(t *testing.T) {
		service, _, cookie, secret := enabledMFAService(t)
		service.cfg.MFA.MaxAttempts = 2
		service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
		challenge, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		valid := mustTOTP(t, secret, service.now())
		invalid := "000000"
		if invalid == valid {
			invalid = "111111"
		}
		for range service.cfg.MFA.MaxAttempts {
			_, err = service.VerifyLoginMFA(t.Context(), challenge.ChallengeID, invalid)
			assert.ErrorIs(t, err, ErrInvalidMFA)
		}
		_, err = service.VerifyLoginMFA(t.Context(), challenge.ChallengeID, valid)
		assert.ErrorIs(t, err, ErrMFAChallenge)

		challenge, err = service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		service.now = func() time.Time {
			return repositoryTestNow.Add(30*time.Second + service.cfg.MFA.ChallengeTTL + time.Second)
		}
		_, err = service.VerifyLoginMFA(t.Context(), challenge.ChallengeID, mustTOTP(t, secret, service.now()))
		assert.ErrorIs(t, err, ErrMFAChallenge)
		_ = cookie
	})

	t.Run("corrupt secret", func(t *testing.T) {
		service, harness, _, _ := enabledMFAService(t)
		principal, err := harness.repository.findLoginPrincipal(t.Context(), "alice")
		require.NoError(t, err)
		require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principal.ID).
			Update("totp_secret", "v1.invalid").Error)
		challenge, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		_, err = service.VerifyLoginMFA(t.Context(), challenge.ChallengeID, "123456")
		assert.Error(t, err)
	})

	t.Run("audit failure rolls back login", func(t *testing.T) {
		service, harness, _, secret := enabledMFAService(t)
		service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
		challenge, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		harness.auditErr = errors.New("audit unavailable")
		_, err = service.VerifyLoginMFA(
			t.Context(), challenge.ChallengeID, mustTOTP(t, secret, service.now()),
		)
		assert.ErrorContains(t, err, "audit unavailable")
		harness.auditErr = nil
		result, err := service.VerifyLoginMFA(
			t.Context(), challenge.ChallengeID, mustTOTP(t, secret, service.now()),
		)
		require.NoError(t, err)
		assert.NotEmpty(t, result.SessionID)
	})
}

func TestMFAEnrollmentReadLocksPrincipalBeforeLoadingEnrollment(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	now := repositoryTestNow
	require.NoError(t, harness.repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
		PrincipalID: principalID, SecretCiphertext: "ciphertext", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}))

	const callbackName = "test:record_mfa_enrollment_query_order"
	var tables []string
	require.NoError(t, harness.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == "iam_principals" || db.Statement.Table == (model.MFAEnrollment{}).TableName() {
			tables = append(tables, db.Statement.Table)
		}
	}))
	t.Cleanup(func() { require.NoError(t, harness.db.Callback().Query().Remove(callbackName)) })

	err := harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		_, loadErr := repository.activeMFAEnrollmentForUpdate(t.Context(), principalID, now)
		return loadErr
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"iam_principals", (model.MFAEnrollment{}).TableName()}, tables)
}

func uniqueStrings(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
