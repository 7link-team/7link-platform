package dlock

import (
	"context"
	"path/filepath"
	"testing"

	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
