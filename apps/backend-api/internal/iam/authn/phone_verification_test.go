package authn

import (
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newPhoneVerificationService mirrors the email fixture: a real notification
// recorder, because the code only exists once it has left through the outbox.
func newPhoneVerificationService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
	t.Helper()
	service, harness, recorder := newVerificationService(t)
	service.cfg.PhoneVerification = authnext.PhoneVerificationConfig{Enabled: true, CodeTTL: 10 * time.Minute}
	return service, harness, recorder
}

func seedPrincipalWithPhone(t *testing.T, harness *authnRepositoryHarness, id guid.ID, login, phone string) {
	t.Helper()
	seedUnverifiedPrincipal(t, harness, id, login)
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", id).Update("phone", phone).Error)
}

func signIn(t *testing.T, service *Service, login string) string {
	t.Helper()
	session, err := service.BeginLogin(t.Context(), login, "correct horse battery staple", "")
	require.NoError(t, err)
	return service.SessionCookie(session.SessionID)
}

func TestPhoneVerificationProvesTheNumberOnTheAccount(t *testing.T) {
	service, harness, recorder := newPhoneVerificationService(t)
	principalID := guid.ID(601)
	seedPrincipalWithPhone(t, harness, principalID, "alice", "+8613800138000")
	cookie := signIn(t, service, "alice")

	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	assert.Equal(t, phoneVerificationNotification, payload.Type)
	assert.Equal(t, "+8613800138000", payload.Recipient, "the message goes to the number on the account")
	require.Len(t, payload.Code, 6)
	assert.Empty(t, payload.VerificationURL, "a number is proven by a code, not a link")

	require.NoError(t, service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code))
	assert.True(t, harness.principal(t, principalID).PhoneVerified)

	// Spent, observed directly rather than inferred from a second attempt being
	// refused: a refusal could come from the promotion being idempotent instead.
	var spent int64
	require.NoError(t, harness.db.
		Table(harness.runtime.TablePrefix()+"iam_phone_verification_codes").
		Where("principal_id = ? AND consumed_at IS NOT NULL", principalID).Count(&spent).Error)
	assert.Equal(t, int64(1), spent, "the code is consumed, not left live")

	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code),
		authnext.ErrInvalidPhoneVerification)
}

func TestACodeProvesTheNumberItWasSentToAndNoOther(t *testing.T) {
	// The number is kept with the code for exactly this: if somebody changes
	// their number while a code is outstanding, that code must not promote the
	// new one.
	service, harness, recorder := newPhoneVerificationService(t)
	principalID := guid.ID(602)
	seedPrincipalWithPhone(t, harness, principalID, "bob", "+8613800138000")
	cookie := signIn(t, service, "bob")

	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)

	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Update("phone", "+14155550132").Error)

	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code),
		authnext.ErrInvalidPhoneVerification, "the code was sent to a number this account no longer has")
	assert.False(t, harness.principal(t, principalID).PhoneVerified)
}

func TestGuessingAPhoneCodeRunsOutOfAttempts(t *testing.T) {
	// Six digits is guessable if guessing is free, so wrong codes are counted
	// and the credential stops answering once the ceiling is reached -- even for
	// the right code afterwards.
	service, harness, recorder := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 603, "carol", "+8613800138000")
	cookie := signIn(t, service, "carol")

	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	wrong := "000000"
	if payload.Code == wrong {
		wrong = "111111"
	}

	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		assert.ErrorIs(t,
			service.CompletePhoneVerification(t.Context(), "", cookie, wrong),
			authnext.ErrInvalidPhoneVerification, "attempt %d", attempt)
	}
	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code),
		authnext.ErrInvalidPhoneVerification, "the right code no longer works once the attempts are spent")
	assert.False(t, harness.principal(t, 603).PhoneVerified)

	// Asking again replaces the spent credential, so somebody who ran out is not
	// locked out forever.
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	require.NoError(t, service.CompletePhoneVerification(t.Context(), "", cookie, recorder.last(t).Code))
	assert.True(t, harness.principal(t, 603).PhoneVerified)
}

