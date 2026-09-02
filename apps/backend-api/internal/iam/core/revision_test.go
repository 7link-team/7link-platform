package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRevisionStoreLifecycleUsesPrefixedTable(t *testing.T) {
	db := openRevisionDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE sl_iam_policy_revisions (
		tenant_id INTEGER PRIMARY KEY,
		revision BIGINT NOT NULL,
		updated_at DATETIME NULL
	)`).Error)

	store, err := NewRevisionStore("sl_")
	require.NoError(t, err)

	revision, err := store.Current(t.Context(), db, 11)
	require.NoError(t, err)
	assert.Zero(t, revision)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := store.Lock(t.Context(), tx, 11); err != nil {
			return err
		}
		current, err := store.Current(t.Context(), tx, 11)
		if err != nil {
			return err
		}
		assert.Zero(t, current, "locking must not advance the revision")
		return store.Bump(t.Context(), tx, 11, time.Date(2026, 9, 2, 12, 0, 0, 123456789, time.FixedZone("test", 8*60*60)))
	}))

	wantUpdatedAt := time.Date(2026, 9, 2, 5, 0, 0, 987654321, time.UTC)
	require.NoError(t, store.Bump(t.Context(), db, 11, wantUpdatedAt))
	revision, err = store.Current(t.Context(), db, 11)
	require.NoError(t, err)
	assert.Equal(t, int64(2), revision)

	var updatedAt time.Time
	require.NoError(t, db.Table("sl_iam_policy_revisions").
		Select("updated_at").Where("tenant_id = ?", 11).Scan(&updatedAt).Error)
	assert.Equal(t, wantUpdatedAt.Truncate(time.Millisecond), updatedAt.UTC())

	var unprefixed int64
	require.NoError(t, db.Raw(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'iam_policy_revisions'`,
	).Scan(&unprefixed).Error)
	assert.Zero(t, unprefixed)
}

func TestRevisionStoreConcurrentBumpsDoNotLoseUpdates(t *testing.T) {
	db := openRevisionDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE iam_policy_revisions (
		tenant_id INTEGER PRIMARY KEY,
		revision BIGINT NOT NULL,
		updated_at DATETIME NULL
	)`).Error)
	store, err := NewRevisionStore("")
	require.NoError(t, err)

	const workers = 32
	start := make(chan struct{})
	errors := make(chan error, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			<-start
			errors <- store.Bump(t.Context(), db, 17, time.Unix(int64(worker+1), 0))
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	revision, err := store.Current(t.Context(), db, 17)
	require.NoError(t, err)
	assert.Equal(t, int64(workers), revision)
}

func TestRevisionStoreLockSerializesTenantMutations(t *testing.T) {
	db := openRevisionDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE iam_policy_revisions (
		tenant_id INTEGER PRIMARY KEY,
		revision BIGINT NOT NULL,
		updated_at DATETIME NULL
	)`).Error)
	store, err := NewRevisionStore("")
	require.NoError(t, err)
	require.NoError(t, store.Bump(t.Context(), db, 23, time.Now()))

	first := db.Begin()
	require.NoError(t, first.Error)
	t.Cleanup(func() { _ = first.Rollback().Error })
	require.NoError(t, store.Lock(t.Context(), first, 23))

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- store.Bump(t.Context(), db, 23, time.Now())
	}()
	<-started
	select {
	case err := <-done:
		require.NoError(t, err)
		t.Fatal("a competing mutation completed while the tenant lock was held")
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, first.Commit().Error)
	require.NoError(t, <-done)
	revision, err := store.Current(t.Context(), db, 23)
	require.NoError(t, err)
	assert.Equal(t, int64(2), revision)
}

func TestRevisionStoreRejectsInvalidConfiguration(t *testing.T) {
	store, err := NewRevisionStore("bad-prefix;")
	assert.Nil(t, store)
	assert.ErrorContains(t, err, "invalid table prefix")

	store, err = NewRevisionStore("")
	require.NoError(t, err)
	db := openRevisionDB(t)

	_, err = store.Current(t.Context(), db, 1)
	assert.ErrorContains(t, err, "get IAM policy revision")
	assert.ErrorContains(t, store.Lock(t.Context(), db, 1), "lock IAM policy revision")
	assert.ErrorContains(t, store.Bump(t.Context(), db, 1, time.Now()), "advance IAM policy revision")

	_, err = store.Current(t.Context(), nil, 1)
	assert.Error(t, err)
	assert.Error(t, store.Lock(t.Context(), nil, 1))
	assert.Error(t, store.Bump(t.Context(), nil, 1, time.Now()))
	_, err = store.Current(t.Context(), db, 0)
	assert.Error(t, err)
	assert.Error(t, store.Lock(t.Context(), db, 0))
	assert.Error(t, store.Bump(t.Context(), db, 0, time.Now()))
	assert.Error(t, store.Bump(t.Context(), db, 1, time.Time{}))

	var nilStore *RevisionStore
	_, err = nilStore.Current(t.Context(), db, 1)
	assert.Error(t, err)
	assert.Error(t, nilStore.Lock(t.Context(), db, 1))
	assert.Error(t, nilStore.Bump(t.Context(), db, 1, time.Now()))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Current(canceled, db, 1)
	assert.ErrorIs(t, err, context.Canceled)
}

func openRevisionDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on", filepath.ToSlash(filepath.Join(t.TempDir(), "policy.sqlite")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(16)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}
