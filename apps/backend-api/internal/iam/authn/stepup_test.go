package authn

import (
	"errors"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enabledMFAService(t *testing.T) (*Service, *authnRepositoryHarness, string, string) {
	t.Helper()
	service, harness, cookie := authenticatedLocalService(t)
	enrollment, err := service.BeginTOTPEnrollment(t.Context(), "", cookie, "correct horse battery staple")
	require.NoError(t, err)
	_, err = service.ConfirmTOTPEnrollment(t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()))
	require.NoError(t, err)
	return service, harness, cookie, enrollment.Secret
}

func TestStepUpRotatesBoundSession(t *testing.T) {
	service, _, cookie, secret := enabledMFAService(t)
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	options, err := service.BeginStepUp(t.Context(), "", cookie)
	require.NoError(t, err)
	result, err := service.VerifyStepUp(
		t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)
	assert.Equal(t, repositoryTestNow.Add(time.Hour), result.ExpiresAt)

	_, err = service.Authenticate(t.Context(), "", cookie)
	assert.ErrorIs(t, err, ErrInvalidSession)
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(result.SessionID))
	require.NoError(t, err)
	assert.Equal(t, 2, claims.ACR)
	assert.Contains(t, claims.AMR, "mfa")
	_, err = service.VerifyStepUp(
		t.Context(), "", service.SessionCookie(result.SessionID), options.ChallengeID,
		mustTOTP(t, secret, service.now()),
	)
	assert.ErrorIs(t, err, ErrMFAChallenge)
}

func TestStepUpRejectsDifferentSessionAndBearerOnlyCaller(t *testing.T) {
	service, harness, cookie, secret := enabledMFAService(t)
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	options, err := service.BeginStepUp(t.Context(), "", cookie)
	require.NoError(t, err)
	principal, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	otherToken, err := service.CreateSession(t.Context(), principal.PrincipalID, service.now(), assurancePassword(service.now()))
	require.NoError(t, err)
	otherCookie := service.SessionCookie(otherToken)

	_, err = service.VerifyStepUp(
		t.Context(), "", otherCookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	assert.ErrorIs(t, err, ErrMFAChallenge)
	result, err := service.VerifyStepUp(
		t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, result.SessionID)

	bearer, _, err := service.IssueAccessToken(t.Context(), principal.PrincipalID, "", "")
	require.NoError(t, err)
	_, err = service.BeginStepUp(t.Context(), "Bearer "+bearer, "")
	assert.ErrorIs(t, err, ErrMFAChallenge)
	_ = harness
}

func TestStepUpAuditFailureRollsBackChallengeAndSessionRotation(t *testing.T) {
	service, harness, cookie, secret := enabledMFAService(t)
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	options, err := service.BeginStepUp(t.Context(), "", cookie)
	require.NoError(t, err)
	harness.auditErr = errors.New("audit unavailable")
	_, err = service.VerifyStepUp(
		t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	assert.ErrorContains(t, err, "audit unavailable")
	_, err = service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err, "the original session must remain active")

	harness.auditErr = nil
	result, err := service.VerifyStepUp(
		t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	require.NoError(t, err)
	assert.NotEmpty(t, result.SessionID)
}

func TestStepUpRequiresMFAAndValidBrowserInputs(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	_, err := service.BeginStepUp(t.Context(), "", cookie)
	assert.ErrorIs(t, err, ErrMFANotEnabled)
	_, err = service.BeginStepUp(t.Context(), "", "")
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = service.VerifyStepUp(t.Context(), "", cookie, "", "123456")
	assert.ErrorIs(t, err, ErrMFAChallenge)
	_, err = service.VerifyStepUp(t.Context(), "", cookie, "challenge", "")
	assert.ErrorIs(t, err, ErrMFAChallenge)
}

func TestStepUpWrongCodeAttemptLimitAndExpiry(t *testing.T) {
	service, harness, cookie, secret := enabledMFAService(t)
	service.cfg.MFA.MaxAttempts = 2
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	options, err := service.BeginStepUp(t.Context(), "", cookie)
	require.NoError(t, err)
	valid := mustTOTP(t, secret, service.now())
	invalid := "000000"
	if invalid == valid {
		invalid = "111111"
	}
	for range service.cfg.MFA.MaxAttempts {
		_, err = service.VerifyStepUp(t.Context(), "", cookie, options.ChallengeID, invalid)
		assert.ErrorIs(t, err, ErrInvalidMFA)
	}
	challenge, err := harness.repository.getStepUpChallenge(t.Context(), tokenHash(options.ChallengeID))
	require.NoError(t, err)
	assert.Equal(t, service.cfg.MFA.MaxAttempts, challenge.Attempts)
	require.NotNil(t, challenge.ConsumedAt)
	_, err = service.VerifyStepUp(t.Context(), "", cookie, options.ChallengeID, valid)
	assert.ErrorIs(t, err, ErrMFAChallenge)

	options, err = service.BeginStepUp(t.Context(), "", cookie)
	require.NoError(t, err)
	service.now = func() time.Time { return options.ExpiresAt.Add(time.Second) }
	_, err = service.VerifyStepUp(t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()))
	assert.ErrorIs(t, err, ErrMFAChallenge)
}

func TestStepUpStorageAndStartAuditFailures(t *testing.T) {
	t.Run("start audit", func(t *testing.T) {
		service, harness, cookie, _ := enabledMFAService(t)
		harness.auditErr = errors.New("audit unavailable")
		_, err := service.BeginStepUp(t.Context(), "", cookie)
		assert.ErrorContains(t, err, "create step-up challenge")
		tables, _, tableErr := harness.repository.tables()
		require.NoError(t, tableErr)
		count, countErr := tables.StepUpChallenges.WithContext(t.Context()).Count()
		require.NoError(t, countErr)
		assert.Zero(t, count)
	})

	t.Run("challenge store", func(t *testing.T) {
		service, harness, cookie, _ := enabledMFAService(t)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_stepup_challenges").Error)
		_, err := service.BeginStepUp(t.Context(), "", cookie)
		assert.ErrorContains(t, err, "create step-up challenge")
	})

	t.Run("challenge lookup", func(t *testing.T) {
		service, harness, cookie, secret := enabledMFAService(t)
		options, err := service.BeginStepUp(t.Context(), "", cookie)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_stepup_challenges").Error)
		_, err = service.VerifyStepUp(
			t.Context(), "", cookie, options.ChallengeID, mustTOTP(t, secret, service.now()),
		)
		assert.ErrorIs(t, err, ErrMFAChallenge)
	})
}

func assurancePassword(now time.Time) authnext.Assurance {
	return authnext.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}}
}
