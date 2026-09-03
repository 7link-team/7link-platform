package authn

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepositoryRecoveryAndVerificationTokensAreSingleUse(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	old := &model.PasswordRecoveryToken{TokenHMAC: "old", PrincipalID: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, harness.repository.createPasswordRecovery(t.Context(), old, now))
	current := &model.PasswordRecoveryToken{TokenHMAC: "current", PrincipalID: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, harness.repository.createPasswordRecovery(t.Context(), current, now))
	_, err := harness.repository.activePasswordRecovery(t.Context(), "old", now)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	loaded, err := harness.repository.activePasswordRecovery(t.Context(), "current", now)
	require.NoError(t, err)
	assert.Equal(t, "current", loaded.TokenHMAC)
	assertSingleWinner(t, func() (bool, error) {
		return harness.repository.consumePasswordRecovery(t.Context(), "current", now)
	})

	require.NoError(t, harness.repository.createPasswordRecovery(t.Context(), &model.PasswordRecoveryToken{
		TokenHMAC: "all", PrincipalID: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}, now))
	require.NoError(t, harness.repository.consumePasswordRecoveries(t.Context(), 1, now))
	_, err = harness.repository.activePasswordRecovery(t.Context(), "all", now)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	verification := &model.EmailVerificationToken{
		TokenHMAC: "verification", PrincipalID: 1, Email: "alice@example.test", Code: "123456",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, harness.repository.replaceEmailVerification(t.Context(), verification, now))
	byHMAC, err := harness.repository.activeEmailVerificationByHMAC(t.Context(), "verification", now)
	require.NoError(t, err)
	assert.Equal(t, "123456", byHMAC.Code)
	byCode, err := harness.repository.activeEmailVerificationByCode(t.Context(), "123456", now)
	require.NoError(t, err)
	assert.Equal(t, "verification", byCode.TokenHMAC)
	latest, err := harness.repository.latestEmailVerificationTime(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, now, *latest)
	assertSingleWinner(t, func() (bool, error) {
		return harness.repository.consumeEmailVerification(t.Context(), "verification", now)
	})

	second := &model.EmailVerificationToken{
		TokenHMAC: "second", PrincipalID: 1, Email: "alice@example.test", Code: "654321",
		CreatedAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, harness.repository.createEmailVerification(t.Context(), second))
	require.NoError(t, harness.repository.consumeEmailVerifications(t.Context(), 1, now.Add(2*time.Minute)))
	_, err = harness.repository.activeEmailVerificationByHMAC(t.Context(), "second", now)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestRepositoryEmailVerificationCodeCollisionFailsClosed(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "bob")
	now := repositoryTestNow
	for _, row := range []*model.EmailVerificationToken{
		{TokenHMAC: "alice-token", PrincipalID: 1, Email: "alice@example.test", Code: "123456", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{TokenHMAC: "bob-token", PrincipalID: 2, Email: "bob@example.test", Code: "123456", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		require.NoError(t, harness.repository.createEmailVerification(t.Context(), row))
	}

	row, err := harness.repository.activeEmailVerificationByCode(t.Context(), "123456", now)
	assert.ErrorIs(t, err, errAmbiguousEmailVerificationCode)
	assert.Nil(t, row)
}

func TestRepositoryNotificationOutboxClaimAndTransitions(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	now := repositoryTestNow
	rows := []*model.NotificationOutbox{
		{ID: 1, Kind: "email", Recipient: "a@example.test", PayloadCiphertext: "a", Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: 2, Kind: "email", Recipient: "b@example.test", PayloadCiphertext: "b", Status: "pending", AvailableAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now},
		{ID: 3, Kind: "email", Recipient: "c@example.test", PayloadCiphertext: "c", Status: "delivering", AvailableAt: now, LockedAt: timePointer(now.Add(-time.Hour)), CreatedAt: now, UpdatedAt: now},
		{ID: 4, Kind: "email", Recipient: "d@example.test", PayloadCiphertext: "d", Status: "delivering", AvailableAt: now, CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		require.NoError(t, harness.repository.enqueueNotification(t.Context(), row))
	}
	require.NoError(t, harness.repository.resetStaleNotifications(t.Context(), now.Add(-time.Minute)))
	ids, err := harness.repository.pendingNotificationIDs(t.Context(), now, 20)
	require.NoError(t, err)
	assert.ElementsMatch(t, []guid.ID{1, 3, 4}, ids)

	claims := make(chan *model.NotificationOutbox, 8)
	assertSingleWinner(t, func() (bool, error) {
		row, changed, err := harness.repository.claimNotification(t.Context(), 1, now)
		if changed {
			claims <- row
		}
		return changed, err
	})
	close(claims)
	claimed := <-claims
	require.NotNil(t, claimed)
	assert.Equal(t, "delivering", claimed.Status)
	assert.Equal(t, 1, claimed.Attempts)
	changed, err := harness.repository.markNotificationSent(t.Context(), 1, claimed.Attempts, now.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.markNotificationSent(t.Context(), 1, claimed.Attempts, now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, changed)

	claimed, claimedOK, err := harness.repository.claimNotification(t.Context(), 3, now)
	require.NoError(t, err)
	assert.True(t, claimedOK)
	require.NotNil(t, claimed)
	changed, err = harness.repository.markNotificationFailed(
		t.Context(), 3, claimed.Attempts, "pending", "temporary failure", now.Add(time.Minute), now,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	var failed model.NotificationOutbox
	require.NoError(t, harness.db.Table("sl_iam_notification_outbox").Where("id = ?", 3).Take(&failed).Error)
	assert.Equal(t, "pending", failed.Status)
	assert.Equal(t, "temporary failure", failed.LastError)
	assert.Nil(t, failed.LockedAt)

	reclaimed, claimedOK, err := harness.repository.claimNotification(t.Context(), 3, now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, claimedOK)
	require.NotNil(t, reclaimed)
	assert.Equal(t, 2, reclaimed.Attempts)
	changed, err = harness.repository.markNotificationFailed(
		t.Context(), 3, claimed.Attempts, "pending", "stale failure", now.Add(4*time.Minute), now.Add(3*time.Minute),
	)
	require.NoError(t, err)
	assert.False(t, changed, "an old delivery attempt cannot fail a newer lease")
	changed, err = harness.repository.markNotificationSent(t.Context(), 3, claimed.Attempts, now.Add(3*time.Minute))
	require.NoError(t, err)
	assert.False(t, changed, "an old delivery attempt cannot complete a newer lease")
	changed, err = harness.repository.markNotificationSent(t.Context(), 3, reclaimed.Attempts, now.Add(3*time.Minute))
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestRepositoryNotificationClaimRollsBackWhenClaimedRowCannotBeRead(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	now := repositoryTestNow
	require.NoError(t, harness.repository.enqueueNotification(t.Context(), &model.NotificationOutbox{
		ID: 1, Kind: "email", Recipient: "a@example.test", PayloadCiphertext: "a",
		Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}))

	readErr := errors.New("claimed row read failed")
	const callbackName = "test:fail_notification_claim_read"
	require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == "sl_iam_notification_outbox" {
			db.AddError(readErr)
		}
	}))
	claimed, changed, err := harness.repository.claimNotification(t.Context(), 1, now)
	assert.ErrorIs(t, err, readErr)
	assert.Nil(t, claimed)
	assert.False(t, changed)
	require.NoError(t, harness.db.Callback().Query().Remove(callbackName))

	var stored model.NotificationOutbox
	require.NoError(t, harness.db.Table("sl_iam_notification_outbox").Where("id = ?", 1).Take(&stored).Error)
	assert.Equal(t, "pending", stored.Status)
	assert.Zero(t, stored.Attempts)
	assert.Nil(t, stored.LockedAt)
}

func timePointer(value time.Time) *time.Time { return &value }
