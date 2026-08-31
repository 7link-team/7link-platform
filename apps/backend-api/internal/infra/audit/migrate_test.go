package audit

import (
	"testing"

	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrationSource(t *testing.T) {
	source := (&Module{}).MigrationSource()
	assert.Equal(t, "goose_audit", source.TableName)
	assert.NotNil(t, source.FS)

	// The runtime module satisfies the application migration runner contract.
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1")), Config{Enabled: true})
	assert.Equal(t, "audit", auditModule.ModuleName())
	require.NotNil(t, auditModule.MigrationSource())
	require.Equal(t, source.TableName, auditModule.MigrationSource().TableName)
}

func TestMigrateCreatesTable(t *testing.T) {
	db := openTestDB(t)
	migrateTestDatabase(t, db)
	require.True(t, db.Migrator().HasTable("operation_logs"))

	// Migrating again is idempotent.
	migrateTestDatabase(t, db)
}

func TestOperationLogTableName(t *testing.T) {
	assert.Equal(t, operationLogsTable, (OperationLog{}).TableName())
}

func migrateTestDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, (&Module{}).MigrationSource(), db.Dialector.Name()))
}
