package audit

import (
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/audit/model"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testPrefix = "wp_"

func newPrefixedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "prefixed.sqlite")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	source := (&Module{}).MigrationSource()
	source.Prefix = testPrefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	return db
}

// TestMigrationPrefixesTablesAndIndexes covers the case that only shows up on
// postgres and sqlite, where index names are schema-global: two prefixed
// installations in one schema collide on index names unless those are
// prefixed too.
func TestMigrationPrefixesTablesAndIndexes(t *testing.T) {
	db := newPrefixedDB(t)
	assert.True(t, db.Migrator().HasTable(testPrefix+model.OperationLog{}.TableName()))
	assert.False(t, db.Migrator().HasTable(model.OperationLog{}.TableName()))

	var indexes []string
	require.NoError(t, db.Raw(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_%' OR name LIKE ?`,
		testPrefix+"idx_%").Scan(&indexes).Error)
	require.NotEmpty(t, indexes, "the migration must create named indexes")
	for _, name := range indexes {
		assert.Contains(t, name, testPrefix, "index %q must be prefixed", name)
	}

	var triggers []string
	require.NoError(t, db.Raw(
		`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`,
	).Scan(&triggers).Error)
	assert.ElementsMatch(t, []string{
		testPrefix + "trg_operation_logs_no_delete",
		testPrefix + "trg_operation_logs_no_update",
	}, triggers)
}

func TestGormSinkWritesToPrefixedTable(t *testing.T) {
	db := newPrefixedDB(t)
	sink := NewGormSink(db, testPrefix)

	require.NoError(t, sink.Write(t.Context(), []Event{{
		ID: "2366488506482438144", OccurredAt: time.Now(), Entry: KindHTTP,
		Operation: "POST /orders", Access: AccessWrite, Outcome: OutcomeSuccess,
	}}))

	var count int64
	require.NoError(t, db.Table(testPrefix+model.OperationLog{}.TableName()).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestGormSinkWithoutPrefixCannotSeePrefixedTable(t *testing.T) {
	db := newPrefixedDB(t)
	err := NewGormSink(db, "").Write(t.Context(), []Event{{
		ID: "2366488506482438144", OccurredAt: time.Now(), Entry: KindHTTP,
		Operation: "POST /orders", Access: AccessWrite, Outcome: OutcomeSuccess,
	}})
	require.Error(t, err, "an unprefixed sink must not reach the prefixed table")
}
