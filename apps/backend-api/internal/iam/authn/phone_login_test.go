package authn

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newPhoneLoginService turns on signing in by number as well as proving one.
func newPhoneLoginService(t *testing.T) (*Service, *authnRepositoryHarness, *notificationRecorder) {
	t.Helper()
	service, harness, recorder := newPhoneVerificationService(t)
	service.cfg.PhoneVerification.LoginEnabled = true
	return service, harness, recorder
}

// seedProvenPhone gives a principal a number it has already proven, which is the
// only kind that signs anybody in.
func seedProvenPhone(t *testing.T, harness *authnRepositoryHarness, id guid.ID, login, phone string) {
	t.Helper()
	seedPrincipalWithPhone(t, harness, id, login, phone)
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", id).Update("phone_verified", true).Error)
}

func TestSigningInWithACodeSentToAProvenNumber(t *testing.T) {
	service, harness, recorder := newPhoneLoginService(t)
	principalID := guid.ID(701)
	seedProvenPhone(t, harness, principalID, "alice", "+8613800138000")

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+86 138 0013 8000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	assert.Equal(t, phoneLoginNotification, payload.Type)
	assert.Equal(t, "+8613800138000", payload.Recipient)
	require.Len(t, payload.Code, 6)

	result, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", payload.Code, "")
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
	require.NotEmpty(t, result.SessionID)

	// The session says what proved it, which is what an audit and a step-up policy
	// read to decide how far it reaches.
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(result.SessionID))
	require.NoError(t, err)
	assert.Equal(t, principalID, claims.PrincipalID)
	assert.Equal(t, []string{"sms"}, claims.AMR)
	assert.Equal(t, 1, claims.ACR, "one factor, the same as a password")

	// The code is spent.
	_, err = service.CompletePhoneLogin(t.Context(), "+8613800138000", payload.Code, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestAnUnprovenNumberSignsNobodyIn(t *testing.T) {
	// A number nobody proved is a string somebody typed into their own profile,
	// and anybody can type any number.
	service, harness, recorder := newPhoneLoginService(t)
	seedPrincipalWithPhone(t, harness, 702, "bob", "+8613800138000")

	before := recorder.count()
	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count(), "no code is sent to an unproven number")

	_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", "123456", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestANumberTwoAccountsProvedSignsNeitherIn(t *testing.T) {
	// Carriers reissue numbers, so two accounts can each have proved the same one.
	// It then identifies nobody, and the safe answer is the one an ambiguous
	// recovery identifier already gets.
	service, harness, recorder := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 703, "carol", "+8613800138000")
	seedProvenPhone(t, harness, 704, "dave", "+8613800138000")

	before := recorder.count()
	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count())

	_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", "123456", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestAskingForASignInCodeNeverSaysWhetherTheNumberIsKnown(t *testing.T) {
	// The answer is read by whoever typed the number, and they are not always its
	// owner. A different answer for a known number would be a way to enumerate
	// which numbers have accounts.
	service, harness, _ := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 705, "erin", "+8613800138000")

	for _, typed := range []string{
		"+8613800138000", // reaches an account
		"+14155550132",   // reaches nobody
		"not a number",   // is not a number
		"",               // is nothing
	} {
		assert.NoError(t, service.BeginPhoneLogin(t.Context(), typed), "typed %q", typed)
	}
}

func TestGuessingASignInCodeTripsTheSameLockAsGuessingAPassword(t *testing.T) {
	// Six digits is worth guessing if guessing is free. The code has its own
	// ceiling, and each wrong one also counts against the account lock, so the
	// two ways in cannot be attacked independently.
	service, harness, recorder := newPhoneLoginService(t)
	service.cfg.Password.MaxFailedAttempts = 3
	service.cfg.Password.LockDuration = time.Hour
	seedProvenPhone(t, harness, 706, "frank", "+8613800138000")

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)
	wrong := "000000"
	if payload.Code == wrong {
		wrong = "111111"
	}

	for attempt := 0; attempt < 3; attempt++ {
		_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", wrong, "")
		assert.ErrorIs(t, err, authnext.ErrInvalidCredentials, "attempt %d", attempt)
	}

	// Locked out, the right code no longer works either -- and a password would
	// not work now, which is the point of sharing the lock.
	_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", payload.Code, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "frank", "correct horse battery staple", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)

	// And a locked account is not sent a fresh way in.
	before := recorder.count()
	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count())
}