func TestAskingAgainReplacesTheOutstandingCode(t *testing.T) {
	// One live code per person: the last message somebody received is the one
	// that works, which is what they will type.
	service, harness, recorder := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 604, "dave", "+8613800138000")
	cookie := signIn(t, service, "dave")

	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	first := recorder.last(t).Code
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	second := recorder.last(t).Code
	require.NotEqual(t, first, second, "a fresh code is a fresh code")

	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, first),
		authnext.ErrInvalidPhoneVerification, "the superseded code is gone")
	require.NoError(t, service.CompletePhoneVerification(t.Context(), "", cookie, second))
	assert.True(t, harness.principal(t, 604).PhoneVerified)
}

func TestAnExpiredPhoneCodeProvesNothing(t *testing.T) {
	service, harness, recorder := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 605, "erin", "+8613800138000")
	cookie := signIn(t, service, "erin")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)

	service.now = func() time.Time { return repositoryTestNow.Add(11 * time.Minute) }
	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code),
		authnext.ErrInvalidPhoneVerification)
	assert.False(t, harness.principal(t, 605).PhoneVerified)
}

func TestVerifyingNeedsANumberToVerify(t *testing.T) {
	service, harness, _ := newPhoneVerificationService(t)
	seedUnverifiedPrincipal(t, harness, 606, "frank")
	cookie := signIn(t, service, "frank")

	assert.ErrorIs(t, service.BeginPhoneVerification(t.Context(), "", cookie), authnext.ErrPhoneRequired)

	// And the flow refuses outright where an operator has not enabled it.
	service.cfg.PhoneVerification.Enabled = false
	assert.ErrorIs(t, service.BeginPhoneVerification(t.Context(), "", cookie), authnext.ErrPhoneVerificationDisabled)
	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, "123456"),
		authnext.ErrPhoneVerificationDisabled)
}

func TestAPhoneCodeNeedsASessionAndASixDigitCode(t *testing.T) {
	// The session says whose number this is, so an anonymous caller has no number
	// to prove and nothing in the request identifies one.
	service, harness, _ := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 607, "grace", "+8613800138000")

	assert.Error(t, service.BeginPhoneVerification(t.Context(), "", ""))
	assert.Error(t, service.CompletePhoneVerification(t.Context(), "", "", "123456"))

	cookie := signIn(t, service, "grace")
	for _, refused := range []string{"", "12345", "1234567", "12a456", "      "} {
		assert.ErrorIs(t,
			service.CompletePhoneVerification(t.Context(), "", cookie, refused),
			authnext.ErrInvalidPhoneVerification, "%q", refused)
	}
}

func TestVerifyingTwiceIsNotAnError(t *testing.T) {
	// Asking to prove a number already proven is somebody pressing a button
	// twice, not a failure, and it must not send a second message.
	service, harness, recorder := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 608, "heidi", "+8613800138000")
	cookie := signIn(t, service, "heidi")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	require.NoError(t, service.CompletePhoneVerification(t.Context(), "", cookie, recorder.last(t).Code))

	before := recorder.count()
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count(), "no second message for a number already proven")
}

func TestErasureTakesTheVerificationCodeWithTheNumber(t *testing.T) {
	// The code row holds a phone number, so it is PII and the erasure path has to
	// delete it rather than leave a copy behind.
	service, harness, _ := newPhoneVerificationService(t)
	principalID := guid.ID(609)
	seedPrincipalWithPhone(t, harness, principalID, "ivan", "+8613800138000")
	cookie := signIn(t, service, "ivan")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))

	table := harness.runtime.TablePrefix() + "iam_phone_verification_codes"
	var before int64
	require.NoError(t, harness.db.Table(table).Where("principal_id = ?", principalID).Count(&before).Error)
	require.Equal(t, int64(1), before)

	require.NoError(t, ErasePrincipalData(
		t.Context(), harness.db, harness.runtime.TablePrefix(), principalID, repositoryTestNow,
	))
	var after int64
	require.NoError(t, harness.db.Table(table).Where("principal_id = ?", principalID).Count(&after).Error)
	assert.Zero(t, after, "the number left with the rest of the authentication data")
}

