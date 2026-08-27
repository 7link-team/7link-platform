package dlock

import (
	"context"
	"sync/atomic"
	"testing"

	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type countingWriter struct {
	db    *gorm.DB
	calls atomic.Int32
}

func (r *countingWriter) Write() *gorm.DB {
	r.calls.Add(1)
	return r.db
}

func TestModuleWithoutDatabaseReportsUnhealthy(t *testing.T) {
	module := NewModule(nil)
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrDatabaseUnavailable)
	_, err := module.TryLock(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
	_, err = module.Lock(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrDatabaseUnavailable)
}

func TestModuleInitializesAndProvidesLocks(t *testing.T) {
	db := newDB(t)
	router := &countingWriter{db: db}
	module := NewModule(router, WithTTL(DefaultTTL))

	require.NoError(t, Migrate(t.Context(), db))
	assert.Zero(t, router.calls.Load())
	require.NoError(t, module.CheckHealth(t.Context()))
	assert.Equal(t, int32(1), router.calls.Load())

	lock, err := module.TryLock(t.Context(), "module")
	require.NoError(t, err)
	assert.Equal(t, int32(2), router.calls.Load())
	require.NoError(t, lock.Unlock(t.Context()))
}

func TestModuleReportsClosedDatabase(t *testing.T) {
	db := newDB(t)
	module := NewModule(&gormx.DatasourceRouter{Writer: []*gorm.DB{db}})
	require.NoError(t, Migrate(t.Context(), db))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.Error(t, module.CheckHealth(context.Background()))
}
