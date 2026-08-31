package wuid

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
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
	migrateTestSource(t, db, (&dlock.Module{}).MigrationSource())
	migrateTestSource(t, db, (&Module{}).MigrationSource())
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
	assert.Equal(t, worker.retryInterval(), worker.step(canceled), "a failed reacquire retries without restoring liveness")
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

func TestMigrationSourceCreatesSchema(t *testing.T) {
	t.Setenv(EnvKey, "")
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_wuid", source.TableName)
	assert.NotNil(t, source.FS)

	db := newTestDB(t)
	assert.True(t, db.Migrator().HasTable("worker_ids"))
}

func migrateTestSource(t *testing.T, db *gorm.DB, source goosex.Source) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
}

func TestOptionsComposeAndWorkerIntervals(t *testing.T) {
	var lostCalls, reacquireCalls int
	config := newConfig(
		WithLease(0),
		OnLost(nil),
		OnLost(func(error) { lostCalls++ }),
		OnLost(func(error) { lostCalls++ }),
		OnReacquire(nil),
		OnReacquire(func(uint16) { reacquireCalls++ }),
		OnReacquire(func(uint16) { reacquireCalls++ }),
	)
	assert.Equal(t, DefaultLease, config.lease)
	config.onLost(assert.AnError)
	config.onReacquire(7)
	assert.Equal(t, 2, lostCalls)
	assert.Equal(t, 2, reacquireCalls)

	short := &Worker{}
	assert.Equal(t, time.Second, short.renewInterval())
	assert.Equal(t, time.Second, short.retryInterval())
	long := &Worker{lease: time.Hour}
	assert.Equal(t, renewRetryInterval, long.retryInterval())
}

func TestStaticWorkerCloseIsIdempotent(t *testing.T) {
	worker := newStatic(9)
	require.NoError(t, worker.Close(t.Context()))
	require.NoError(t, worker.Close(t.Context()))
}

func TestWorkerIDPoolExhaustion(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	require.NoError(t, db.Create(&workerRow{ID: MaxWorkerID, Token: "occupied", ExpiresAt: 1 << 62}).Error)
	_, _, err := claimUnderLock(t.Context(), db, time.Hour, gormx.NowMillisExpr(db.Dialector.Name()))
	assert.ErrorContains(t, err, "worker id pool exhausted")
}

func TestWorkerCloseReportsDatabaseFailure(t *testing.T) {
	db := newTestDB(t)
	worker := &Worker{db: db}
	worker.id.Store(1)
	worker.token.Store("token")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.Error(t, worker.Close(t.Context()))
}
