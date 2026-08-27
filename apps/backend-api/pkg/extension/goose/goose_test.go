package goosex

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dolthub/go-mysql-server/driver"
	"github.com/dolthub/go-mysql-server/memory"
	gmssql "github.com/dolthub/go-mysql-server/sql"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

//go:embed testdata/sql
var testdataFS embed.FS

type databaseFactory func(*testing.T) *sql.DB

type mysqlProvider struct {
	provider gmssql.DatabaseProvider
}

func (p mysqlProvider) Resolve(dsn string, _ *driver.Options) (string, gmssql.DatabaseProvider, error) {
	return dsn, p.provider, nil
}

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
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(t.Context()))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func openMySQL(t *testing.T) *sql.DB {
	t.Helper()
	const databaseName = "goose"

	database := memory.NewDatabase(databaseName)
	database.BaseDatabase.EnablePrimaryKeyIndexes()
	drv := driver.New(mysqlProvider{provider: memory.NewDBProvider(database)}, nil)
	connector, err := drv.OpenConnector(databaseName)
	require.NoError(t, err)

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(t.Context()))
	_, err = db.ExecContext(t.Context(), "USE "+databaseName)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		if err := drv.Close(); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("close MySQL engine: %v", err)
		}
	})
	return db
}

func openPostgres(t *testing.T) *sql.DB {
	t.Helper()
	port := freeTCPPort(t)
	runtimePath := filepath.Join(t.TempDir(), "postgres")
	config := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Database("goose").
		Username("postgres").
		Password("postgres").
		RuntimePath(runtimePath).
		StartTimeout(time.Minute).
		Logger(io.Discard)

	postgres := embeddedpostgres.NewDatabase(config)
	require.NoError(t, postgres.Start())
	t.Cleanup(func() { require.NoError(t, postgres.Stop()) })

	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/goose?sslmode=disable", port)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.PingContext(t.Context()))
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func freeTCPPort(t *testing.T) uint32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()
	return uint32(listener.Addr().(*net.TCPAddr).Port)
}

func TestMigrations_AllDialects(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		open    databaseFactory
	}{
		{name: "sqlite", dialect: "sqlite", open: openSQLite},
		{name: "mysql", dialect: "mysql", open: openMySQL},
		{name: "postgres", dialect: "postgres", open: openPostgres},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			db := tt.open(t)
			root := migrationsRoot(t)
			versionTable := "goose_widgets_" + tt.name

			upToDate, err := UpToDate(ctx, db, root, tt.dialect, versionTable)
			require.NoError(t, err)
			assert.False(t, upToDate, "fresh database must not be up to date")

			require.NoError(t, Run(ctx, db, root, tt.dialect, versionTable))
			_, err = db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'first')`)
			require.NoError(t, err)
			assert.Equal(t, 1, widgetCount(t, ctx, db))

			// A second run must preserve both the schema and existing data.
			require.NoError(t, Run(ctx, db, root, tt.dialect, versionTable))
			assert.Equal(t, 1, widgetCount(t, ctx, db))
			upToDate, err = UpToDate(ctx, db, root, tt.dialect, versionTable)
			require.NoError(t, err)
			assert.True(t, upToDate)

			require.NoError(t, Down(ctx, db, root, tt.dialect, versionTable))
			assertWidgetsTableMissing(t, ctx, db)
			upToDate, err = UpToDate(ctx, db, root, tt.dialect, versionTable)
			require.NoError(t, err)
			assert.False(t, upToDate)

			require.NoError(t, Run(ctx, db, root, tt.dialect, versionTable))
			require.NoError(t, DownTo(ctx, db, root, tt.dialect, versionTable, 0))
			assertWidgetsTableMissing(t, ctx, db)
		})
	}
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
