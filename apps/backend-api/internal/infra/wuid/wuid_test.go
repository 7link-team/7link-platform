package wuid

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wuid.sqlite")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, dlock.Migrate(t.Context(), db))
	require.NoError(t, Migrate(t.Context(), db))
	return db
}

func TestOpenStaticWorkerID(t *testing.T) {
	t.Setenv(EnvKey, "42")
	worker, err := Open(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, uint16(42), worker.ID())
	assert.True(t, worker.Alive())
	require.NoError(t, worker.Close(t.Context()))
	assert.False(t, worker.Alive())
}

func TestOpenRejectsInvalidStaticWorkerID(t *testing.T) {
	t.Setenv(EnvKey, "65536")
	_, err := Open(t.Context(), nil)
	assert.ErrorContains(t, err, "invalid WUID")
}

func TestDatabaseWorkersAreDistinctAndReleasedIDsAreReused(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	first, err := Open(t.Context(), db, WithLease(time.Minute))
	require.NoError(t, err)
	second, err := Open(t.Context(), db, WithLease(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, uint16(0), first.ID())
	assert.Equal(t, uint16(1), second.ID())
	require.NoError(t, first.Close(t.Context()))

	reused, err := Open(t.Context(), db, WithLease(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, uint16(0), reused.ID())
	require.NoError(t, reused.Close(t.Context()))
	require.NoError(t, second.Close(t.Context()))
}

func TestWorkerRenewsLease(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	worker, err := Open(t.Context(), db, WithLease(time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { _ = worker.Close(context.Background()) })

	require.NoError(t, db.Model(&workerRow{}).
		Where("id = ?", worker.ID()).UpdateColumn("expires_at", int64(0)).Error)
	worker.step(t.Context())

	var row workerRow
	require.NoError(t, db.First(&row, "id = ?", worker.ID()).Error)
	assert.Positive(t, row.ExpiresAt)
	assert.True(t, worker.Alive())
}

func TestWorkerDeclaresLeaseLostAfterTakeover(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	lost := make(chan error, 1)
	worker, err := Open(t.Context(), db, WithLease(time.Hour), OnLost(func(err error) { lost <- err }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = worker.Close(context.Background()) })

	require.NoError(t, db.Model(&workerRow{}).
		Where("id = ?", worker.ID()).UpdateColumn("token", "replacement").Error)
	worker.step(t.Context())

	assert.False(t, worker.Alive())
	select {
	case err := <-lost:
		assert.ErrorContains(t, err, "taken over")
	default:
		t.Fatal("OnLost was not called")
	}
	worker.declareLost(assert.AnError)
	select {
	case <-lost:
		t.Fatal("OnLost was called more than once for one loss")
	default:
	}
}

func TestWorkerUsesLeaseMarginForTransientRenewalFailures(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	lost := make(chan error, 1)
	worker, err := Open(t.Context(), db, WithLease(time.Hour), OnLost(func(err error) { lost <- err }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = worker.Close(context.Background()) })

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	worker.step(canceled)
	assert.True(t, worker.Alive(), "one transient failure must not discard a fresh lease")

	margin := worker.lease / 3
	worker.lastRenew.Store(monotonicMillis() - (worker.lease - margin).Milliseconds() - 1)
	worker.step(canceled)
	assert.False(t, worker.Alive())
	select {
	case err := <-lost:
		assert.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("OnLost was not called after the safety margin elapsed")
	}
}

func TestWorkerReacquiresExpiredWorkerID(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	reacquired := make(chan uint16, 1)
	worker, err := Open(t.Context(), db, WithLease(time.Hour), OnReacquire(func(id uint16) { reacquired <- id }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = worker.Close(context.Background()) })
	previousToken := worker.token.Load().(string)

	require.NoError(t, db.Model(&workerRow{}).Where("id = ?", worker.ID()).Updates(map[string]any{
		"token": "replacement", "expires_at": int64(0),
	}).Error)
	worker.step(t.Context())
	require.False(t, worker.Alive())
	worker.step(t.Context())

	assert.True(t, worker.Alive())
	assert.Equal(t, uint16(0), worker.ID())
	assert.Equal(t, uint64(2), worker.Generation())
	assert.NotEqual(t, previousToken, worker.token.Load().(string))
	select {
	case id := <-reacquired:
		assert.Equal(t, worker.ID(), id)
	default:
		t.Fatal("OnReacquire was not called")
	}
}

func TestWorkerReallocatesWhenLostWorkerIDIsStillLeased(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	reacquired := make(chan uint16, 1)
	worker, err := Open(t.Context(), db, WithLease(time.Hour), OnReacquire(func(id uint16) { reacquired <- id }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = worker.Close(context.Background()) })

	require.NoError(t, db.Model(&workerRow{}).Where("id = ?", worker.ID()).Updates(map[string]any{
		"token": "replacement", "expires_at": int64(1 << 62),
	}).Error)
	worker.step(t.Context())
	require.False(t, worker.Alive())
	worker.step(t.Context())

	assert.True(t, worker.Alive())
	assert.Equal(t, uint16(1), worker.ID())
	assert.Equal(t, uint64(2), worker.Generation())
	select {
	case id := <-reacquired:
		assert.Equal(t, uint16(1), id)
	default:
		t.Fatal("OnReacquire was not called")
	}
}

func TestMigrationLifecycle(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	require.NoError(t, MigrateDown(t.Context(), db))
	require.NoError(t, Migrate(t.Context(), db))
	require.NoError(t, MigrateDownTo(t.Context(), db, 0))
	require.NoError(t, Migrate(t.Context(), db))
}
