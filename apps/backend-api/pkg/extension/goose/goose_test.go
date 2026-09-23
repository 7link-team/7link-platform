package goosex

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var testMigrations = fstest.MapFS{
	"sql/sqlite/00001_widgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (
    id   INTEGER NOT NULL PRIMARY KEY,
    name TEXT    NOT NULL
);

-- +goose Down
DROP TABLE widgets;
`)},
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db, err := gormDB.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(t.Context()))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func TestMigrationsSQLite(t *testing.T) {
	ctx := t.Context()
	db := openSQLite(t)
	root := testMigrations
	const versionTable = "goose_widgets_sqlite"
	source := Source{FS: root, TableName: versionTable}
	status, err := Inspect(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, Status{
		Target:   1,
		State:    StatePending,
		Embedded: []int64{1},
		Applied:  []int64{},
		Pending:  []int64{1},
		Unknown:  []int64{},
	}, status)

	upToDate, err := UpToDate(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.False(t, upToDate, "fresh database must not be up to date")

	require.NoError(t, Migrate(ctx, db, source, "sqlite"))
	status, err = Inspect(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, StateClean, status.State)
	assert.Equal(t, int64(1), status.Current)
	assert.Equal(t, []int64{1}, status.Applied)
	_, err = db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'first')`)
	require.NoError(t, err)
	assert.Equal(t, 1, widgetCount(t, ctx, db))

	// A second run must preserve both the schema and existing data.
	require.NoError(t, Migrate(ctx, db, source, "sqlite"))
	assert.Equal(t, 1, widgetCount(t, ctx, db))
	upToDate, err = UpToDate(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.True(t, upToDate)

	require.NoError(t, MigrateDown(ctx, db, source, "sqlite"))
	assertWidgetsTableMissing(t, ctx, db)
	upToDate, err = UpToDate(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.False(t, upToDate)

	require.NoError(t, MigrateUpTo(ctx, db, source, "sqlite", 1))
	require.NoError(t, MigrateDownTo(ctx, db, source, "sqlite", 0))
	require.NoError(t, MigrateDownTo(ctx, db, source, "sqlite", 0))
	assertWidgetsTableMissing(t, ctx, db)

	require.NoError(t, MigrateUp(ctx, db, source, "sqlite"))
	assert.ErrorContains(t, MigrateUpTo(ctx, db, source, "sqlite", 0), "lower version")
	assert.ErrorContains(t, MigrateUpTo(ctx, db, source, "sqlite", 2), "exceeds embedded")
	assert.ErrorContains(t, MigrateDownTo(ctx, db, source, "sqlite", 2), "higher version")
	assert.ErrorContains(t, MigrateDownTo(ctx, db, source, "sqlite", -1), "cannot be negative")
}

func TestInspectDetectsDatabaseAheadOfEmbeddedMigrations(t *testing.T) {
	db := openSQLite(t)
	root := testMigrations
	const versionTable = "goose_widgets_ahead"
	source := Source{FS: root, TableName: versionTable}
	require.NoError(t, Migrate(t.Context(), db, source, "sqlite"))
	_, err := db.ExecContext(t.Context(),
		"INSERT INTO "+versionTable+" (version_id, is_applied) VALUES (?, ?)", 2, true,
	)
	require.NoError(t, err)

	status, err := Inspect(t.Context(), db, source, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, StateAhead, status.State)
	assert.Equal(t, int64(2), status.Current)
	assert.Equal(t, int64(1), status.Target)
	assert.Equal(t, []int64{2}, status.Unknown)
	assert.ErrorContains(t, Migrate(t.Context(), db, source, "sqlite"), "unavailable in embedded SQL")

	upToDate, err := UpToDate(t.Context(), db, source, "sqlite")
	require.NoError(t, err)
	assert.False(t, upToDate)
}

func TestVersionAppliedUsesLatestPrefixedGooseState(t *testing.T) {
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db, err := gormDB.DB()
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	source := Source{FS: testMigrations, TableName: "goose_widgets", Prefix: "test_"}
	require.NoError(t, Migrate(t.Context(), db, source, "sqlite"))
	applied, err := VersionApplied(t.Context(), gormDB, source, 1)
	require.NoError(t, err)
	assert.True(t, applied)

	_, err = db.ExecContext(t.Context(),
		"INSERT INTO test_goose_widgets (version_id, is_applied) VALUES (?, ?)", 1, false,
	)
	require.NoError(t, err)
	applied, err = VersionApplied(t.Context(), gormDB, source, 1)
	require.NoError(t, err)
	assert.False(t, applied)

	_, err = VersionApplied(t.Context(), gormDB, source, 2)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = VersionApplied(t.Context(), nil, source, 1)
	assert.ErrorContains(t, err, "nil database")
	_, err = VersionApplied(t.Context(), gormDB, Source{TableName: "goose;drop"}, 1)
	assert.ErrorContains(t, err, "invalid version table")
}

func widgetCount(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM widgets`).Scan(&count))
	return count
}

func assertWidgetsTableMissing(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (2, 'missing')`)
	assert.Error(t, err)
}

func TestMigrateRejectsUnsupportedDialect(t *testing.T) {
	err := Migrate(t.Context(), nil, Source{FS: testMigrations, TableName: "goose_bad_dialect"}, "oracle")
	assert.Error(t, err)
}

func TestResolveDialect(t *testing.T) {
	for _, dialect := range []string{"sqlite", "sqlite3", "mysql", "postgres", "postgresql", "pgsql", "pg", " PGSQL "} {
		_, subdir, err := ResolveDialect(dialect)
		require.NoError(t, err, "dialect %q", dialect)
		assert.NotEmpty(t, subdir)
	}
	_, _, err := ResolveDialect("oracle")
	assert.Error(t, err)
}

func TestMigrationFailuresArePropagated(t *testing.T) {
	db := openSQLite(t)
	require.NoError(t, db.Close())
	root := testMigrations

	assert.Error(t, Migrate(t.Context(), db, Source{FS: root, TableName: "goose_closed_migrate"}, "sqlite"))
	assert.Error(t, MigrateUp(t.Context(), db, Source{FS: root, TableName: "goose_closed_up"}, "sqlite"))
	assert.Error(t, MigrateUpTo(t.Context(), db, Source{FS: root, TableName: "goose_closed_up_to"}, "sqlite", 1))
	assert.Error(t, MigrateDown(t.Context(), db, Source{FS: root, TableName: "goose_closed_down"}, "sqlite"))
	assert.Error(t, MigrateDownTo(t.Context(), db, Source{FS: root, TableName: "goose_closed_down_to"}, "sqlite", 0))

	empty := fstest.MapFS{"unrelated.txt": {Data: []byte("value")}}
	_, err := newProvider(db, Source{FS: empty, TableName: "goose_missing_fs"}, "sqlite")
	assert.ErrorContains(t, err, "create provider")
}

func TestValidateStatusRejectsDivergedHistory(t *testing.T) {
	err := ValidateStatus(Status{
		Current: 2,
		Target:  2,
		Pending: []int64{1},
	})
	assert.ErrorContains(t, err, "embedded and applied migrations diverge")
	assert.NoError(t, ValidateStatus(Status{Current: 1, Target: 2, Pending: []int64{2}}))
}
