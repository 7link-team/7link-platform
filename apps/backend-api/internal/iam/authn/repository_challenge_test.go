package authn

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepositoryMFAStateAndSingleUseFactors(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedCredential(t, 1, "hash")
	now := repositoryTestNow

	require.NoError(t, harness.repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
		PrincipalID: 1, SecretCiphertext: "first", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}))
	require.NoError(t, harness.repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
		PrincipalID: 1, SecretCiphertext: "second", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))
	enrollment, err := harness.repository.activeMFAEnrollment(t.Context(), 1, now)
	require.NoError(t, err)
	assert.Equal(t, "second", enrollment.SecretCiphertext)

	codes := []*model.RecoveryCode{
		{PrincipalID: 1, CodeHash: "a", CreatedAt: now},
		{PrincipalID: 1, CodeHash: "b", CreatedAt: now},
	}
	require.NoError(t, harness.repository.replaceRecoveryCodes(t.Context(), 1, codes))
	count, err := harness.repository.countAvailableRecoveryCodes(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	consumed, err := harness.repository.consumeRecoveryCode(t.Context(), 1, []string{"a"}, now)
	require.NoError(t, err)
	assert.True(t, consumed)
	consumed, err = harness.repository.consumeRecoveryCode(t.Context(), 1, []string{"a"}, now)
	require.NoError(t, err)
	assert.False(t, consumed)

	challenge := &model.MFAChallenge{
		IDHash: "challenge", PrincipalID: 1, ReturnURL: "/", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	require.NoError(t, harness.repository.createMFAChallenge(t.Context(), challenge, now))
	loaded, err := harness.repository.getMFAChallenge(t.Context(), "challenge")
	require.NoError(t, err)
	assert.Zero(t, loaded.Attempts)
	changed, err := harness.repository.failMFAChallenge(t.Context(), "challenge", 0, 2, now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.failMFAChallenge(t.Context(), "challenge", 0, 2, now)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = harness.repository.failMFAChallenge(t.Context(), "challenge", 1, 2, now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.consumeMFAChallenge(t.Context(), "challenge", now)
	require.NoError(t, err)
	assert.False(t, changed)

	require.NoError(t, harness.repository.createMFAChallenge(t.Context(), &model.MFAChallenge{
		IDHash: "consumable", PrincipalID: 1, ReturnURL: "/", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}, now))
	assertSingleWinner(t, func() (bool, error) {
		return harness.repository.consumeMFAChallenge(t.Context(), "consumable", now)
	})

	require.NoError(t, harness.repository.disableMFA(t.Context(), 1, now))
	require.NoError(t, harness.repository.purgeMFAState(t.Context(), 1))
	require.NoError(t, harness.repository.deleteMFAEnrollment(t.Context(), 1))
	count, err = harness.repository.countAvailableRecoveryCodes(t.Context(), 1)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestRepositoryExpiredChallengesCannotFailOrConsume(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	require.NoError(t, harness.db.Table("sl_iam_mfa_challenges").Create(&model.MFAChallenge{
		IDHash: "expired-mfa", PrincipalID: 1, ReturnURL: "/", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}).Error)
	require.NoError(t, harness.db.Table("sl_iam_stepup_challenges").Create(&model.StepUpChallenge{
		IDHash: "expired-step-up", PrincipalID: 1, SessionHash: "session",
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}).Error)

	checks := []struct {
		name string
		call func() (bool, error)
	}{
		{"fail MFA", func() (bool, error) {
			return harness.repository.failMFAChallenge(t.Context(), "expired-mfa", 0, 2, now)
		}},
		{"consume MFA", func() (bool, error) { return harness.repository.consumeMFAChallenge(t.Context(), "expired-mfa", now) }},
		{"fail step-up", func() (bool, error) {
			return harness.repository.failStepUpChallenge(t.Context(), "expired-step-up", 0, 2, now)
		}},
		{"consume step-up", func() (bool, error) {
			return harness.repository.consumeStepUpChallenge(t.Context(), "expired-step-up", 1, "session", now)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			changed, err := check.call()
			require.NoError(t, err)
			assert.False(t, changed)
		})
	}

	var attempts int
	var consumedAt *time.Time
	require.NoError(t, harness.db.Table("sl_iam_mfa_challenges").
		Select("attempts, consumed_at").Where("id_hash = ?", "expired-mfa").Row().Scan(&attempts, &consumedAt))
	assert.Zero(t, attempts)
	assert.Nil(t, consumedAt)
	require.NoError(t, harness.db.Table("sl_iam_stepup_challenges").
		Select("attempts, consumed_at").Where("id_hash = ?", "expired-step-up").Row().Scan(&attempts, &consumedAt))
	assert.Zero(t, attempts)
	assert.Nil(t, consumedAt)
}

func TestRepositoryReplacementTransactionsRollBackForEveryWriterClone(t *testing.T) {
	for _, pinWriter := range []bool{false, true} {
		t.Run(fmt.Sprintf("pinned=%t", pinWriter), func(t *testing.T) {
			harness := newAuthnRepositoryHarness(t, "sl_")
			harness.seedPrincipal(t, 1, "alice")
			now := repositoryTestNow
			require.NoError(t, harness.repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
				PrincipalID: 1, SecretCiphertext: "original", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			}))
			require.NoError(t, harness.db.Exec(`
				CREATE TRIGGER sl_deny_mfa_enrollment_insert
				BEFORE INSERT ON sl_iam_mfa_enrollments
				BEGIN SELECT RAISE(ABORT, 'enrollment insert denied'); END
			`).Error)
			repository := harness.repository
			if pinWriter {
				var err error
				repository, err = repository.pinWriter()
				require.NoError(t, err)
			}

			err := repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
				PrincipalID: 1, SecretCiphertext: "replacement", CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
			})
			assert.ErrorContains(t, err, "enrollment insert denied")
			stored, err := harness.repository.activeMFAEnrollment(t.Context(), 1, now)
			require.NoError(t, err)
			assert.Equal(t, "original", stored.SecretCiphertext)
		})
	}
}

