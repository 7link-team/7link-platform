package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAccountCleanupHarness(t *testing.T) (*gorm.DB, *AccountObjects, *Local) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cleanup.sqlite")+"?_foreign_keys=on&_busy_timeout=10000&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	source := (&Module{}).MigrationSource()
	source.Prefix = "cleanup_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	accounts, err := NewAccountObjects(driver, "local", source.Prefix)
	require.NoError(t, err)
	return db, accounts, driver
}

func TestStagedAccountObjectSurvivesRollbackAndIsCollected(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.False(t, readable)
	rollback := errors.New("audit failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3))
		return rollback
	}), rollback)
	q := query.Bind(db, accounts.prefix)
	object, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "staged", object.State)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), intent.StorageKey)
	require.ErrorIs(t, err, ErrNotFound)
	object, err = q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "deleted", object.State)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3) }))
}

func TestLiveAvatarProtectedReplacementAndCandidateExpiry(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	first, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("one"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, first, 2, 4) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, first, 2, 3) }))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.True(t, readable)
	readable, err = accounts.AccountKeyReadable(t.Context(), db, 4, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.False(t, readable)
	second, err := accounts.Stage(t.Context(), db, 5, 3, "avatars/3/5.png", bytes.NewBufferString("two"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, second, 6, 3) }))
	candidate, err := accounts.Stage(t.Context(), db, 7, 3, "avatars/3/7.png", bytes.NewBufferString("new"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	expiry := now.Add(time.Minute)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.Register(t.Context(), tx, candidate, 8, 3, "avatar_candidate", &expiry)
	}))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Minute), 0, 100)
	require.NoError(t, err)
	for _, key := range []string{"avatars/3/1.png", "avatars/3/7.png"} {
		_, err := driver.Open(t.Context(), key)
		require.ErrorIs(t, err, ErrNotFound)
	}
	readable, err = accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/5.png", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, readable)
}

type cleanupFailureDriver struct {
	Driver
	fail bool
}

func (d *cleanupFailureDriver) Delete(ctx context.Context, key string) error {
	if d.fail {
		return errors.New("storage offline")
	}
	return d.Driver.Delete(ctx, key)
}

func TestCleanupFailureRetriesWithoutRevivingObject(t *testing.T) {
	db, accounts, local := newAccountCleanupHarness(t)
	driver := &cleanupFailureDriver{Driver: local, fail: true}
	accounts.driver = driver
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.ErrorContains(t, err, "storage offline")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3) }))
	driver.fail = false
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(3*time.Hour), 0, 100)
	require.NoError(t, err)
	q := query.Bind(db, accounts.prefix)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "released", intent.State)
}

func TestStageCollisionNeverDeletesPreexistingBytes(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	_, err := driver.Put(t.Context(), "avatars/3/1.png", bytes.NewBufferString("old"), 3)
	require.NoError(t, err)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("new"), 3, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, ErrExists)
	_, err = accounts.CollectAccountObjects(t.Context(), db, time.Now().Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	reader, err := driver.Open(t.Context(), "avatars/3/1.png")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "old", string(data))
}

func TestAbandonedLocalWriteReclaimsPendingAndKeepsTombstone(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	key := "avatars/3/1.png"
	q := query.Bind(db, accounts.prefix)
	require.NoError(t, q.AccountPreparation.WithContext(t.Context()).Create(&model.AccountPreparation{ID: 1, PrincipalID: 3, BackendID: "local", StorageKey: key, State: "writing", ExpiresAt: now.Add(-time.Hour).UnixMilli(), CreatedAt: now.Add(-2 * time.Hour).UnixMilli()}))
	require.NoError(t, os.MkdirAll(filepath.Join(driver.directory, "avatars/3"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(driver.directory, localPendingKey(key)), []byte("partial"), 0600))
	_, err := accounts.CollectAccountObjects(t.Context(), db, now, 0, 100)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(driver.directory, localPendingKey(key)))
	require.ErrorIs(t, err, os.ErrNotExist)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "abandoned", intent.State)
	// A delayed remote-style write cannot gain a reference; the next sweep
	// removes any bytes that appeared after the first reclamation.
	_, err = driver.Put(t.Context(), key, bytes.NewBufferString("late"), 4)
	require.NoError(t, err)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), key)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAccountTransactionBoundariesAndCandidatePromotion(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.ErrorIs(t, accounts.Register(t.Context(), db, prepared, 2, 3, "avatar", nil), ErrAccountTransaction)
	require.ErrorIs(t, accounts.UseAvatarCandidate(t.Context(), db, 3, 2, "avatars/3/1.png", now), ErrAccountTransaction)
	require.ErrorIs(t, accounts.ReleaseAccountResources(t.Context(), db, 3, now), ErrAccountTransaction)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := accounts.Stage(t.Context(), tx, 4, 3, "avatars/3/4.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
		require.ErrorIs(t, err, ErrAccountTransaction)
		return nil
	}))
	expiry := now.Add(time.Minute)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.Register(t.Context(), tx, prepared, 2, 3, "avatar_candidate", &expiry)
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 4, 5, "avatars/3/1.png", now)
	}), ErrNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 3, 5, "avatars/3/1.png", expiry.Add(time.Second))
	}), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 3, 5, "avatars/3/1.png", now)
	}))
	rollback := errors.New("erasure failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, accounts.ReleaseAccountResources(t.Context(), tx, 3, now))
		return rollback
	}), rollback)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", expiry.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, readable, "current reference has no candidate expiry")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReleaseAccountResources(t.Context(), tx, 3, now) }))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now, 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), "avatars/3/1.png")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStageFailureValidationAndCleanupCursor(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	now := time.Now()
	_, err := accounts.Stage(t.Context(), nil, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(-time.Hour))
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("short"), 100, now.Add(time.Hour))
	require.ErrorIs(t, err, ErrLength)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
	require.Error(t, err, "the durable key must never be reused")
	_, err = accounts.CollectAccountObjects(t.Context(), db, now, 0, 0)
	require.ErrorIs(t, err, ErrInvalidKey)
	after, err := accounts.CollectAccountObjects(t.Context(), db, now, 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, after)
	after, err = accounts.CollectAccountObjects(t.Context(), db, now, after, 1)
	require.NoError(t, err)
	require.Zero(t, after)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/missing.png", now)
	require.NoError(t, err)
	require.False(t, readable)
}
