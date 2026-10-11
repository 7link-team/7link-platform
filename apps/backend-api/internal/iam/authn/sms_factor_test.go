package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSMSFactorService turns on the second factor as well as proving a number.
func newSMSFactorService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
	t.Helper()
	service, harness, recorder := newPhoneLoginService(t)
	service.cfg.PhoneVerification.SecondFactorEnabled = true
	recorders[service] = recorder
	return service, harness, recorder
}

// requireSecondFactor marks an account as needing one.
func requireSecondFactor(t *testing.T, harness *authnRepositoryHarness, id guid.ID) {
	t.Helper()
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_credentials").
		Where("principal_id = ?", id).Update("mfa_required", true).Error)
}

// enrollTOTP walks the module's own enrollment, because a secret written straight
// into the row would not be the thing these tests rely on.
func enrollTOTP(t *testing.T, service *Service, login string) string {
	t.Helper()
	cookie := signIn(t, service, login)
	enrollment, err := service.BeginTOTPEnrollment(
		t.Context(), "", cookie, "correct horse battery staple",
	)
	require.NoError(t, err)
	_, err = service.ConfirmTOTPEnrollment(
		t.Context(), "", cookie, mustTOTP(t, enrollment.Secret, service.now()),
	)
	require.NoError(t, err)
	return enrollment.Secret
}

func TestAProvenNumberAnswersASecondFactorChallenge(t *testing.T) {
	// An account with no TOTP secret used to be one nobody could get past: the
	// challenge asked for a factor it had not enrolled. A proven number answers it.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(801)
	seedProvenPhone(t, harness, principalID, "alice", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	require.Equal(t, "mfa_required", login.Status)
	assert.Equal(t, []string{"sms"}, login.Methods, "the only factor this account has")
	require.NotEmpty(t, login.ChallengeID)

	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	assert.Equal(t, phoneMFANotification, payload.Type)
	assert.Equal(t, "+8613800138000", payload.Recipient)

	result, err := service.VerifyLoginMFA(t.Context(), login.ChallengeID, payload.Code)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
	require.NotEmpty(t, result.SessionID)

	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(result.SessionID))
	require.NoError(t, err)
	assert.Equal(t, 2, claims.ACR, "two factors")
}

