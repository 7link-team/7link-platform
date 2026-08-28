package dlock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationLifecycle(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_dlock", source.TableName)
	assert.NotNil(t, source.FS)

	db, err := gorm.Open(sqlite.Open("file:migration?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, Migrate(t.Context(), db))
	require.NoError(t, Migrate(t.Context(), db))
	require.NoError(t, MigrateUp(t.Context(), db))
	require.NoError(t, MigrateUpTo(t.Context(), db, 1))
	require.NoError(t, MigrateDown(t.Context(), db))
	require.NoError(t, MigrateDown(t.Context(), db))
	require.NoError(t, MigrateDownTo(t.Context(), db, 0))
	require.NoError(t, Migrate(t.Context(), db))
}

func TestMigrationCommandsRequireDatabase(t *testing.T) {
	assert.ErrorIs(t, Migrate(t.Context(), nil), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateUp(t.Context(), nil), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateUpTo(t.Context(), nil, 1), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateDown(t.Context(), nil), ErrDatabaseUnavailable)
	assert.ErrorIs(t, MigrateDownTo(t.Context(), nil, 0), ErrDatabaseUnavailable)
}
