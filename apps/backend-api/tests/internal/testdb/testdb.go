// Package testdb provides integration-test database engines without adding
// their heavy dependencies to the backend module.
package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/driver"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/server"
	gmssql "github.com/dolthub/go-mysql-server/sql"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type mysqlProvider struct {
	provider gmssql.DatabaseProvider
}

func (p mysqlProvider) Resolve(dsn string, _ *driver.Options) (string, gmssql.DatabaseProvider, error) {
	return dsn, p.provider, nil
}

// OpenMySQL opens the in-process go-mysql-server engine. It exercises the
// MySQL dialect but is not a substitute for the real MySQL 8 gate.
func OpenMySQL(t *testing.T) *sql.DB {
	t.Helper()

	const databaseName = "integration"
	database := memory.NewDatabase(databaseName)
	database.BaseDatabase.EnablePrimaryKeyIndexes()
	drv := driver.New(mysqlProvider{provider: memory.NewDBProvider(database)}, nil)
	t.Cleanup(func() {
		if err := drv.Close(); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("close MySQL engine: %v", err)
		}
	})
	connector, err := drv.OpenConnector(databaseName)
	require.NoError(t, err)
	db := sql.OpenDB(connector)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)
	require.NoError(t, db.PingContext(t.Context()))
	_, err = db.ExecContext(t.Context(), "USE "+databaseName)
	require.NoError(t, err)
	return db
}

// OpenMySQLWithDSN starts an in-process go-mysql-server engine on a TCP
// listener and returns both its pool and a connection string usable by child
// processes. It exercises the MySQL dialect but is not a substitute for the
// real MySQL 8 gate.
func OpenMySQLWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()

	const databaseName = "integration"
	database := memory.NewDatabase(databaseName)
	database.BaseDatabase.EnablePrimaryKeyIndexes()
	provider := memory.NewDBProvider(database)
	engine := sqle.NewDefault(provider)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	mysqlServer, err := server.NewServer(
		server.Config{Listener: listener}, engine, gmssql.NewContext,
		memory.NewSessionBuilder(provider), nil,
	)
	require.NoError(t, err)
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- mysqlServer.Start() }()

	dsn := fmt.Sprintf("root@tcp(%s)/%s?multiStatements=true", listener.Addr(), databaseName)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return db.PingContext(t.Context()) == nil
	}, 5*time.Second, 10*time.Millisecond)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		require.NoError(t, mysqlServer.Close())
		select {
		case startErr := <-serverErrors:
			if startErr != nil && !errors.Is(startErr, net.ErrClosed) {
				t.Errorf("serve MySQL: %v", startErr)
			}
		case <-time.After(time.Second):
			t.Error("MySQL server did not stop")
		}
		engine.Close()
	})
	return db, dsn
}

// OpenPostgres starts an isolated embedded PostgreSQL 16 instance.
func OpenPostgres(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := OpenPostgresWithDSN(t)
	return db
}

// OpenPostgresWithDSN starts an isolated embedded PostgreSQL 16 instance and
// returns both its pool and connection string for tests that open another ORM pool.
func OpenPostgresWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()

	const maxStartAttempts = 3
	runtimeRoot := t.TempDir()
	var (
		postgres *embeddedpostgres.EmbeddedPostgres
		port     uint32
		startErr error
	)
	for attempt := range maxStartAttempts {
		port = freeTCPPort(t)
		config := embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V16).
			Port(port).
			Database("integration").
			Username("postgres").
			Password("postgres").
			RuntimePath(filepath.Join(runtimeRoot, fmt.Sprintf("postgres-%d", attempt))).
			StartTimeout(time.Minute).
			Logger(io.Discard)
		postgres = embeddedpostgres.NewDatabase(config)
		startErr = postgres.Start()
		if startErr == nil || !isPostgresPortConflict(startErr) {
			break
		}
	}
	require.NoError(t, startErr)
	t.Cleanup(func() { require.NoError(t, postgres.Stop()) })
	dsn := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/integration?sslmode=disable", port)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	return db, dsn
}

// OpenSQLite opens an isolated file-backed SQLite database with foreign keys
// enabled on every connection through the driver DSN.
func OpenSQLite(t *testing.T) *sql.DB {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "integration.sqlite") + "?_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	return db
}

func isPostgresPortConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already listening on port") ||
		strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

func freeTCPPort(t *testing.T) uint32 {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { require.NoError(t, listener.Close()) }()
	return uint32(listener.Addr().(*net.TCPAddr).Port)
}