func TestANumberCannotBeBothFactorsOfTheSameSignIn(t *testing.T) {
	// A code to the number that just opened the login is the same factor twice.
	// Refused where the code is asked for, so it is never sent at all.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(802)
	seedProvenPhone(t, harness, principalID, "bob", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	first, err := service.CompletePhoneLogin(
		t.Context(), "+8613800138000", recorder.last(t).Code, "",
	)
	require.NoError(t, err)
	require.NotEqual(t, "authenticated", first.Status, "a second factor is still owed")

	if first.ChallengeID != "" {
		assert.NotContains(t, first.Methods, "sms", "the number already played its part")
		before := recorder.count()
		assert.ErrorIs(t,
			service.SendMFAPhoneCode(t.Context(), first.ChallengeID),
			authnext.ErrSMSFactorUnavailable)
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		assert.Equal(t, before, recorder.count(), "no code left for a factor that would not count")
		return
	}
	// With no other factor to offer, the account is told to enroll one instead of
	// being let through on the number twice.
	assert.Equal(t, "mfa_enrollment_required", first.Status)
}

func TestASecondFactorCodeIsNotASignInCode(t *testing.T) {
	// Each purpose answers its own flow. A second-factor code must not sign
	// anybody in on its own, which would turn two factors back into one.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(803)
	seedProvenPhone(t, harness, principalID, "carol", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "carol", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	factorCode := recorder.last(t).Code

	_, err = service.CompletePhoneLogin(t.Context(), "+8613800138000", factorCode, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)

	// It still answers the challenge it was sent for.
	result, err := service.VerifyLoginMFA(t.Context(), login.ChallengeID, factorCode)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
}

func TestATOTPAccountKeepsAnsweringWithTOTP(t *testing.T) {
	// The new factor is offered beside the old one, never instead of it.
	service, harness, _ := newSMSFactorService(t)
	principalID := guid.ID(804)
	seedProvenPhone(t, harness, principalID, "dave", "+8613800138000")
	secret := enrollTOTP(t, service, "dave")

	login, err := service.BeginLogin(t.Context(), "dave", "correct horse battery staple", "")
	require.NoError(t, err)
	require.Equal(t, "mfa_required", login.Status)
	assert.Equal(t, []string{"totp", "recovery_code", "sms"}, login.Methods)

	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	result, err := service.VerifyLoginMFA(
		t.Context(), login.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
}

func TestASecondFactorCodeIsRefusedWhereItIsNotTurnedOn(t *testing.T) {
	// It is the weakest factor here, so a deployment can prove numbers and still
	// refuse to accept one as a second factor.
	service, harness, _ := newSMSFactorService(t)
	principalID := guid.ID(805)
	seedProvenPhone(t, harness, principalID, "erin", "+8613800138000")
	enrollTOTP(t, service, "erin")
	service.cfg.PhoneVerification.SecondFactorEnabled = false

	login, err := service.BeginLogin(t.Context(), "erin", "correct horse battery staple", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"totp", "recovery_code"}, login.Methods, "no sms on offer")
	assert.ErrorIs(t,
		service.SendMFAPhoneCode(t.Context(), login.ChallengeID),
		authnext.ErrSMSFactorUnavailable)
}

func TestASecondFactorCodeFollowsTheNumberItWasSentTo(t *testing.T) {
	// The number is re-read when the code is spent: a code proves the number it
	// went to, and that has to still be the number on the account.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(806)
	seedProvenPhone(t, harness, principalID, "frank", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "frank", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	code := recorder.last(t).Code

	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Updates(map[string]any{
		"phone": "+14155550132", "phone_verified": false,
	}).Error)

	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	assert.ErrorIs(t, err, authnext.ErrInvalidMFA,
		"a code that no longer proves the number is a wrong code, so another can be asked for")
}

func TestAnExpiredOrSpentSecondFactorCodeAnswersNothing(t *testing.T) {
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(807)
	seedProvenPhone(t, harness, principalID, "grace", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "grace", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	code := recorder.last(t).Code

	service.now = func() time.Time { return repositoryTestNow.Add(11 * time.Minute) }
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	assert.Error(t, err, "an expired code answers nothing")

	service.now = func() time.Time { return repositoryTestNow }
	result, err := service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)

	// Spent, so a replay of the same code and challenge is refused.
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	assert.ErrorIs(t, err, ErrMFAChallenge)
}

func TestMFAStatusReportsWhetherANumberCanAnswer(t *testing.T) {
	service, harness, _ := newSMSFactorService(t)
	principalID := guid.ID(808)
	seedPrincipalWithPhone(t, harness, principalID, "heidi", "+8613800138000")
	cookie := signIn(t, service, "heidi")

	status, err := service.MFAStatus(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.False(t, status.SMSAvailable, "an unproven number answers nothing")

	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Update("phone_verified", true).Error)
	status, err = service.MFAStatus(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.True(t, status.SMSAvailable)

	service.cfg.PhoneVerification.SecondFactorEnabled = false
	status, err = service.MFAStatus(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.False(t, status.SMSAvailable, "not where the deployment refuses it")

	// A failed read is not "no number can answer". That answer sends somebody to
	// enroll a factor they already have, and tells the screen the opposite of
	// what the account says.
	service.cfg.PhoneVerification.SecondFactorEnabled = true
	failPrincipalReads(t, harness)
	_, err = service.MFAStatus(t.Context(), "", cookie)
	require.Error(t, err)
}

func TestANumberIsRefusedAsTheSecondFactorOfASignInItAlreadyOpened(t *testing.T) {
	// An account with TOTP as well as a proven number gets a challenge after
	// signing in by number -- and the challenge offers TOTP, not another code.
	// Asking for one anyway is refused at the point of asking, so none is sent.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(809)
	seedProvenPhone(t, harness, principalID, "ivan", "+8613800138000")
	enrollTOTP(t, service, "ivan")

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	login, err := service.CompletePhoneLogin(
		t.Context(), "+8613800138000", recorder.last(t).Code, "",
	)
	require.NoError(t, err)
	require.Equal(t, "mfa_required", login.Status)
	require.NotEmpty(t, login.ChallengeID)
	assert.Equal(t, []string{"totp", "recovery_code"}, login.Methods,
		"the number already played its part in this sign-in")

	before := recorder.count()
	assert.ErrorIs(t,
		service.SendMFAPhoneCode(t.Context(), login.ChallengeID),
		authnext.ErrSMSFactorUnavailable)
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count(), "nothing was sent")
}

func TestASecondFactorCodeDiesWhenANewerNumberIsProven(t *testing.T) {
	// Proving a different number while a code is outstanding leaves that code
	// proving possession of a number this account no longer has. The number is
	// still verified, so only the comparison against the stored one refuses it.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(810)
	seedProvenPhone(t, harness, principalID, "judy", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "judy", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	oldCode := recorder.last(t).Code

	// A newer number, proven: the flag stays true and only the number differs.
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Updates(map[string]any{
		"phone": "+14155550132", "phone_verified": true,
	}).Error)

	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, oldCode)
	assert.ErrorIs(t, err, authnext.ErrInvalidMFA)
}

func TestACodeAlreadySentStopsAnsweringWhenTheFactorIsTurnedOff(t *testing.T) {
	// Turning the factor off has to stop codes already in flight from answering,
	// not merely stop new ones being sent.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(811)
	seedProvenPhone(t, harness, principalID, "karl", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "karl", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	code := recorder.last(t).Code

	service.cfg.PhoneVerification.SecondFactorEnabled = false
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	assert.ErrorIs(t, err, authnext.ErrInvalidMFA)
}

func TestAWrongCodeOnAnAccountWithoutTOTPIsACleanRefusal(t *testing.T) {
	// Without the number, this account has no second factor at all, so a wrong
	// code must not be handed to the TOTP comparison -- there is no secret to
	// compare it against, and the answer is a refusal rather than a fault.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(812)
	seedProvenPhone(t, harness, principalID, "mona", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "mona", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	wrong := "000000"
	if recorder.last(t).Code == wrong {
		wrong = "111111"
	}

	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, wrong)
	require.Error(t, err)
	assert.ErrorIs(t, err, authnext.ErrInvalidMFA,
		"a wrong code, reported as one and counted against the challenge")
}

func TestASecondFactorCodeIsOnlySentForAChallengeThatCanTakeOne(t *testing.T) {
	// The challenge is the whole of the request, so every way it can fail to be
	// one has to answer before anything is sent: nothing, something that matches
	// no challenge, one already spent, one expired, and one whose attempts are
	// gone. None of them says which.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(813)
	seedProvenPhone(t, harness, principalID, "nora", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	assert.ErrorIs(t, service.SendMFAPhoneCode(t.Context(), ""), ErrMFAChallenge)
	assert.ErrorIs(t, service.SendMFAPhoneCode(t.Context(), "matches-nothing"), ErrMFAChallenge)

	login, err := service.BeginLogin(t.Context(), "nora", "correct horse battery staple", "")
	require.NoError(t, err)

	// Spent: the login it belonged to is over.
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, recorder.last(t).Code)
	require.NoError(t, err)
	before := recorder.count()
	assert.ErrorIs(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID), ErrMFAChallenge)

	// Expired.
	second, err := service.BeginLogin(t.Context(), "nora", "correct horse battery staple", "")
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow.Add(2 * time.Hour) }
	assert.ErrorIs(t, service.SendMFAPhoneCode(t.Context(), second.ChallengeID), ErrMFAChallenge)
	service.now = func() time.Time { return repositoryTestNow }

	// Attempts spent on the challenge itself.
	third, err := service.BeginLogin(t.Context(), "nora", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), third.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	for attempt := 0; attempt < service.cfg.MFA.MaxAttempts; attempt++ {
		_, verifyErr := service.VerifyLoginMFA(t.Context(), third.ChallengeID, "000000")
		require.Error(t, verifyErr, "attempt %d", attempt)
	}
	assert.ErrorIs(t, service.SendMFAPhoneCode(t.Context(), third.ChallengeID), ErrMFAChallenge,
		"a challenge nobody can answer any more is not sent another code")

	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before+1, recorder.count(),
		"one message for the one challenge that could still take a code")
}

