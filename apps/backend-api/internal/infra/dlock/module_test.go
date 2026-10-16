package dlock

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	modulepkg "7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTransactionLeaseProvider(t *testing.T) {
	rt, db := newModuleRuntime(t)
	m := NewModule(rt, WithAutoRefresh())
	services := modulepkg.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	acquire, err := modulepkg.Resolve[TransactionLease](services)
	require.NoError(t, err)
	lease, err := acquire(t.Context(), "governance:seed:1", time.Minute)
	require.NoError(t, err)
	require.Equal(t, time.Minute, lease.locker.ttl)
	require.False(t, lease.locker.autoRefresh)
	_, err = acquire(t.Context(), "governance:seed:1", time.Minute)
	require.ErrorIs(t, err, ErrNotAcquired)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return lease.FenceOn(t.Context(), tx) }))
	require.NoError(t, lease.Unlock(t.Context()))
	_, err = acquire(t.Context(), "", time.Minute)
	require.ErrorIs(t, err, ErrInvalidTransaction)
	_, err = acquire(t.Context(), "key", 0)
	require.ErrorIs(t, err, ErrInvalidTransaction)
	_, err = NewModule(nil).transactionLease(t.Context(), "key", time.Minute)
	require.ErrorIs(t, err, ErrDatabaseUnavailable)
}

func newModuleRuntime(t *testing.T) (*appenv.Runtime, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "dlock-module.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })
	db := router.Write()
	migrateTestDatabase(t, db)
	return appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"), appenv.WithDatabase(router)), db
}

func TestModuleWithoutDatabaseReportsUnhealthy(t *testing.T) {
	module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")))
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrDatabaseUnavailable)
	_, err := module.TryLock(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
	_, err = module.Lock(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
}

func TestModuleInitializesAndProvidesLocks(t *testing.T) {
	rt, db := newModuleRuntime(t)
	module := NewModule(rt, WithTTL(DefaultTTL))
	assert.Same(t, db, module.writer())
	require.NoError(t, module.CheckHealth(t.Context()))

	lock, err := module.TryLock(t.Context(), "module")
	require.NoError(t, err)
	require.NoError(t, lock.Unlock(t.Context()))
}

func TestModuleReportsClosedDatabase(t *testing.T) {
	rt, db := newModuleRuntime(t)
	module := NewModule(rt)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.Error(t, module.CheckHealth(context.Background()))
}

func TestRenewingLeaseProvider(t *testing.T) {
	rt, db := newModuleRuntime(t)
	m := NewModule(rt)
	services := modulepkg.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	acquire, err := modulepkg.Resolve[RenewingLease](services)
	require.NoError(t, err)
	lost := make(chan error, 1)
	lease, err := acquire(t.Context(), "leader", 90*time.Millisecond, func(_ string, err error) { lost <- err })
	require.NoError(t, err)
	require.True(t, lease.locker.autoRefresh)
	require.Equal(t, 90*time.Millisecond, lease.locker.ttl)
	require.NoError(t, db.Exec("UPDATE dlocks SET owner = ? WHERE name = ?", "replacement", "leader").Error)
	select {
	case err := <-lost:
		require.ErrorIs(t, err, ErrNotAcquired)
	case <-time.After(3 * time.Second):
		t.Fatal("lease loss did not notify owner")
	}
	require.NoError(t, lease.Unlock(t.Context()))
	callback := func(string, error) {}
	for _, tc := range []struct {
		name string
		ttl  time.Duration
		lost func(string, error)
	}{{"", time.Second, callback}, {"x", 0, callback}, {"x", time.Second, nil}} {
		_, err := acquire(t.Context(), tc.name, tc.ttl, tc.lost)
		require.ErrorIs(t, err, ErrInvalidTransaction)
	}
	_, err = NewModule(nil).renewingLease(t.Context(), "x", time.Second, callback)
	require.ErrorIs(t, err, ErrDatabaseUnavailable)
}

func TestTryRenewingLeaseDoesNotWaitAndNotifiesLoss(t *testing.T) {
	rt, db := newModuleRuntime(t)
	m := NewModule(rt)
	services := modulepkg.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	acquire, err := modulepkg.Resolve[TryRenewingLease](services)
	require.NoError(t, err)
	lost := make(chan error, 1)
	lease, err := acquire(t.Context(), "jobs:ck:jobs.work:key", 90*time.Millisecond, func(_ string, err error) { lost <- err })
	require.NoError(t, err)
	t.Cleanup(func() { _ = lease.Unlock(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	contender, err := acquire(ctx, lease.Name(), time.Second, func(string, error) {})
	require.Nil(t, contender)
	require.ErrorIs(t, err, ErrNotAcquired)
	require.NoError(t, ctx.Err(), "contention must return without waiting for the deadline")
	// Auto-refresh is behavior, not merely a configuration flag.
	require.NoError(t, db.Exec("UPDATE dlocks SET expires_at=0 WHERE name=?", lease.Name()).Error)
	require.Eventually(t, func() bool {
		var expiry int64
		result := db.Table("dlocks").Select("expires_at").Where("name=?", lease.Name()).Scan(&expiry)
		return result.Error == nil && expiry > 0
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, db.Exec("UPDATE dlocks SET owner=? WHERE name=?", "replacement", lease.Name()).Error)
	select {
	case err := <-lost:
		require.ErrorIs(t, err, ErrNotAcquired)
	case <-time.After(3 * time.Second):
		t.Fatal("try lease loss was not reported")
	}
	require.NoError(t, lease.Unlock(t.Context()))
	var owner string
	require.NoError(t, db.Table("dlocks").Select("owner").Where("name=?", lease.Name()).Scan(&owner).Error)
	require.Equal(t, "replacement", owner)
	_, err = acquire(t.Context(), "", time.Second, func(string, error) {})
	require.ErrorIs(t, err, ErrInvalidTransaction)
	_, err = NewModule(nil).tryRenewingLease(t.Context(), "key", time.Second, func(string, error) {})
	require.ErrorIs(t, err, ErrDatabaseUnavailable)
}