func TestRepositoryRecoveryCodeReplacementRejectsEveryForeignOwner(t *testing.T) {
	for name, rows := range map[string][]*model.RecoveryCode{
		"foreign first": {{PrincipalID: 2, CodeHash: "foreign-first", CreatedAt: repositoryTestNow}},
		"foreign later": {
			{PrincipalID: 1, CodeHash: "valid", CreatedAt: repositoryTestNow},
			{PrincipalID: 2, CodeHash: "foreign-later", CreatedAt: repositoryTestNow},
		},
		"nil row": {nil},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newAuthnRepositoryHarness(t, "sl_")
			harness.seedPrincipal(t, 1, "alice")
			harness.seedPrincipal(t, 2, "bob")
			require.NoError(t, harness.repository.replaceRecoveryCodes(t.Context(), 1, []*model.RecoveryCode{
				{PrincipalID: 1, CodeHash: "original", CreatedAt: repositoryTestNow},
			}))
			err := harness.repository.replaceRecoveryCodes(t.Context(), 1, rows)
			assert.ErrorIs(t, err, errRecoveryCodePrincipalMismatch)
			count, countErr := harness.repository.countAvailableRecoveryCodes(t.Context(), 1)
			require.NoError(t, countErr)
			assert.Equal(t, int64(1), count)
			count, countErr = harness.repository.countAvailableRecoveryCodes(t.Context(), 2)
			require.NoError(t, countErr)
			assert.Zero(t, count)
		})
	}
}

func TestRepositoryPurgeMFAStateRollsBackEveryTableOnFailure(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	require.NoError(t, harness.repository.replaceMFAEnrollment(t.Context(), &model.MFAEnrollment{
		PrincipalID: 1, SecretCiphertext: "secret", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}))
	require.NoError(t, harness.repository.replaceRecoveryCodes(t.Context(), 1, []*model.RecoveryCode{
		{PrincipalID: 1, CodeHash: "code", CreatedAt: now},
	}))
	require.NoError(t, harness.repository.createMFAChallenge(t.Context(), &model.MFAChallenge{
		IDHash: "challenge", PrincipalID: 1, ReturnURL: "/", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, now))
	require.NoError(t, harness.db.Exec(`
		CREATE TRIGGER sl_deny_recovery_code_delete
		BEFORE DELETE ON sl_iam_recovery_codes
		BEGIN SELECT RAISE(ABORT, 'recovery delete denied'); END
	`).Error)

	err := harness.repository.purgeMFAState(t.Context(), 1)
	assert.ErrorContains(t, err, "recovery delete denied")
	for table := range map[string]struct{}{
		"sl_iam_mfa_enrollments": {}, "sl_iam_recovery_codes": {}, "sl_iam_mfa_challenges": {},
	} {
		var count int64
		require.NoError(t, harness.db.Table(table).Where("principal_id = ?", 1).Count(&count).Error)
		assert.Equal(t, int64(1), count, table)
	}
}

func TestRepositoryPasskeysArePrincipalScopedAndCountersAreCAS(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "bob")
	now := repositoryTestNow

	user, err := harness.repository.ensurePasskeyUser(t.Context(), &model.PasskeyUser{
		PrincipalID: 1, UserHandle: "alice-handle", CreatedAt: now,
	})
	require.NoError(t, err)
	assert.Equal(t, "alice-handle", user.UserHandle)
	user, err = harness.repository.ensurePasskeyUser(t.Context(), &model.PasskeyUser{
		PrincipalID: 1, UserHandle: "ignored", CreatedAt: now,
	})
	require.NoError(t, err)
	assert.Equal(t, "alice-handle", user.UserHandle)
	_, err = harness.repository.getPasskeyUser(t.Context(), 1)
	require.NoError(t, err)
	_, err = harness.repository.findPasskeyUserByHandle(t.Context(), 1, "alice-handle")
	require.NoError(t, err)

	created, err := harness.repository.createPasskey(t.Context(), &model.Passkey{
		IDHash: "passkey", PrincipalID: 1, Name: "Laptop", CredentialCiphertext: "cipher",
		CreatedAt: now, UpdatedAt: now,
	}, 1)
	require.NoError(t, err)
	assert.True(t, created)
	created, err = harness.repository.createPasskey(t.Context(), &model.Passkey{
		IDHash: "second", PrincipalID: 1, Name: "Phone", CredentialCiphertext: "cipher",
		CreatedAt: now, UpdatedAt: now,
	}, 1)
	require.NoError(t, err)
	assert.False(t, created)
	count, err := harness.repository.countPasskeys(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	rows, err := harness.repository.listPasskeys(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = harness.repository.getPasskey(t.Context(), "passkey")
	require.NoError(t, err)

	updated, err := harness.repository.updatePasskeyCounter(
		t.Context(), "passkey", 0, 1, "cipher", "new-cipher", now.Add(time.Minute),
	)
	require.NoError(t, err)
	assert.True(t, updated)
	updated, err = harness.repository.updatePasskeyCounter(
		t.Context(), "passkey", 0, 2, "cipher", "stale", now.Add(2*time.Minute),
	)
	require.NoError(t, err)
	assert.False(t, updated)
	updated, err = harness.repository.storePasskeyCloneWarning(
		t.Context(), "passkey", "new-cipher", "warning", now.Add(3*time.Minute),
	)
	require.NoError(t, err)
	assert.True(t, updated)
	renamed, changed, err := harness.repository.renamePasskey(t.Context(), 2, "passkey", "Stolen", now)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, renamed)
	renamed, changed, err = harness.repository.renamePasskey(t.Context(), 1, "passkey", "Security key", now)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "Security key", renamed.Name)
	deleted, err := harness.repository.deletePasskey(t.Context(), 2, "passkey")
	require.NoError(t, err)
	assert.False(t, deleted)
	deleted, err = harness.repository.deletePasskey(t.Context(), 1, "passkey")
	require.NoError(t, err)
	assert.True(t, deleted)
}