func TestASecondFactorCodeNeedsANumberToSendItTo(t *testing.T) {
	// A challenge for an account whose number went away, or was never proven,
	// cannot be answered this way -- and says so rather than sending nothing and
	// reporting success.
	service, harness, _ := newSMSFactorService(t)
	principalID := guid.ID(814)
	seedProvenPhone(t, harness, principalID, "olga", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "olga", "correct horse battery staple", "")
	require.NoError(t, err)

	for _, update := range []map[string]any{
		{"phone_verified": false},
		{"phone": "", "phone_verified": false},
	} {
		require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
			Where("id = ?", principalID).Updates(update).Error)
		assert.ErrorIs(t,
			service.SendMFAPhoneCode(t.Context(), login.ChallengeID),
			authnext.ErrSMSFactorUnavailable)
	}
}

func TestNoStorageFaultWhileSendingASecondFactorCodeIsARefusal(t *testing.T) {
	// A failed read or write is not "this challenge cannot take a code": told that,
	// somebody picks another factor they may not have.
	for name, fail := range map[string]func(*testing.T, *authnRepositoryHarness){
		"writing the code": func(t *testing.T, h *authnRepositoryHarness) {
			failWritesTo(t, h, "iam_phone_verification_codes")
		},
		"writing the outbox": func(t *testing.T, h *authnRepositoryHarness) {
			failWritesTo(t, h, "iam_notification_outbox")
		},
		"reading the challenge": func(t *testing.T, h *authnRepositoryHarness) {
			failReadsFrom(t, h, "iam_mfa_challenges")
		},
		// A failed read of the account is not "this account has no number": that
		// answer sends somebody looking for a factor they do not have.
		"reading the account": failPrincipalReads,
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, _ := newSMSFactorService(t)
			principalID := guid.ID(815)
			seedProvenPhone(t, harness, principalID, "pia", "+8613800138000")
			requireSecondFactor(t, harness, principalID)
			login, err := service.BeginLogin(t.Context(), "pia", "correct horse battery staple", "")
			require.NoError(t, err)

			fail(t, harness)
			err = service.SendMFAPhoneCode(t.Context(), login.ChallengeID)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrMFAChallenge)
			assert.NotErrorIs(t, err, authnext.ErrSMSFactorUnavailable)
		})
	}
}

