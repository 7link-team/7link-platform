package dlock

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:migration?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, Migrate(t.Context(), db))
	require.NoError(t, MigrateDown(t.Context(), db))
	require.NoError(t, Migrate(t.Context(), db))
	require.NoError(t, MigrateDownTo(t.Context(), db, 0))
	require.NoError(t, Migrate(t.Context(), db))
}
