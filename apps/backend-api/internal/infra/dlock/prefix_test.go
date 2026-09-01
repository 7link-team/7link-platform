package dlock

import (
	"context"
	"path/filepath"
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testPrefix = "wp_"

// newPrefixedDB migrates only the prefixed schema. The unprefixed tables are
// deliberately absent, so any statement that forgets the prefix fails loudly
// instead of silently reading an empty table.
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

	source := (&Module{}).MigrationSource()
	source.Prefix = testPrefix
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	return db
}

func TestMigrationCreatesOnlyPrefixedTables(t *testing.T) {
	db := newPrefixedDB(t)
	assert.True(t, db.Migrator().HasTable(testPrefix+"dlocks"), "prefixed table must exist")
	assert.False(t, db.Migrator().HasTable("dlocks"), "unprefixed table must not exist")
	assert.True(t, db.Migrator().HasTable(testPrefix+"goose_dlock"), "version table must be prefixed too")
	assert.False(t, db.Migrator().HasTable("goose_dlock"))
}

func TestLockLifecycleAgainstPrefixedSchema(t *testing.T) {
	db := newPrefixedDB(t)
	locker := NewLocker(db, WithTablePrefix(testPrefix))

	// Every statement the locker issues runs here: acquire, contend, refresh,
	// release. Any one that named the unprefixed table would error.
	first, err := locker.TryLock(t.Context(), "job")
	require.NoError(t, err)

	_, err = locker.TryLock(t.Context(), "job")
	require.ErrorIs(t, err, ErrNotAcquired)

	require.NoError(t, first.Refresh(t.Context()))
	require.NoError(t, first.Unlock(context.Background()))

	second, err := locker.TryLock(t.Context(), "job")
	require.NoError(t, err)
	require.NoError(t, second.Unlock(context.Background()))
}

func TestLockerWithoutPrefixCannotSeePrefixedSchema(t *testing.T) {
	// The guard for the guard: if the prefix were being ignored, the test above
	// would pass with an unprefixed locker too. It must not.
	db := newPrefixedDB(t)
	_, err := NewLocker(db).TryLock(t.Context(), "job")
	require.Error(t, err, "an unprefixed locker must not reach the prefixed table")
}

func TestInvalidPrefixIsRejectedBeforeReachingDDL(t *testing.T) {
	for _, prefix := range []string{"WP_", "wp-", "1wp", "wp_;drop table x;--", "wp_'"} {
		assert.Error(t, goosex.ValidatePrefix(prefix), "must reject %q", prefix)
	}
	for _, prefix := range []string{"", "wp_", "tenant_a", "t1"} {
		assert.NoError(t, goosex.ValidatePrefix(prefix), "must accept %q", prefix)
	}
}
