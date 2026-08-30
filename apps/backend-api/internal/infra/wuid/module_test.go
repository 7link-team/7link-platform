package wuid

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testWriter struct{ db *gorm.DB }

func (writer *testWriter) Write() *gorm.DB { return writer.db }

func TestModuleWithoutDatabaseReportsUnhealthy(t *testing.T) {
	t.Setenv(EnvKey, "")
	module := newModule(nil)
	require.NoError(t, module.Start(t.Context()))
	assert.ErrorIs(t, module.CheckHealth(t.Context()), ErrUnavailable)
	assert.NoError(t, module.Stop(t.Context()))
}

func TestModuleStaticWorkerDoesNotRequireDatabase(t *testing.T) {
	t.Setenv(EnvKey, "7")
	module := newModule(nil)
	require.NoError(t, module.Start(t.Context()))
	id, err := module.ID()
	require.NoError(t, err)
	assert.Equal(t, uint16(7), id)
	assert.NoError(t, module.CheckHealth(t.Context()))
	require.NoError(t, module.Stop(t.Context()))
}

func TestModuleOwnsMigrationAndReportsLeaseState(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newTestDB(t)
	module := newModule(&testWriter{db: db})
	require.NoError(t, Migrate(t.Context(), db))
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
