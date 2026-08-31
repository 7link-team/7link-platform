package wuid

import (
	"path/filepath"
	"testing"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newModuleRuntime(t *testing.T) (*appenv.Runtime, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "wuid-module.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })
	db := router.Write()
	migrateTestSource(t, db, (&dlock.Module{}).MigrationSource())
	migrateTestSource(t, db, (&Module{}).MigrationSource())
	return appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"), appenv.WithDatabase(router)), db
}

func TestModuleWithoutDatabaseReportsUnhealthy(t *testing.T) {
	t.Setenv(EnvKey, "")
	module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")))
	require.NoError(t, module.Start(t.Context()))
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)
	assert.NoError(t, module.Stop(t.Context()))
}

func TestModuleStaticWorkerDoesNotRequireDatabase(t *testing.T) {
	t.Setenv(EnvKey, "7")
	module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")))
	require.NoError(t, module.Start(t.Context()))
	id, err := module.ID()
	require.NoError(t, err)
	assert.Equal(t, uint16(7), id)
	assert.NoError(t, module.CheckHealth(t.Context()))
	require.NoError(t, module.Stop(t.Context()))
}

func TestModuleOwnsMigrationAndReportsLeaseState(t *testing.T) {
	t.Setenv(EnvKey, "")
	rt, _ := newModuleRuntime(t)
	module := NewModule(rt)
	require.NoError(t, module.Start(t.Context()))
	assert.NoError(t, module.CheckHealth(t.Context()))
	lease, err := module.Current()
	require.NoError(t, err)
	assert.Equal(t, uint16(0), lease.WorkerID)
	assert.Positive(t, lease.Generation)

	expected := assert.AnError
	module.notifyLost(expected)
	_, err = module.Current()
	assert.ErrorIs(t, err, expected)
	module.notifyReacquired(9)
	assert.NoError(t, module.CheckHealth(t.Context()))
	require.NoError(t, module.Stop(t.Context()))
}

func TestPublicModuleContractWithoutDatabase(t *testing.T) {
	t.Setenv(EnvKey, "")
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	m := NewModule(rt)
	assert.Same(t, rt, m.runtime)
	assert.Equal(t, Name, m.ModuleName())
	require.NoError(t, m.ProvideServices(module.NewServices()))
	assert.Nil(t, m.writer())
	_, err := m.Current()
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, m.Start(t.Context()))
	_, err = m.Current()
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, m.Stop(t.Context()))
}

func TestModuleRejectsInvalidStaticWorkerConfiguration(t *testing.T) {
	t.Setenv(EnvKey, "invalid")
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")))
	assert.ErrorContains(t, m.Start(t.Context()), "allocate worker ID")
}