// TestACodeAnswersOnlyTheFlowItWasSentFor pins the mechanism rather than a
// reachable attack. Through the API the two purposes cannot both be live for one
// account -- a verify code exists only while the number is unproven and a sign-in
// code only once it is proven, and the act that flips that flag consumes the
// verify code. The purpose is part of the key so the property holds locally,
// without depending on two flags elsewhere staying in step, and so a direct
// database write or a future flow cannot make one code answer the other.
func TestACodeAnswersOnlyTheFlowItWasSentFor(t *testing.T) {
	service, harness, _ := newPhoneLoginService(t)
	principalID := guid.ID(707)
	seedProvenPhone(t, harness, principalID, "grace", "+8613800138000")
	now := repositoryTestNow

	for _, purpose := range []string{phoneCodeVerify, phoneCodeLogin} {
		require.NoError(t, service.repo.replacePhoneCode(t.Context(), &model.PhoneVerificationCode{
			PrincipalID: principalID, Purpose: purpose, Phone: "+8613800138000",
			Code:      map[string]string{phoneCodeVerify: "111111", phoneCodeLogin: "222222"}[purpose],
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}))
	}

	// Both live at once, each answering its own flow and no other.
	verify, err := service.repo.activePhoneCode(t.Context(), principalID, phoneCodeVerify, now)
	require.NoError(t, err)
	assert.Equal(t, "111111", verify.Code)
	login, err := service.repo.activePhoneCode(t.Context(), principalID, phoneCodeLogin, now)
	require.NoError(t, err)
	assert.Equal(t, "222222", login.Code)

	// Spending one leaves the other alone.
	spent, err := service.repo.consumePhoneCode(t.Context(), principalID, phoneCodeLogin, maxCodeAttempts, now)
	require.NoError(t, err)
	require.True(t, spent)
	_, err = service.repo.activePhoneCode(t.Context(), principalID, phoneCodeLogin, now)
	assert.Error(t, err, "the sign-in code is spent")
	verify, err = service.repo.activePhoneCode(t.Context(), principalID, phoneCodeVerify, now)
	require.NoError(t, err)
	assert.Equal(t, "111111", verify.Code, "the confirm-your-number code is untouched")

	// And the sign-in flow refuses the other purpose's code.
	_, err = service.CompletePhoneLogin(t.Context(), "+8613800138000", "111111", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestPhoneSignInMeetsTheSameSecondFactorGate(t *testing.T) {
	// A second way in that skipped the second factor would be a way around it.
	service, harness, recorder := newPhoneLoginService(t)
	principalID := guid.ID(709)
	seedProvenPhone(t, harness, principalID, "ivan", "+8613800138000")
	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_credentials").
		Where("principal_id = ?", principalID).Update("mfa_required", true).Error)

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	result, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", recorder.last(t).Code, "")
	require.NoError(t, err)
	assert.Equal(t, "mfa_enrollment_required", result.Status,
		"the policy asks for a factor this account has not enrolled, exactly as it does after a password")
	assert.NotEqual(t, "authenticated", result.Status)
}

func TestPhoneSignInIsRefusedWhereItIsNotTurnedOn(t *testing.T) {
	// Proving a number is useful on its own, so a deployment can keep numbers on
	// file without making them a way in.
	service, harness, _ := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 710, "judy", "+8613800138000")
	service.cfg.PhoneVerification.LoginEnabled = false

	assert.ErrorIs(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"), authnext.ErrPhoneLoginDisabled)
	_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", "123456", "")
	assert.ErrorIs(t, err, authnext.ErrPhoneLoginDisabled)
}

func TestASignInCodeIsRefusedOnceTheNumberChanged(t *testing.T) {
	// The code proves the number it was sent to. Changed in between, it proves
	// nothing about the account now -- and changing a proven number unverifies it,
	// so the number stops being a way in at all.
	service, harness, recorder := newPhoneLoginService(t)
	principalID := guid.ID(711)
	seedProvenPhone(t, harness, principalID, "karl", "+8613800138000")
	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	payload := recorder.last(t)

	require.NoError(t, harness.db.Table(harness.runtime.TablePrefix()+"iam_principals").
		Where("id = ?", principalID).Updates(map[string]any{
		"phone": "+14155550132", "phone_verified": false,
	}).Error)

	_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", payload.Code, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestPhoneSignInRefusesWhatCannotBeANumberOrACode(t *testing.T) {
	// Checked before anything is read, so a request carrying something else costs
	// a comparison rather than a statement -- and a malformed code is refused the
	// same way a wrong one is, so neither says which it was.
	service, harness, _ := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 712, "lena", "+8613800138000")

	for _, test := range []struct{ phone, code string }{
		{"13800138000", "123456"},     // no country code
		{"not a number", "123456"},    // not a number
		{"", "123456"},                // nothing
		{"+8613800138000", "12345"},   // too short
		{"+8613800138000", "1234567"}, // too long
		{"+8613800138000", "12a456"},  // not digits
		{"+8613800138000", ""},        // nothing
	} {
		_, err := service.CompletePhoneLogin(t.Context(), test.phone, test.code, "")
		assert.ErrorIs(t, err, authnext.ErrInvalidCredentials, "%q / %q", test.phone, test.code)
	}
}