func TestPhoneVerificationRefusesConfigurationItCannotStandBehind(t *testing.T) {
	// Six digits read off a message already in somebody's hand is worth guessing
	// if it stays live for a day, and a code nobody can be sent proves nothing.
	// Both are refused at startup rather than at the first request.
	base := func() authnext.Config {
		config := localAuthnConfig()
		config.Notification = authnext.NotificationConfig{
			URL: "https://notify.example.test", PollInterval: time.Second,
			RequestTimeout: time.Second, MaxAttempts: 3,
		}
		config.PhoneVerification = authnext.PhoneVerificationConfig{
			Enabled: true, CodeTTL: 10 * time.Minute,
		}
		return config
	}

	for name, mutate := range map[string]func(*authnext.Config){
		"code lives too long":  func(c *authnext.Config) { c.PhoneVerification.CodeTTL = 2 * time.Hour },
		"code expires at once": func(c *authnext.Config) { c.PhoneVerification.CodeTTL = time.Second },
		"nothing can deliver":  func(c *authnext.Config) { c.Notification = authnext.NotificationConfig{} },
		"web surface disabled": func(c *authnext.Config) { c.Web.Enabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			config := base()
			mutate(&config)
			service := &Service{cfg: config, web: config.Web}
			require.Error(t, service.configurePhoneVerification())
		})
	}

	// Switched off, it has nothing to validate.
	config := base()
	config.PhoneVerification.Enabled = false
	service := &Service{cfg: config, web: config.Web}
	require.NoError(t, service.configurePhoneVerification())

	// A configuration it can stand behind passes, which needs a service that can
	// actually deliver: the fixture builds one.
	ready, _, _ := newPhoneVerificationService(t)
	require.NoError(t, ready.configurePhoneVerification())
}

func TestPhoneVerificationStorageFaultsAreNotReportedAsRefusals(t *testing.T) {
	// A failed read is not a wrong code. Told the code is invalid, somebody asks
	// for another and gets the same answer; the fault has to reach the 5xx path
	// that is actually watched.
	for name, run := range map[string]func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error{
		"begin": func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error {
			require.NoError(t, harness.db.Exec(
				"DROP TABLE "+harness.runtime.TablePrefix()+"iam_phone_verification_codes").Error)
			return service.BeginPhoneVerification(t.Context(), "", cookie)
		},
		"complete": func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error {
			require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
			require.NoError(t, harness.db.Exec(
				"DROP TABLE "+harness.runtime.TablePrefix()+"iam_phone_verification_codes").Error)
			return service.CompletePhoneVerification(t.Context(), "", cookie, "123456")
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, _ := newPhoneVerificationService(t)
			seedPrincipalWithPhone(t, harness, 610, "judy", "+8613800138000")
			cookie := signIn(t, service, "judy")
			err := run(t, service, harness, cookie)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrInvalidPhoneVerification)
			assert.NotErrorIs(t, err, authnext.ErrPhoneRequired)
			assert.NotErrorIs(t, err, authnext.ErrPhoneVerificationDisabled)
		})
	}
}

