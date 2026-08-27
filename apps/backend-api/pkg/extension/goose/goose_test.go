package goosex

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

//go:embed testdata/sql
var testdataFS embed.FS

// migrationsRoot re-roots the embedded FS at "sql" so it matches what Run
// expects (it resolves the "sql/<dialect>" subdirectory internally).
func migrationsRoot(t *testing.T) fs.FS {
	t.Helper()
	root, err := fs.Sub(testdataFS, "testdata")
	require.NoError(t, err)
	return root
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
	root := migrationsRoot(t)
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

	upToDate, err := UpToDate(ctx, db, root, "sqlite", versionTable)
	require.NoError(t, err)
	assert.False(t, upToDate, "fresh database must not be up to date")

	require.NoError(t, Run(ctx, db, root, "sqlite", versionTable))
	status, err = Inspect(ctx, db, source, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, StateClean, status.State)
	assert.Equal(t, int64(1), status.Current)
	assert.Equal(t, []int64{1}, status.Applied)
	_, err = db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'first')`)
	require.NoError(t, err)
	assert.Equal(t, 1, widgetCount(t, ctx, db))

	// A second run must preserve both the schema and existing data.
	require.NoError(t, Run(ctx, db, root, "sqlite", versionTable))
	assert.Equal(t, 1, widgetCount(t, ctx, db))
	upToDate, err = UpToDate(ctx, db, root, "sqlite", versionTable)
	require.NoError(t, err)
	assert.True(t, upToDate)

	require.NoError(t, Down(ctx, db, root, "sqlite", versionTable))
	assertWidgetsTableMissing(t, ctx, db)
	upToDate, err = UpToDate(ctx, db, root, "sqlite", versionTable)
	require.NoError(t, err)
	assert.False(t, upToDate)

	require.NoError(t, Run(ctx, db, root, "sqlite", versionTable))
	require.NoError(t, DownTo(ctx, db, root, "sqlite", versionTable, 0))
	assertWidgetsTableMissing(t, ctx, db)
}

func TestInspectDetectsDatabaseAheadOfEmbeddedMigrations(t *testing.T) {
	db := openSQLite(t)
	root := migrationsRoot(t)
	const versionTable = "goose_widgets_ahead"
	require.NoError(t, Run(t.Context(), db, root, "sqlite", versionTable))
	_, err := db.ExecContext(t.Context(),
		"INSERT INTO "+versionTable+" (version_id, is_applied) VALUES (?, ?)", 2, true,
	)
	require.NoError(t, err)

	status, err := Inspect(t.Context(), db, Source{FS: root, TableName: versionTable}, "sqlite")
	require.NoError(t, err)
	assert.Equal(t, StateAhead, status.State)
	assert.Equal(t, int64(2), status.Current)
	assert.Equal(t, int64(1), status.Target)
	assert.Equal(t, []int64{2}, status.Unknown)

	upToDate, err := UpToDate(t.Context(), db, root, "sqlite", versionTable)
	require.NoError(t, err)
	assert.False(t, upToDate)
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

func TestRun_UnsupportedDialect(t *testing.T) {
	err := Run(t.Context(), nil, migrationsRoot(t), "oracle", "goose_bad_dialect")
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
	root := migrationsRoot(t)

	assert.ErrorContains(t, Run(t.Context(), db, root, "sqlite", "goose_closed_run"), "run migrations")
	assert.ErrorContains(t, Down(t.Context(), db, root, "sqlite", "goose_closed_down"), "rollback migration")
	assert.ErrorContains(t, DownTo(t.Context(), db, root, "sqlite", "goose_closed_down_to", 0), "rollback migrations")

	empty := fstest.MapFS{"unrelated.txt": {Data: []byte("value")}}
	_, err := newProvider(db, empty, "sqlite", "goose_missing_fs")
	assert.ErrorContains(t, err, "create provider")
}
