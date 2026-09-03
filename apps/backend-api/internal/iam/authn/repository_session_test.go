package authn

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRepositorySessionOwnershipExpiryAndRevocation(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "bob")
	now := repositoryTestNow
	rows := []*model.Session{
		testSession("current", 1, now, now.Add(time.Hour), now.Add(8*time.Hour)),
		testSession("other", 1, now.Add(time.Minute), now.Add(time.Hour), now.Add(8*time.Hour)),
		testSession("bob", 2, now, now.Add(time.Hour), now.Add(8*time.Hour)),
		testSession("expired", 1, now, now.Add(-time.Minute), now.Add(8*time.Hour)),
	}
	for _, row := range rows {
		require.NoError(t, harness.repository.createSession(t.Context(), row))
	}

	current, err := harness.repository.activeSession(t.Context(), "current", now)
	require.NoError(t, err)
	assert.Equal(t, "current", current.IDHash)
	_, err = harness.repository.activeSession(t.Context(), "expired", now)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	touchedAt := now.Add(2 * time.Minute)
	touched, err := harness.repository.touchSession(t.Context(), "current", touchedAt, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.True(t, touched)
	current, err = harness.repository.activeSession(t.Context(), "current", now)
	require.NoError(t, err)
	assert.Equal(t, touchedAt, current.LastSeenAt)

	active, err := harness.repository.listActiveSessions(t.Context(), 1, now)
	require.NoError(t, err)
	require.Len(t, active, 2)
	assert.Equal(t, "current", active[0].IDHash)

	changed, err := harness.repository.revokeSession(t.Context(), 2, "other", now)
	require.NoError(t, err)
	assert.False(t, changed, "principal B cannot revoke principal A's session")
	changed, err = harness.repository.revokeSession(t.Context(), 1, "other", now)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.repository.revokeSession(t.Context(), 1, "other", now)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRepositorySessionMutationsRequireActiveSessionAndCapIdleExpiry(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	revokedAt := now.Add(-time.Minute)
	rows := []*model.Session{
		testSession("active", 1, now.Add(-time.Hour), now.Add(time.Minute), now.Add(2*time.Minute)),
		testSession("idle-expired", 1, now.Add(-time.Hour), now.Add(-time.Minute), now.Add(time.Hour)),
		testSession("absolute-expired", 1, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Minute)),
		testSession("revoked", 1, now.Add(-time.Hour), now.Add(time.Hour), now.Add(2*time.Hour)),
	}
	rows[3].RevokedAt = &revokedAt
	for _, row := range rows {
		require.NoError(t, harness.repository.createSession(t.Context(), row))
	}

	for _, id := range []string{"idle-expired", "absolute-expired", "revoked"} {
		changed, err := harness.repository.touchSession(t.Context(), id, now, now.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, changed, id)
		_, _, err = harness.repository.rotateSession(
			t.Context(), 1, id, testSession("replacement-"+id, 1, now, now.Add(time.Hour), now.Add(2*time.Hour)), now,
		)
		assert.ErrorIs(t, err, gorm.ErrRecordNotFound, id)
	}

	changed, err := harness.repository.touchSession(t.Context(), "active", now, now.Add(time.Hour))
	require.NoError(t, err)
	assert.True(t, changed)
	active, err := harness.repository.activeSession(t.Context(), "active", now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(2*time.Minute), active.ExpiresAt, "idle expiry must not exceed the absolute expiry")
	var replacements int64
	require.NoError(t, harness.db.Table("sl_iam_sessions").Where("id_hash LIKE ?", "replacement-%").Count(&replacements).Error)
	assert.Zero(t, replacements)
}

func TestRepositoryConcurrentSessionRotationCommitsOneReplacement(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	require.NoError(t, harness.repository.createSession(
		t.Context(), testSession("old", 1, now, now.Add(time.Hour), now.Add(8*time.Hour)),
	))

	const contenders = 8
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errs := make(chan error, contenders)
	var sequence atomic.Int64
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			id := fmt.Sprintf("replacement-%d", sequence.Add(1))
			_, changed, err := harness.repository.rotateSession(
				t.Context(), 1, "old", testSession(id, 1, now, now.Add(time.Hour), now.Add(8*time.Hour)), now,
			)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = nil
			}
			results <- changed
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)

	winners := 0
	for changed := range results {
		if changed {
			winners++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, winners)
	var replacements int64
	require.NoError(t, harness.db.Table("sl_iam_sessions").Where("id_hash LIKE ?", "replacement-%").Count(&replacements).Error)
	assert.Equal(t, int64(1), replacements)
}

func TestRepositoryRevokesOtherAuthenticationInCallerTransaction(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	now := repositoryTestNow
	for _, id := range []string{"keep", "revoke"} {
		require.NoError(t, harness.repository.createSession(t.Context(), testSession(id, 1, now, now.Add(time.Hour), now.Add(8*time.Hour))))
	}
	require.NoError(t, harness.db.Table("sl_iam_refresh_tokens").Create(map[string]any{
		"id_hash": "refresh", "principal_id": 1,
	}).Error)

	require.NoError(t, harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		return repository.revokeOtherAuthentication(t.Context(), 1, "keep", now)
	}))
	var states []struct {
		IDHash    string
		RevokedAt *time.Time
	}
	require.NoError(t, harness.db.Table("sl_iam_sessions").
		Select("id_hash, revoked_at").Where("principal_id = ?", 1).Order("id_hash").Scan(&states).Error)
	require.Len(t, states, 2)
	assert.Nil(t, states[0].RevokedAt)
	require.NotNil(t, states[1].RevokedAt)
	var refreshRevokedAt *time.Time
	require.NoError(t, harness.db.Table("sl_iam_refresh_tokens").
		Select("revoked_at").Where("id_hash = ?", "refresh").Scan(&refreshRevokedAt).Error)
	require.NotNil(t, refreshRevokedAt)

	count, err := harness.repository.revokeSessions(t.Context(), 1, "", now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	count, err = harness.repository.revokeRefreshTokens(t.Context(), 1, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestRepositoryRotateSessionRequiresOwnedActiveSession(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	harness.seedPrincipal(t, 1, "alice")
	harness.seedPrincipal(t, 2, "bob")
	now := repositoryTestNow
	current := testSession("old", 1, now, now.Add(time.Hour), now.Add(8*time.Hour))
	require.NoError(t, harness.repository.createSession(t.Context(), current))

	err := harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		_, _, err := repository.rotateSession(t.Context(), 2, "old", testSession("bad", 2, now, now.Add(time.Hour), now.Add(8*time.Hour)), now)
		return err
	})
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var loaded *model.Session
	var changed bool
	require.NoError(t, harness.repository.runInTx(t.Context(), func(repository *Repository) error {
		var err error
		loaded, changed, err = repository.rotateSession(
			t.Context(), 1, "old", testSession("new", 1, now, now.Add(time.Hour), now.Add(8*time.Hour)), now,
		)
		return err
	}))
	assert.True(t, changed)
	assert.Equal(t, "old", loaded.IDHash)
	_, err = harness.repository.activeSession(t.Context(), "old", now)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = harness.repository.activeSession(t.Context(), "new", now)
	require.NoError(t, err)
}

func testSession(id string, principalID guid.ID, createdAt, expiresAt, absoluteExpiresAt time.Time) *model.Session {
	return &model.Session{
		IDHash: id, PrincipalID: principalID, CreatedAt: createdAt, AuthTime: createdAt,
		ACR: 1, AMR: "pwd", LastSeenAt: createdAt, ExpiresAt: expiresAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
	}
}
