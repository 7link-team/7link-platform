package audit

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/audit/model"
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

func TestOperationLogsAreAppendOnly(t *testing.T) {
	db := openTestDB(t)
	migrateTestDatabase(t, db)

	require.NoError(t, db.Exec(`INSERT INTO operation_logs
		(id, occurred_at, entry, operation, access, outcome)
		VALUES (?, ?, ?, ?, ?, ?)`, 1, time.Now().UTC(), "test", "create", "write", "success").Error)
	assert.Error(t, db.Exec(`UPDATE operation_logs SET operation = ? WHERE id = ?`, "changed", 1).Error)
	assert.Error(t, db.Exec(`DELETE FROM operation_logs WHERE id = ?`, 1).Error)

	var count int64
	require.NoError(t, db.Table("operation_logs").Where("id = ?", 1).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestAppendOnlyMigrationDownAndUp(t *testing.T) {
	db := openTestDB(t)
	migrateTestDatabase(t, db)
	require.NoError(t, db.Exec(`INSERT INTO operation_logs
		(id, occurred_at, entry, operation, access, outcome)
		VALUES (?, ?, ?, ?, ?, ?)`, 1, time.Now().UTC(), "test", "create", "write", "success").Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()

	require.NoError(t, goosex.MigrateDown(t.Context(), sqlDB, source, "sqlite"))
	require.NoError(t, db.Exec(`UPDATE operation_logs SET operation = ? WHERE id = ?`, "after-down", 1).Error)
	require.NoError(t, goosex.MigrateUp(t.Context(), sqlDB, source, "sqlite"))
	assert.Error(t, db.Exec(`UPDATE operation_logs SET operation = ? WHERE id = ?`, "after-up", 1).Error)
	assert.Error(t, db.Exec(`DELETE FROM operation_logs WHERE id = ?`, 1).Error)
}

func TestMySQLAppendOnlyMigrationClearsEveryTriggerBeforeCreate(t *testing.T) {
	raw, err := migrationsFS.ReadFile("sql/mysql/00002_operation_logs_append_only.sql")
	require.NoError(t, err)
	migration := string(raw)
	created := regexp.MustCompile(`(?m)^CREATE TRIGGER \{\{\.Prefix\}\}(\S+)$`).FindAllStringSubmatch(migration, -1)
	require.NotEmpty(t, created)
	for _, match := range created {
		name := match[1]
		dropAt := strings.Index(migration, "DROP TRIGGER IF EXISTS {{.Prefix}}"+name+";")
		createAt := strings.Index(migration, "CREATE TRIGGER {{.Prefix}}"+name)
		assert.GreaterOrEqual(t, dropAt, 0, "trigger %s must be cleared for retry", name)
		assert.Less(t, dropAt, createAt, "trigger %s must be cleared before creation", name)
	}
}

func TestOperationLogTableName(t *testing.T) {
	assert.Equal(t, model.OperationLog{}.TableName(), (OperationLog{}).TableName())
}

func migrateTestDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, (&Module{}).MigrationSource(), db.Dialector.Name()))
}
