package wuid

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/dlock"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testPrefix = "wp_"

// newPrefixedDB migrates only the prefixed schema, for wuid and for the dlock
// table its allocation lock uses. The unprefixed tables are deliberately
// absent, so a statement that forgets the prefix fails instead of quietly
// reading an empty table.
func newPrefixedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "prefixed.sqlite")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	for _, source := range []goosex.Source{
		(&dlock.Module{}).MigrationSource(),
		(&Module{}).MigrationSource(),
	} {
		source.Prefix = testPrefix
		require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	}
	return db
}

func TestMigrationCreatesOnlyPrefixedTables(t *testing.T) {
	db := newPrefixedDB(t)
	for _, table := range []string{"worker_ids", "goose_wuid", "dlocks", "goose_dlock"} {
		assert.True(t, db.Migrator().HasTable(testPrefix+table), "%s must be prefixed", table)
		assert.False(t, db.Migrator().HasTable(table), "%s must not exist unprefixed", table)
	}
}

// TestWorkerLifecycleAgainstPrefixedSchema exercises the whole chain against a
// prefixed schema: the allocation lock, the reclaim scan, the insert, the
// deadline update, renewal, and release. Any statement still naming an
// unprefixed table errors, because that table does not exist.
func TestWorkerLifecycleAgainstPrefixedSchema(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newPrefixedDB(t)

	first, err := Open(t.Context(), db, WithLease(time.Minute), WithTablePrefix(testPrefix))
	require.NoError(t, err)
	assert.Equal(t, uint16(0), first.ID())

	second, err := Open(t.Context(), db, WithLease(time.Minute), WithTablePrefix(testPrefix))
	require.NoError(t, err)
	assert.Equal(t, uint16(1), second.ID(), "allocation must see the first worker's row")

	require.NoError(t, first.renew(t.Context()))
	require.NoError(t, first.Close(context.Background()))
	require.NoError(t, second.Close(context.Background()))

	// The released ID is reusable, which only works if the reclaim scan also
	// reads the prefixed table.
	third, err := Open(t.Context(), db, WithLease(time.Minute), WithTablePrefix(testPrefix))
	require.NoError(t, err)
	assert.Equal(t, uint16(0), third.ID())
	require.NoError(t, third.Close(context.Background()))
}

func TestWorkerWithoutPrefixCannotSeePrefixedSchema(t *testing.T) {
	t.Setenv(EnvKey, "")
	db := newPrefixedDB(t)
	_, err := Open(t.Context(), db, WithLease(time.Minute))
	require.Error(t, err, "an unprefixed worker must not reach the prefixed table")
}
