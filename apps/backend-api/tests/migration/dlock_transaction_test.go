package migration_test

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/tests/internal/testdb"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDlockTransactionFencePreventsExpiredLeaseTakeover(t *testing.T) {
	sqlDB := testdb.OpenPostgres(t)
	sqlDB.SetMaxOpenConns(4)
	db := openMilestoneGORM(t, sqlDB, "postgres")
	source := (&dlock.Module{}).MigrationSource()
	source.Prefix = deploymentPrefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "postgres"))
	locker := dlock.NewLocker(db, dlock.WithTablePrefix(deploymentPrefix), dlock.WithTTL(time.Minute))
	lease, err := locker.TryLock(t.Context(), "governance:seed:1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lease.Unlock(context.Background()) })
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, lease.FenceOn(t.Context(), tx))
		// Force expiry while the protected transaction still owns the row lock.
		require.NoError(t, tx.Exec("UPDATE "+deploymentPrefix+"dlocks SET expires_at=0 WHERE name=?", lease.Name()).Error)
		contenderCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		poolWaits := sqlDB.Stats().WaitCount
		contender, err := locker.TryLock(contenderCtx, lease.Name())
		require.Nil(t, contender)
		require.Error(t, err)
		require.ErrorIs(t, contenderCtx.Err(), context.DeadlineExceeded)
		require.Equal(t, poolWaits, sqlDB.Stats().WaitCount, "deadline must come from database contention, not waiting for a pool connection")
		return nil
	}))
	replacement, err := locker.TryLock(t.Context(), lease.Name())
	require.NoError(t, err, "takeover becomes possible only after the protected transaction ends")
	require.NoError(t, lease.Unlock(t.Context()), "stale owner cannot clear replacement")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return replacement.FenceOn(t.Context(), tx) }))
	require.NoError(t, replacement.Unlock(t.Context()))
}