func TestPhoneSignInRefusesAReturnURLItWasNotToldAbout(t *testing.T) {
	// An unlisted return URL is the caller getting it wrong, not somebody failing
	// to sign in, and it must not read as a bad code.
	service, harness, _ := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 713, "mona", "+8613800138000")

	_, err := service.CompletePhoneLogin(
		t.Context(), "+8613800138000", "123456", "https://elsewhere.example.test/",
	)
	assert.ErrorIs(t, err, authnext.ErrReturnURL)
	assert.NotErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestPhoneSignInStorageFaultsAreNotReportedAsBadCredentials(t *testing.T) {
	// Told the number and code do not sign anybody in, somebody tries again and
	// gets the same answer. A fault has to reach the path that is watched.
	for name, run := range map[string]func(t *testing.T, service *Service, harness *authnRepositoryHarness) error{
		"queueing a code": func(t *testing.T, service *Service, harness *authnRepositoryHarness) error {
			require.NoError(t, harness.db.Exec(
				"DROP TABLE "+harness.runtime.TablePrefix()+"iam_phone_verification_codes").Error)
			return service.BeginPhoneLogin(t.Context(), "+8613800138000")
		},
		"spending a code": func(t *testing.T, service *Service, harness *authnRepositoryHarness) error {
			require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
			require.NoError(t, harness.db.Exec(
				"DROP TABLE "+harness.runtime.TablePrefix()+"iam_phone_verification_codes").Error)
			_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", "123456", "")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, _ := newPhoneLoginService(t)
			seedProvenPhone(t, harness, 714, "omar", "+8613800138000")
			err := run(t, service, harness)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrInvalidCredentials)
			assert.NotErrorIs(t, err, authnext.ErrPhoneLoginDisabled)
		})
	}
}

func TestSomebodyWhoNeverHadAPasswordCanStillSignInByNumber(t *testing.T) {
	// A principal that only ever arrived through a federated provider has no
	// credential row at all. It has no password to lock and no second factor
	// recorded, so the absence is what a zero-valued credential already says --
	// and a proven number is the one way in such an account has here.
	service, harness, recorder := newPhoneLoginService(t)
	principalID := guid.ID(715)
	seedProvenPhone(t, harness, principalID, "pia", "+8613800138000")
	require.NoError(t, harness.db.Exec(
		"DELETE FROM "+harness.runtime.TablePrefix()+"iam_credentials WHERE principal_id = ?",
		principalID).Error)

	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	result, err := service.CompletePhoneLogin(
		t.Context(), "+8613800138000", recorder.last(t).Code, "",
	)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
	require.NotEmpty(t, result.SessionID)
}

