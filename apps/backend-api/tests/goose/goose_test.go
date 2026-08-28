package goose_test

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
	"time"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/dolthub/go-mysql-server/driver"
	"github.com/dolthub/go-mysql-server/memory"
	gmssql "github.com/dolthub/go-mysql-server/sql"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/sql
var migrations embed.FS

type databaseFactory func(*testing.T) *sql.DB

type mysqlProvider struct {
	provider gmssql.DatabaseProvider
}

func (p mysqlProvider) Resolve(dsn string, _ *driver.Options) (string, gmssql.DatabaseProvider, error) {
	return dsn, p.provider, nil
}

func migrationsRoot(t *testing.T) fs.FS {
	t.Helper()
	root, err := fs.Sub(migrations, "testdata")
	require.NoError(t, err)
	return root
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
	config := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Database("goose").
		Username("postgres").
		Password("postgres").
		RuntimePath(filepath.Join(t.TempDir(), "postgres")).
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

func TestMigrationsMySQLAndPostgres(t *testing.T) {
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
			ctx := t.Context()
			db := tt.open(t)
			root := migrationsRoot(t)
			versionTable := "goose_widgets_" + tt.name
			source := goosex.Source{FS: root, TableName: versionTable}
			upToDate, err := goosex.UpToDate(ctx, db, source, tt.dialect)
			require.NoError(t, err)
			assert.False(t, upToDate)
			require.NoError(t, goosex.Migrate(ctx, db, source, tt.dialect))
			_, err = db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'first')`)
			require.NoError(t, err)
			assert.Equal(t, 1, widgetCount(t, ctx, db))
			require.NoError(t, goosex.Migrate(ctx, db, source, tt.dialect))
			require.NoError(t, goosex.MigrateDown(ctx, db, source, tt.dialect))
			assertWidgetsTableMissing(t, ctx, db)
			require.NoError(t, goosex.MigrateUp(ctx, db, source, tt.dialect))
			require.NoError(t, goosex.MigrateDownTo(ctx, db, source, tt.dialect, 0))
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