func TestAnOutstandingCodeDoesNotSurviveTheAccountBeingSuspended(t *testing.T) {
	// Ending somebody's authentication authority has to end the code that would
	// prove their number too, or a suspended account keeps a live credential.
	service, harness, _ := newPhoneVerificationService(t)
	principalID := guid.ID(611)
	seedPrincipalWithPhone(t, harness, principalID, "karl", "+8613800138000")
	cookie := signIn(t, service, "karl")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))

	require.NoError(t, service.repo.runInTx(t.Context(), func(repository *Repository) error {
		return service.revokePrincipalCredentialsOn(t.Context(), repository.writer(), principalID)
	}))

	var live int64
	require.NoError(t, harness.db.
		Table(harness.runtime.TablePrefix()+"iam_phone_verification_codes").
		Where("principal_id = ? AND consumed_at IS NULL", principalID).Count(&live).Error)
	assert.Zero(t, live, "the outstanding code was spent with the rest of the authority")
}

func TestCompletingWithoutEverAskingForACodeIsRefused(t *testing.T) {
	service, harness, _ := newPhoneVerificationService(t)
	seedPrincipalWithPhone(t, harness, 612, "lena", "+8613800138000")
	cookie := signIn(t, service, "lena")

	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", cookie, "123456"),
		authnext.ErrInvalidPhoneVerification)
}

func TestASuspendedPrincipalCannotFinishProvingANumber(t *testing.T) {
	// The lock re-reads the principal, so an account disabled between asking and
	// answering is refused as a dead session rather than promoted.
	service, harness, recorder := newPhoneVerificationService(t)
	principalID := guid.ID(613)
	seedPrincipalWithPhone(t, harness, principalID, "mona", "+8613800138000")
	cookie := signIn(t, service, "mona")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)

	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Update("status", "disabled").Error)

	err := service.CompletePhoneVerification(t.Context(), "", cookie, payload.Code)
	require.Error(t, err)
	assert.False(t, harness.principal(t, principalID).PhoneVerified)
}

func TestPhoneVerificationReportsAnUnavailableRepository(t *testing.T) {
	// Every read and write here goes through the shared table binding, so a
	// repository with nothing behind it has to answer unavailable rather than
	// panic or report a refusal the caller would act on.
	repository := &Repository{}
	row := &model.PhoneVerificationCode{PrincipalID: 1, Purpose: phoneCodeVerify, Phone: "+8613800138000", Code: "123456"}
	now := repositoryTestNow

	require.Error(t, repository.replacePhoneCode(t.Context(), row))
	_, err := repository.activePhoneCode(t.Context(), 1, phoneCodeVerify, now)
	require.Error(t, err)
	require.Error(t, repository.recordPhoneCodeAttempt(t.Context(), 1, phoneCodeVerify, maxCodeAttempts, now))
	_, err = repository.consumePhoneCode(t.Context(), 1, phoneCodeVerify, maxCodeAttempts, now)
	require.Error(t, err)
	require.Error(t, repository.consumePhoneVerifications(t.Context(), 1, now))
	_, err = repository.verifyPrincipalPhone(t.Context(), 1, "+8613800138000", now)
	require.Error(t, err)

	// And the flow above them refuses rather than reporting a bad code.
	service := &Service{repo: repository}
	assert.ErrorIs(t, service.BeginPhoneVerification(t.Context(), "", ""), authnext.ErrPhoneVerificationDisabled)
	assert.ErrorIs(t,
		service.CompletePhoneVerification(t.Context(), "", "", "123456"),
		authnext.ErrPhoneVerificationDisabled)
}

// failWritesTo makes every write to one table fail while leaving its rows in
// place, which is how a storage fault is told apart from a missing row.
func failWritesTo(t *testing.T, harness *authnRepositoryHarness, table string) {
	t.Helper()
	name := harness.runtime.TablePrefix() + table
	fail := func(db *gorm.DB) {
		if db.Statement.Table == name {
			db.AddError(errors.New("storage unavailable"))
		}
	}
	require.NoError(t, harness.db.Callback().Create().Before("gorm:create").Register("test:fail_create", fail))
	require.NoError(t, harness.db.Callback().Update().Before("gorm:update").Register("test:fail_update", fail))
	// Some writes here are a single UPDATE sent as raw SQL, which carries no
	// table clause at all. Matching only the clause left those writes injectable
	// by nothing, so the statement text is what identifies them.
	require.NoError(t, harness.db.Callback().Raw().Before("gorm:raw").Register("test:fail_raw",
		func(db *gorm.DB) {
			if strings.Contains(db.Statement.SQL.String(), name) {
				db.AddError(errors.New("storage unavailable"))
			}
		}))
}