func TestProvingANumberNeedsASessionWhosePrincipalIsStillThere(t *testing.T) {
	// The lock re-reads the principal inside the transaction, so a session whose
	// principal went away between is a dead session rather than a server fault.
	service, harness, _ := newPhoneVerificationService(t)
	principalID := guid.ID(716)
	seedPrincipalWithPhone(t, harness, principalID, "quinn", "+8613800138000")
	cookie := signIn(t, service, "quinn")

	// The session still resolves because the token carries the subject, but the
	// row the lock wants is gone.
	require.NoError(t, harness.db.Exec(
		"UPDATE "+harness.runtime.TablePrefix()+"iam_principals SET activation_required = ? WHERE id = ?",
		true, principalID).Error)

	err := service.BeginPhoneVerification(t.Context(), "", cookie)
	require.Error(t, err)
	assert.NotErrorIs(t, err, authnext.ErrPhoneRequired)
}

func TestAnAuditThatCannotBeWrittenLeavesTheNumberUnproven(t *testing.T) {
	// The record of what happened is part of what happened. If it cannot be
	// written the number is not promoted and the code is not spent, so the person
	// can try again once the chain is writable rather than being left with a
	// number marked proven that nothing recorded.
	service, harness, recorder := newPhoneVerificationService(t)
	principalID := guid.ID(717)
	seedPrincipalWithPhone(t, harness, principalID, "rosa", "+8613800138000")
	cookie := signIn(t, service, "rosa")
	require.NoError(t, service.BeginPhoneVerification(t.Context(), "", cookie))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	code := recorder.last(t).Code

	harness.auditErr = errors.New("audit unavailable")
	err := service.CompletePhoneVerification(t.Context(), "", cookie, code)
	assert.ErrorContains(t, err, "audit unavailable")
	assert.False(t, harness.principal(t, principalID).PhoneVerified)

	// The code survived, so the same one works once the chain does.
	harness.auditErr = nil
	require.NoError(t, service.CompletePhoneVerification(t.Context(), "", cookie, code))
	assert.True(t, harness.principal(t, principalID).PhoneVerified)
}

func TestAnAuditThatCannotBeWrittenQueuesNoSignInCode(t *testing.T) {
	// Queueing a code and recording that it was queued are one act: a message
	// nobody can account for must not go out.
	service, harness, recorder := newPhoneLoginService(t)
	seedProvenPhone(t, harness, 718, "sven", "+8613800138000")

	harness.auditErr = errors.New("audit unavailable")
	before := recorder.count()
	err := service.BeginPhoneLogin(t.Context(), "+8613800138000")
	require.Error(t, err)
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Equal(t, before, recorder.count(), "no message left for an act nothing recorded")

	harness.auditErr = nil
	require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
	require.NoError(t, service.deliverPendingNotifications(t.Context()))
	assert.Greater(t, recorder.count(), before)
}

// failReadsFrom makes every read of one table fail while leaving its rows in
// place. Dropping the table instead would cascade into the rows under test.
func failReadsFrom(t *testing.T, harness *authnRepositoryHarness, table string) {
	t.Helper()
	name := harness.runtime.TablePrefix() + table
	failQueriesOn(t, harness, func(db *gorm.DB) bool { return db.Statement.Table == name })
}

// failPrincipalReads makes every read of the principal table fail, in both forms
// identity sends them. It cannot go through failReadsFrom: the readers that
// resolve somebody by login name or by number alias that table, and gorm records
// the alias in place of the name, so matching a prefixed name injects nothing
// into them and the test passes having proved nothing.
func failPrincipalReads(t *testing.T, harness *authnRepositoryHarness) {
	t.Helper()
	name := harness.runtime.TablePrefix() + "iam_principals"
	failQueriesOn(t, harness, func(db *gorm.DB) bool {
		return db.Statement.Table == "principal" || db.Statement.Table == name
	})
}

