package goose_test

import (
	"database/sql"
	"testing"
	"time"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mysqldriver "gorm.io/driver/mysql"
	postgresdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const queryPrefix = "wp_"

// gormOver reuses the already-open integration connection so the ORM and the
// migration runner talk to the same database.
func gormOver(t *testing.T, dialect string, db *sql.DB) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector
	switch dialect {
	case "mysql":
		dialector = mysqldriver.New(mysqldriver.Config{Conn: db})
	case "postgres":
		dialector = postgresdriver.New(postgresdriver.Config{Conn: db})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return gormDB
}

// TestPrefixedModuleQueriesOnMySQLAndPostgres closes the gap the sqlite unit
// tests leave: they prove the prefix reaches every statement, but only on
// sqlite. Index-name collisions and identifier quoting differ per engine, and
// the generated queries render through each dialect's own dialector.
//
// Only the prefixed schema is migrated, so any statement still naming an
// unprefixed table fails against a table that does not exist.
func TestPrefixedModuleQueriesOnMySQLAndPostgres(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		open    databaseFactory
	}{
		{name: "mysql", dialect: "mysql", open: openMySQL},
		{name: "postgres", dialect: "postgres", open: openPostgres},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(wuid.EnvKey, "")
			ctx := t.Context()
			sqlDB := tt.open(t)
			gormDB := gormOver(t, tt.dialect, sqlDB)

			for _, source := range []goosex.Source{
				(&dlock.Module{}).MigrationSource(),
				(&wuid.Module{}).MigrationSource(),
				(&audit.Module{}).MigrationSource(),
			} {
				source.Prefix = queryPrefix
				require.NoError(t, goosex.Migrate(ctx, sqlDB, source, tt.dialect))
			}

			t.Run("dlock", func(t *testing.T) {
				locker := dlock.NewLocker(gormDB, dlock.WithTablePrefix(queryPrefix))
				held, err := locker.TryLock(ctx, "job")
				require.NoError(t, err)

				_, err = locker.TryLock(ctx, "job")
				assert.ErrorIs(t, err, dlock.ErrNotAcquired)

				require.NoError(t, held.Refresh(ctx))
				require.NoError(t, held.Unlock(ctx))
			})

			t.Run("wuid", func(t *testing.T) {
				worker, err := wuid.Open(ctx, gormDB,
					wuid.WithLease(time.Minute), wuid.WithTablePrefix(queryPrefix))
				require.NoError(t, err)
				assert.True(t, worker.Alive())
				require.NoError(t, worker.Close(ctx))
			})

			t.Run("audit", func(t *testing.T) {
				sink := audit.NewGormSink(gormDB, queryPrefix)
				require.NoError(t, sink.Write(ctx, []audit.Event{{
					ID:         "2366488506482438144",
					OccurredAt: time.Now(),
					Entry:      audit.KindHTTP,
					Operation:  "POST /orders",
					Access:     audit.AccessWrite,
					Outcome:    audit.OutcomeSuccess,
				}}))
			})

			t.Run("unprefixed access fails", func(t *testing.T) {
				_, err := dlock.NewLocker(gormDB).TryLock(ctx, "job")
				assert.Error(t, err, "an unprefixed locker must not reach the prefixed table")
			})
		})
	}
}