func TestAFailedWriteIsNotAWrongCode(t *testing.T) {
	// Queuing a code and counting a guess both write. Told the code is invalid,
	// somebody asks for another and gets the same answer, so the fault has to
	// reach the path that is watched instead.
	t.Run("queueing a code", func(t *testing.T) {
		service, harness, _ := newPhoneVerificationService(t)
		seedPrincipalWithPhone(t, harness, 614, "nina", "+8613800138000")
		cookie := signIn(t, service, "nina")
		failWritesTo(t, harness, "iam_phone_verification_codes")

		err := service.BeginPhoneVerification(t.Context(), "", cookie)
		require.Error(t, err)
		assert.NotErrorIs(t, err, authnext.ErrPhoneRequired)
		assert.NotErrorIs(t, err, authnext.ErrInvalidSession)
	})

	t.Run("counting a guess", func(t *testing.T) {
		service, harness, recorder := newPhoneVerificationService(t)
		seedPrincipalWithPhone(t, harness, 615, "omar", "+8613800138000")
		cookie := signIn(t, service, "omar")
		require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
		require.NoError(t, service.deliverPendingNotifications(t.Context()))
		wrong := "000000"
		if recorder.last(t).Code == wrong {
			wrong = "111111"
		}
		failWritesTo(t, harness, "iam_phone_verification_codes")

		err := service.CompletePhoneVerification(t.Context(), "", cookie, wrong)
		require.Error(t, err)
		assert.NotErrorIs(t, err, authnext.ErrInvalidPhoneVerification,
			"a guess that could not be counted is not reported as merely wrong")
	})
}

func TestNoStorageFaultWhileProvingANumberIsReportedAsARefusal(t *testing.T) {
	// The same rule as the sign-in path, on the flow beside it: a missing row is a
	// refusal, a failed read or write is not. Told the code is invalid, somebody
	// asks for another and gets the same answer.
	type step func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error
	begin := func(table string, write bool) step {
		return func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error {
			if write {
				failWritesTo(t, harness, table)
			} else {
				failReadsFrom(t, harness, table)
			}
			return service.BeginPhoneVerification(t.Context(), "", cookie)
		}
	}
	complete := func(table string, write bool) step {
		return func(t *testing.T, service *Service, harness *authnRepositoryHarness, cookie string) error {
			require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
			require.NoError(t, service.deliverPendingNotifications(t.Context()))
			code := recorderFor(t, service).last(t).Code
			if write {
				failWritesTo(t, harness, table)
			} else {
				failReadsFrom(t, harness, table)
			}
			return service.CompletePhoneVerification(t.Context(), "", cookie, code)
		}
	}

	for name, run := range map[string]step{
		"begin reads the principal":     begin("iam_principals", false),
		"begin writes the code":         begin("iam_phone_verification_codes", true),
		"begin writes the outbox":       begin("iam_notification_outbox", true),
		"complete reads the code":       complete("iam_phone_verification_codes", false),
		"complete reads the principal":  complete("iam_principals", false),
		"complete writes the code":      complete("iam_phone_verification_codes", true),
		"complete writes the principal": complete("iam_principals", true),
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, recorder := newPhoneVerificationService(t)
			recorders[service] = recorder
			seedPrincipalWithPhone(t, harness, 720, "ulla", "+8613800138000")
			cookie := signIn(t, service, "ulla")
			err := run(t, service, harness, cookie)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrInvalidPhoneVerification)
			assert.NotErrorIs(t, err, authnext.ErrPhoneRequired)
			assert.NotErrorIs(t, err, authnext.ErrPhoneVerificationDisabled)
		})
	}
}
