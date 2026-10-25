package gormx

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSQLitePlainPathQueuesBackgroundWritersWithoutBlockingReaders(t *testing.T) {
	db, err := (Datasource{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "preview.sqlite") + "?_busy_timeout=1", MaxOpenConns: 4}).Open(t.Context())
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.Exec("CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO counter VALUES (1,0)").Error)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	writer := db.WithContext(ctx).Begin()
	require.NoError(t, writer.Error)
	require.NoError(t, writer.Exec("UPDATE counter SET value=value+1 WHERE id=1").Error)
	var value int
	readCtx, stop := context.WithTimeout(ctx, time.Second)
	require.NoError(t, db.WithContext(readCtx).Raw("SELECT value FROM counter WHERE id=1").Scan(&value).Error)
	stop()
	require.Zero(t, value, "WAL reader sees committed data while a writer is active")
	const workers, iterations = 24, 15
	errors := make(chan error, workers+1)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for range iterations {
				err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Exec("UPDATE counter SET value=value+1 WHERE id=1").Error; err != nil {
						return err
					}
					time.Sleep(time.Millisecond)
					return nil
				})
				if err != nil {
					errors <- err
					return
				}
			}
		})
	}
	group.Go(func() {
		for range iterations {
			var current int
			if err := db.WithContext(ctx).Raw("SELECT value FROM counter WHERE id=1").Scan(&current).Error; err != nil {
				errors <- err
				return
			}
			if err := db.WithContext(ctx).Exec("UPDATE counter SET value=value+1 WHERE id=1").Error; err != nil {
				errors <- err
				return
			}
		}
	})
	require.NoError(t, writer.Commit().Error)
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, db.Raw("SELECT value FROM counter WHERE id=1").Scan(&value).Error)
	require.Equal(t, 1+(workers+1)*iterations, value, "all background and user writes commit exactly once")
}

func TestSQLiteWriterAdmissionCancellationAndRollbackRelease(t *testing.T) {
	db, err := (Datasource{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "cancel.sqlite"), MaxOpenConns: 3}).Open(t.Context())
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	for _, finish := range []string{"commit", "rollback", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tx := db.WithContext(ctx).Begin()
			require.NoError(t, tx.Error)
			waiting, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer stop()
			require.ErrorIs(t, db.WithContext(waiting).Begin().Error, context.DeadlineExceeded)
			switch finish {
			case "commit":
				require.NoError(t, tx.Commit().Error)
			case "rollback":
				require.NoError(t, tx.Rollback().Error)
			case "cancel":
				cancel() // no explicit rollback: the pool owns cancellation cleanup
			}
			recovery, done := context.WithTimeout(t.Context(), time.Second)
			defer done()
			require.NoError(t, db.WithContext(recovery).Transaction(func(*gorm.DB) error { return nil }))
		})
	}
	sentinel := errors.New("abort")
	require.ErrorIs(t, db.Transaction(func(*gorm.DB) error { return sentinel }), sentinel)
	require.NoError(t, db.Transaction(func(*gorm.DB) error { return nil }))
}

func TestSQLiteCancellationRacingCompletionKeepsAdmissionUntilDriverFinishes(t *testing.T) {
	db, err := (Datasource{Type: "sqlite", DSN: filepath.Join(t.TempDir(), "completion.sqlite") + "?_busy_timeout=1", MaxOpenConns: 4}).Open(t.Context())
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.Exec("CREATE TABLE counter (value INTEGER)").Error)
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		tx := db.WithContext(ctx).Begin()
		require.NoError(t, tx.Error)
		require.NoError(t, tx.Exec("INSERT INTO counter VALUES (1)").Error)
		var group sync.WaitGroup
		group.Go(cancel)
		group.Go(func() { _ = tx.Rollback().Error })
		group.Wait()
		recovery, stop := context.WithTimeout(t.Context(), time.Second)
		err := db.WithContext(recovery).Transaction(func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO counter VALUES (2)").Error
		})
		stop()
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, db.Table("counter").Where("value=1").Count(&count).Error)
	require.Zero(t, count, "all canceled business writes roll back")
	require.NoError(t, db.Table("counter").Where("value=2").Count(&count).Error)
	require.EqualValues(t, 100, count)
}
