package gormx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNowMillisExpr(t *testing.T) {
	for _, dialect := range []string{"sqlite", "sqlite3", "mysql", "postgres", "postgresql", "pgsql", "pg", " PGSQL "} {
		assert.NotEmpty(t, NowMillisExpr(dialect), dialect)
	}
	assert.Panics(t, func() { NowMillisExpr("oracle") })
}

// TestDatabaseClockMillisReadsTheDatabaseClock covers the one call site of
// NowMillisExpr: lease and replay windows read the shared database clock so a
// node whose own clock runs fast cannot reclaim another node's lease early.
func TestDatabaseClockMillisReadsTheDatabaseClock(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	millis, err := DatabaseClockMillis(t.Context(), db)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(millis), time.Minute)
}

// TestDatabaseClockMillisRefusesANilHandle keeps a missing database from
// reading as a clock of zero, which would expire every lease at once.
func TestDatabaseClockMillisRefusesANilHandle(t *testing.T) {
	_, err := DatabaseClockMillis(t.Context(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a database handle")
}

// TestDatabaseClockMillisPropagatesAFailingQuery keeps a broken connection
// from reporting a time the caller would then trust.
func TestDatabaseClockMillisPropagatesAFailingQuery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = DatabaseClockMillis(t.Context(), db)
	require.Error(t, err)
}