func TestRepositoryPasskeyAndStepUpChallengesAreSingleUse(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow

	require.NoError(t, harness.repository.createPasskeyChallenge(t.Context(), &model.PasskeyChallenge{
		IDHash: "passkey-challenge", Kind: "registration", PrincipalID: guidPointer(1), SessionData: "session",
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}, now))
	challenge, err := harness.repository.getPasskeyChallenge(t.Context(), "passkey-challenge", "registration", 1)
	require.NoError(t, err)
	assert.Equal(t, "session", challenge.SessionData)
	changed, err := harness.repository.consumePasskeyChallenge(t.Context(), "passkey-challenge", "registration", 2, now)
	require.NoError(t, err)
	assert.False(t, changed)
	assertSingleWinner(t, func() (bool, error) {
		return harness.repository.consumePasskeyChallenge(t.Context(), "passkey-challenge", "registration", 1, now)
	})

	stepUp := &model.StepUpChallenge{
		IDHash: "step-up", PrincipalID: 1, SessionHash: "session", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	require.NoError(t, harness.repository.createStepUpChallenge(t.Context(), stepUp, now))
	loaded, err := harness.repository.getStepUpChallenge(t.Context(), "step-up")
	require.NoError(t, err)
	assert.Zero(t, loaded.Attempts)
	changed, err = harness.repository.failStepUpChallenge(t.Context(), "step-up", 0, 3, now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.failStepUpChallenge(t.Context(), "step-up", 0, 3, now)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = harness.repository.consumeStepUpChallenge(t.Context(), "step-up", 2, "session", now)
	require.NoError(t, err)
	assert.False(t, changed)
	assertSingleWinner(t, func() (bool, error) {
		return harness.repository.consumeStepUpChallenge(t.Context(), "step-up", 1, "session", now)
	})
}

func assertSingleWinner(t *testing.T, operation func() (bool, error)) {
	t.Helper()
	const contenders = 8
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errs := make(chan error, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			won, err := operation()
			results <- won
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, winners)
}

func guidPointer(value guid.ID) *guid.ID { return &value }

func TestRepositoryChallengeCleanupRemovesOnlyExpiredOrConsumed(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	consumedAt := now.Add(-time.Minute)
	for _, row := range []*model.PasskeyChallenge{
		{IDHash: "active", Kind: "login", SessionData: "a", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{IDHash: "expired", Kind: "login", SessionData: "b", CreatedAt: now, ExpiresAt: now.Add(-time.Minute)},
		{IDHash: "consumed", Kind: "login", SessionData: "c", CreatedAt: now, ExpiresAt: now.Add(time.Hour), ConsumedAt: &consumedAt},
	} {
		require.NoError(t, harness.db.Table("sl_iam_passkey_challenges").Create(row).Error)
	}
	require.NoError(t, harness.repository.createPasskeyChallenge(t.Context(), &model.PasskeyChallenge{
		IDHash: "new", Kind: "login", SessionData: "new", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, now))
	for _, id := range []string{"active", "new"} {
		_, err := harness.repository.getPasskeyChallenge(t.Context(), id, "login", 0)
		require.NoError(t, err)
	}
	for _, id := range []string{"expired", "consumed"} {
		_, err := harness.repository.getPasskeyChallenge(t.Context(), id, "login", 0)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "%s should be cleaned", id)
	}
}
