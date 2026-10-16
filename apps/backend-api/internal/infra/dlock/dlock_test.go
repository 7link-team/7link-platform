package dlock

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type traceContextKey struct{}

func TestFenceRequiresCurrentUnexpiredOwnerAndTransaction(t *testing.T) {
	db := newDB(t)
	locker := NewLocker(db)
	lease, err := locker.TryLock(t.Context(), "seed")
	require.NoError(t, err)
	require.ErrorIs(t, lease.FenceOn(t.Context(), nil), ErrInvalidTransaction)
	require.ErrorIs(t, lease.FenceOn(t.Context(), db), ErrInvalidTransaction)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.ErrorIs(t, (*Lock)(nil).FenceOn(t.Context(), tx), ErrNotAcquired)
		return lease.FenceOn(t.Context(), tx)
	}))
	locks := locker.locks()
	_, err = locks.WithContext(t.Context()).Where(locks.Name.Eq("seed")).UpdateSimple(locks.ExpiresAt.Value(0))
	require.NoError(t, err)
	err = db.Transaction(func(tx *gorm.DB) error { return lease.FenceOn(t.Context(), tx) })
	require.ErrorIs(t, err, ErrNotAcquired, "expiry alone must reject a stale worker")
	next, err := locker.TryLock(t.Context(), "seed")
	require.NoError(t, err)
	err = db.Transaction(func(tx *gorm.DB) error { return lease.FenceOn(t.Context(), tx) })
	require.ErrorIs(t, err, ErrNotAcquired, "new owner must reject the stale token")
	require.NoError(t, next.Unlock(t.Context()))
	err = db.Transaction(func(tx *gorm.DB) error { return next.FenceOn(t.Context(), tx) })
	require.ErrorIs(t, err, ErrNotAcquired)
	require.NoError(t, lease.Unlock(t.Context()))
}

type contextLogger struct {
	logger.Interface
	contexts chan context.Context
}

func (l *contextLogger) Trace(ctx context.Context, _ time.Time, _ func() (string, int64), _ error) {
	select {
	case l.contexts <- ctx:
	default:
	}
}

var databaseID atomic.Uint64

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:dlock_%d?mode=memory&cache=shared&_busy_timeout=5000", databaseID.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrateTestDatabase(t, db)
	return db
}

func TestTryLockAcquireContendRelease(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t))

	first, err := locker.TryLock(ctx, "job")
	require.NoError(t, err)
	assert.Equal(t, "job", first.Name())
	assert.NotEmpty(t, first.Owner())
	assert.True(t, first.Alive())

	_, err = locker.TryLock(ctx, "job")
	assert.ErrorIs(t, err, ErrNotAcquired)

	require.NoError(t, first.Unlock(ctx))
	assert.False(t, first.Alive())

	second, err := locker.TryLock(ctx, "job")
	require.NoError(t, err)
	assert.NotEqual(t, first.Owner(), second.Owner())
	t.Cleanup(func() { _ = second.Unlock(context.Background()) })
}

func TestLockBlocksUntilReleased(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t))
	held, err := locker.TryLock(ctx, "resource")
	require.NoError(t, err)

	type result struct {
		lock *Lock
		err  error
	}
	acquired := make(chan result, 1)
	go func() {
		lock, lockErr := locker.Lock(ctx, "resource")
		acquired <- result{lock: lock, err: lockErr}
	}()

	select {
	case <-acquired:
		t.Fatal("Lock returned while the lease was still held")
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, held.Unlock(ctx))
	select {
	case got := <-acquired:
		require.NoError(t, got.err)
		require.NotNil(t, got.lock)
		assert.Equal(t, "resource", got.lock.Name())
		t.Cleanup(func() { _ = got.lock.Unlock(context.Background()) })
	case <-time.After(2 * time.Second):
		t.Fatal("Lock did not acquire after release")
	}
}

func TestLockContextCancelled(t *testing.T) {
	locker := NewLocker(newDB(t))
	held, err := locker.TryLock(t.Context(), "resource")
	require.NoError(t, err)
	defer func() { _ = held.Unlock(context.Background()) }()

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	_, err = locker.Lock(ctx, "resource")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExpiredLeaseCanBeTakenOver(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t), WithTTL(80*time.Millisecond))

	stale, err := locker.TryLock(ctx, "lease")
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	current, err := locker.TryLock(ctx, "lease")
	require.NoError(t, err)
	assert.NotEqual(t, stale.Owner(), current.Owner())
	assert.ErrorIs(t, stale.Refresh(ctx), ErrNotAcquired)

	// A stale holder must not clear the current holder's lease.
	require.NoError(t, stale.Unlock(ctx))
	_, err = locker.TryLock(ctx, "lease")
	assert.ErrorIs(t, err, ErrNotAcquired)
	require.NoError(t, current.Unlock(ctx))
}

func TestRefreshAndUnlockedRefreshFails(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t))
	lock, err := locker.TryLock(ctx, "heartbeat")
	require.NoError(t, err)

	require.NoError(t, lock.Refresh(ctx))
	require.NoError(t, lock.Unlock(ctx))
	assert.ErrorIs(t, lock.Refresh(ctx), ErrNotAcquired)
}