func failQueriesOn(t *testing.T, harness *authnRepositoryHarness, match func(*gorm.DB) bool) {
	t.Helper()
	require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register(
		"test:fail_reads", func(db *gorm.DB) {
			if match(db) {
				db.AddError(errors.New("storage unavailable"))
			}
		}))
}

func TestNoStorageFaultOnTheSignInPathIsReportedAsARefusal(t *testing.T) {
	// The rule this module already keeps, applied to the newest way in: a missing
	// row is a refusal, a failed read or write is not. Told their number and code
	// do not sign anybody in, somebody tries again and gets the same answer, and
	// nothing reaches the 5xx that is actually watched.
	type step func(t *testing.T, service *Service, harness *authnRepositoryHarness) error
	type injector func(t *testing.T, harness *authnRepositoryHarness)
	reads := func(table string) injector {
		return func(t *testing.T, harness *authnRepositoryHarness) { failReadsFrom(t, harness, table) }
	}
	writes := func(table string) injector {
		return func(t *testing.T, harness *authnRepositoryHarness) { failWritesTo(t, harness, table) }
	}
	principalReads := func(t *testing.T, harness *authnRepositoryHarness) { failPrincipalReads(t, harness) }
	begin := func(fail injector) step {
		return func(t *testing.T, service *Service, harness *authnRepositoryHarness) error {
			fail(t, harness)
			return service.BeginPhoneLogin(t.Context(), "+8613800138000")
		}
	}
	// A wrong code is what reaches the counters, so the step that watches them
	// has to type one; the right code never touches that path at all.
	complete := func(fail injector, code string) step {
		return func(t *testing.T, service *Service, harness *authnRepositoryHarness) error {
			require.NoError(t, service.BeginPhoneLogin(t.Context(), "+8613800138000"))
			require.NoError(t, service.deliverPendingNotifications(t.Context()))
			if code == "" {
				code = recorderFor(t, service).last(t).Code
			}
			fail(t, harness)
			_, err := service.CompletePhoneLogin(t.Context(), "+8613800138000", code, "")
			return err
		}
	}

	for name, run := range map[string]step{
		"begin reads the principal":      begin(principalReads),
		"begin reads the credential":     begin(reads("iam_credentials")),
		"begin writes the code":          begin(writes("iam_phone_verification_codes")),
		"begin writes the outbox":        begin(writes("iam_notification_outbox")),
		"complete reads the principal":   complete(principalReads, ""),
		"complete reads the code":        complete(reads("iam_phone_verification_codes"), ""),
		"complete writes the code":       complete(writes("iam_phone_verification_codes"), ""),
		"complete writes the session":    complete(writes("iam_sessions"), ""),
		"complete reads the credential":  complete(reads("iam_credentials"), ""),
		"complete writes the credential": complete(writes("iam_credentials"), ""),
		// Both counters a wrong code bumps: the code's own attempt count and the
		// lockout a wrong password shares. Neither may fail quietly -- a guess
		// that was not counted is a guess that did not cost anything.
		"wrong code cannot count the attempt": complete(writes("iam_phone_verification_codes"), "000000"),
		"wrong code cannot count the failure": complete(writes("iam_credentials"), "000000"),
	} {
		t.Run(name, func(t *testing.T) {
			service, harness, recorder := newPhoneLoginService(t)
			recorders[service] = recorder
			seedProvenPhone(t, harness, 719, "tess", "+8613800138000")
			err := run(t, service, harness)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrInvalidCredentials)
			assert.NotErrorIs(t, err, authnext.ErrPhoneLoginDisabled)
		})
	}
}

// recorders lets a table-driven step reach the recorder its own fixture made,
// without threading it through every closure signature.
var recorders = map[*Service]*notificationRecorder{}

func recorderFor(t *testing.T, service *Service) *notificationRecorder {
	t.Helper()
	recorder, ok := recorders[service]
	require.True(t, ok)
	return recorder
}
