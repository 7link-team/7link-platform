package dlock

import (
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationSourceCreatesSchema(t *testing.T) {
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

	migrateTestDatabase(t, db)
	assert.True(t, db.Migrator().HasTable("dlocks"))
}

func migrateTestDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, (&Module{}).MigrationSource(), db.Dialector.Name()))
}