func TestMutualExclusion(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t))
	const (
		workers = 8
		rounds  = 5
	)

	var active atomic.Int32
	var total atomic.Int32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				lock, err := locker.Lock(ctx, "mutex")
				if err != nil {
					t.Errorf("Lock: %v", err)
					return
				}
				if holders := active.Add(1); holders != 1 {
					t.Errorf("mutual exclusion violated: %d holders", holders)
				}
				total.Add(1)
				time.Sleep(time.Millisecond)
				active.Add(-1)
				if err := lock.Unlock(ctx); err != nil {
					t.Errorf("Unlock: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(workers*rounds), total.Load())
}

func TestWithTTLZeroIsIgnored(t *testing.T) {
	locker := NewLocker(newDB(t), WithTTL(0))
	assert.Equal(t, DefaultTTL, locker.ttl)
}

func TestAutoRefreshHoldsBeyondTTL(t *testing.T) {
	ctx := t.Context()
	locker := NewLocker(newDB(t), WithTTL(120*time.Millisecond), WithAutoRefresh())
	lock, err := locker.TryLock(ctx, "long")
	require.NoError(t, err)

	time.Sleep(400 * time.Millisecond)
	assert.True(t, lock.Alive())
	_, err = locker.TryLock(ctx, "long")
	assert.ErrorIs(t, err, ErrNotAcquired)

	require.NoError(t, lock.Unlock(ctx))
	replacement, err := locker.TryLock(ctx, "long")
	require.NoError(t, err)
	t.Cleanup(func() { _ = replacement.Unlock(context.Background()) })
}

func TestAutoRefreshPreservesRequestContext(t *testing.T) {
	log := &contextLogger{Interface: logger.Discard, contexts: make(chan context.Context, 16)}
	db := newDB(t).Session(&gorm.Session{Logger: log})
	locker := NewLocker(db, WithTTL(90*time.Millisecond), WithAutoRefresh())
	ctx := context.WithValue(t.Context(), traceContextKey{}, "trace-id")
	lock, err := locker.TryLock(ctx, "context")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Unlock(context.Background()) })

	for len(log.contexts) > 0 {
		<-log.contexts
	}
	select {
	case refreshCtx := <-log.contexts:
		assert.Equal(t, "trace-id", refreshCtx.Value(traceContextKey{}))
	case <-time.After(time.Second):
		t.Fatal("auto-refresh SQL was not logged")
	}
}

func TestAutoRefreshDetectsTakeover(t *testing.T) {
	db := newDB(t)
	lost := make(chan error, 1)
	locker := NewLocker(db,
		WithTTL(150*time.Millisecond),
		WithAutoRefresh(),
		WithOnLost(func(_ string, err error) { lost <- err }),
	)
	lock, err := locker.TryLock(t.Context(), "taken")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Unlock(context.Background()) })

	locks := query.Use(db).LockRow
	_, err = locks.WithContext(t.Context()).
		Where(locks.Name.Eq("taken")).
		UpdateSimple(locks.Owner.Value("another-owner"))
	require.NoError(t, err)

	select {
	case err := <-lost:
		assert.ErrorIs(t, err, ErrNotAcquired)
	case <-time.After(2 * time.Second):
		t.Fatal("OnLost was not called after takeover")
	}
	assert.False(t, lock.Alive())
}

func TestMaxHoldStopsAutoRefresh(t *testing.T) {
	lost := make(chan error, 1)
	locker := NewLocker(newDB(t),
		WithTTL(60*time.Millisecond),
		WithAutoRefresh(),
		WithMaxHold(120*time.Millisecond),
		WithOnLost(func(_ string, err error) { lost <- err }),
	)
	lock, err := locker.TryLock(t.Context(), "capped")
	require.NoError(t, err)

	select {
	case err := <-lost:
		assert.ErrorIs(t, err, ErrMaxHoldExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("OnLost was not called after max hold")
	}
	assert.False(t, lock.Alive())
	require.NoError(t, lock.Unlock(t.Context()))
}

func TestAutoRefreshReportsDatabaseFailure(t *testing.T) {
	db := newDB(t)
	lost := make(chan error, 1)
	locker := NewLocker(db,
		WithTTL(120*time.Millisecond),
		WithAutoRefresh(),
		WithOnLost(func(_ string, err error) { lost <- err }),
	)
	lock, err := locker.TryLock(t.Context(), "database-failure")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	select {
	case err := <-lost:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("OnLost was not called after the lease could no longer be renewed")
	}
	assert.False(t, lock.Alive())
}

func TestUnlockIsIdempotent(t *testing.T) {
	locker := NewLocker(newDB(t), WithAutoRefresh())
	lock, err := locker.TryLock(t.Context(), "idempotent")
	require.NoError(t, err)

	require.NoError(t, lock.Unlock(t.Context()))
	assert.False(t, lock.Alive())
	require.NoError(t, lock.Unlock(t.Context()))
}

func TestDatabaseErrorsArePropagated(t *testing.T) {
	db := newDB(t)
	locker := NewLocker(db)
	lock, err := locker.TryLock(t.Context(), "closed")
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = locker.TryLock(t.Context(), "closed")
	assert.Error(t, err)
	_, err = locker.Lock(t.Context(), "closed")
	assert.Error(t, err)
	assert.Error(t, lock.Refresh(t.Context()))
	assert.Error(t, lock.Unlock(t.Context()))
}