func TestNoStorageFaultWhileAnsweringWithACodeIsAWrongCode(t *testing.T) {
	// Spending the code reads two things, and neither failing may be reported as
	// an invalid code: a challenge has a fixed number of attempts, so a fault
	// told as a wrong code spends one of them and eventually the whole login.
	for name, fail := range map[string]func(*testing.T, *authnRepositoryHarness){
		"reading the code":    func(t *testing.T, h *authnRepositoryHarness) { failReadsFrom(t, h, "iam_phone_verification_codes") },
		"reading the account": failPrincipalReads,
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, recorder := newSMSFactorService(t)
			principalID := guid.ID(817)
			seedProvenPhone(t, harness, principalID, "nell", "+8613800138000")
			requireSecondFactor(t, harness, principalID)
			login, err := service.BeginLogin(t.Context(), "nell", "correct horse battery staple", "")
			require.NoError(t, err)
			require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
			require.NoError(t, service.deliverPendingNotifications(t.Context()))
			code := recorder.last(t).Code

			fail(t, harness)
			_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrInvalidMFA)
			assert.NotErrorIs(t, err, ErrMFAChallenge)
		})
	}
}

func TestASessionSaysWhichFactorOpenedIt(t *testing.T) {
	// A login opened with a code and finished with TOTP is not a login where a
	// password was typed. The challenge records what proved the first factor and
	// the session repeats it, so a policy that asks for a password is not
	// satisfied by somebody who only ever read a message.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(818)
	seedProvenPhone(t, harness, principalID, "vera", "+8613800138000")
	secret := enrollTOTP(t, service, "vera")
	service.cfg.PhoneVerification.LoginEnabled = true

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	login, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", recorder.last(t).Code, "")
	require.NoError(t, err)
	require.Equal(t, "mfa_required", login.Status)
	assert.NotContains(t, login.Methods, "sms", "the number is what just opened this")

	// Past the period the enrollment already spent: a TOTP code answers once.
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	result, err := service.VerifyLoginMFA(
		t.Context(), login.ChallengeID, mustTOTP(t, secret, service.now()),
	)
	require.NoError(t, err)
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(result.SessionID))
	require.NoError(t, err)
	assert.Equal(t, []string{"sms", "mfa"}, claims.AMR)
}

func TestGuessingASecondFactorCodeRunsOutOfChallengeAttempts(t *testing.T) {
	// An account whose only second factor is a number must not be a challenge
	// that can be guessed at forever. A wrong code counts against the same
	// challenge ceiling a wrong TOTP code does, and once it is spent the right
	// code no longer helps.
	service, harness, recorder := newSMSFactorService(t)
	principalID := guid.ID(816)
	seedProvenPhone(t, harness, principalID, "rosa", "+8613800138000")
	requireSecondFactor(t, harness, principalID)

	login, err := service.BeginLogin(t.Context(), "rosa", "correct horse battery staple", "")
	require.NoError(t, err)
	require.NoError(t, service.SendMFAPhoneCode(t.Context(), login.ChallengeID))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	code := recorder.last(t).Code
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}

	for attempt := 0; attempt < service.cfg.MFA.MaxAttempts; attempt++ {
		_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, wrong)
		require.Error(t, err, "attempt %d", attempt)
	}
	_, err = service.VerifyLoginMFA(t.Context(), login.ChallengeID, code)
	assert.ErrorIs(t, err, ErrMFAChallenge,
		"the right code no longer answers a challenge whose attempts are spent")
}

func TestTheLoginOptionsSayWhetherANumberSignsAnybodyIn(t *testing.T) {
	// The console offers the phone form from this answer alone. Reporting it the
	// same either way is what made the button a dead end on a deployment that
	// had turned the flow off.
	service, _, _ := newSMSFactorService(t)
	assert.True(t, service.Capabilities().PhoneLogin, "the fixture turns it on")

	// Proving a number and signing in with one are separate decisions, so the
	// second factor staying on must not keep advertising a way in.
	service.cfg.PhoneVerification.LoginEnabled = false
	assert.False(t, service.Capabilities().PhoneLogin)
	service.cfg.PhoneVerification.LoginEnabled = true

	// Proving a number is the prerequisite: with that off, nothing is proven and
	// nothing signs anybody in, whatever the login switch says.
	service.cfg.PhoneVerification.Enabled = false
	assert.False(t, service.Capabilities().PhoneLogin)
}
