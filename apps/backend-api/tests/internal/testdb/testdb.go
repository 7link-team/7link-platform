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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	provider            gmssql.DatabaseProvider
	transactionProvider gmssql.DatabaseProvider
}

func (p mysqlProvider) Resolve(dsn string, _ *driver.Options) (string, gmssql.DatabaseProvider, error) {
	return dsn, p.provider, nil
}

// The embedded session commits only concrete memory database types, while the
// query provider applies the DDL bookkeeping adapter to that same database.
func (p mysqlProvider) NewSession(_ context.Context, _ uint32, _ *driver.Connector) (gmssql.Session, error) {
	return memory.NewSession(gmssql.NewBaseSession(), p.transactionProvider), nil
}

// mysqlMemoryDatabase repairs the embedded engine's DDL bookkeeping: dropping
// a table must drop its declared foreign keys, and renaming must retarget them.
// Without this, rebuild migrations leave stale constraints on the old table.
type mysqlMemoryDatabase struct {
	*memory.Database
}

func (d *mysqlMemoryDatabase) DropTable(ctx *gmssql.Context, name string) error {
	if err := d.Database.DropTable(ctx, name); err != nil {
		return err
	}
	collection := d.GetForeignKeyCollection()
	for _, key := range append([]gmssql.ForeignKeyConstraint(nil), collection.Keys()...) {
		if strings.EqualFold(key.Table, name) {
			collection.DropFK(key.Name)
		}
	}
	return nil
}

func (d *mysqlMemoryDatabase) RenameTable(ctx *gmssql.Context, oldName, newName string) error {
	if err := d.Database.RenameTable(ctx, oldName, newName); err != nil {
		return err
	}
	for i := range d.GetForeignKeyCollection().Keys() {
		key := &d.GetForeignKeyCollection().Keys()[i]
		if strings.EqualFold(key.Table, oldName) {
			key.Table = newName
		}
		if strings.EqualFold(key.ParentTable, oldName) {
			key.ParentTable = newName
		}
	}
	table, found, err := d.GetTableInsensitive(ctx, newName)
	if err != nil {
		return err
	}
	if !found {
		return gmssql.ErrTableNotFound.New(newName)
	}
	indexes, err := table.(*memory.Table).GetIndexes(ctx)
	if err != nil {
		return err
	}
	for _, index := range indexes {
		if current, ok := index.(*memory.Index); ok {
			current.Tbl = table.(*memory.Table)
			current.TableName = newName
		}
	}
	return nil
}

// OpenMySQL opens the in-process go-mysql-server engine. It exercises the
// MySQL dialect but is not a substitute for the real MySQL 8 gate.
func OpenMySQL(t *testing.T) *sql.DB {
	t.Helper()

	const databaseName = "integration"
	database := &mysqlMemoryDatabase{Database: memory.NewDatabase(databaseName)}
	database.BaseDatabase.EnablePrimaryKeyIndexes()
	drv := driver.New(mysqlProvider{provider: memory.NewDBProvider(database), transactionProvider: memory.NewDBProvider(database.Database)}, nil)
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
	database := &mysqlMemoryDatabase{Database: memory.NewDatabase(databaseName)}
	database.BaseDatabase.EnablePrimaryKeyIndexes()
	provider := memory.NewDBProvider(database)
	engine := sqle.NewDefault(provider)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	mysqlServer, err := server.NewServer(
		server.Config{Listener: listener}, engine, gmssql.NewContext,
		memory.NewSessionBuilder(memory.NewDBProvider(database.Database)), nil,
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
		if closeErr := engine.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
			t.Errorf("close MySQL engine: %v", closeErr)
		}
	})
	return db, dsn
}

// OpenPostgres hands a test its own PostgreSQL 16 database. See
// OpenPostgresWithDSN for what the test shares with its neighbours and what it
// does not.
func OpenPostgres(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := OpenPostgresWithDSN(t)
	return db
}

// OpenPostgresWithDSN hands a test its own PostgreSQL 16 database and the
// connection string for it, for tests that open another ORM pool.
//
// The server behind it is shared by the whole test binary and the isolation is
// the database, not the instance. Starting an instance means running initdb,
// which measured at 46 seconds here, so a server per test spent most of a test
// run initializing servers: tests/migration alone took 782 seconds for work
// that is mostly DDL. A database created on a running server costs
// milliseconds, and two databases on one server share nothing a migration test
// can observe -- separate catalogs, separate schemas, separate extensions.
//
// A package that calls this must stop the server from TestMain by way of
// ShutdownPostgres, or it leaves a postgres process and its data directory
// behind when the binary exits.
func OpenPostgresWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()

	port := sharedPostgres(t)
	name := fmt.Sprintf("integration_%d", databaseSequence.Add(1))

	admin, err := sql.Open("postgres", postgresDSN(port, "integration"))
	require.NoError(t, err)
	defer func() { require.NoError(t, admin.Close()) }()
	_, err = admin.ExecContext(t.Context(), "CREATE DATABASE "+name)
	require.NoError(t, err, "create the test database")

	// Cleanup is last-in-first-out, so the pool registered below closes before
	// this runs: PostgreSQL refuses to drop a database that still has sessions.
	t.Cleanup(func() {
		dropper, dropErr := sql.Open("postgres", postgresDSN(port, "integration"))
		require.NoError(t, dropErr)
		defer func() { require.NoError(t, dropper.Close()) }()
		_, dropErr = dropper.Exec("DROP DATABASE IF EXISTS " + name)
		require.NoError(t, dropErr)
	})

	dsn := postgresDSN(port, name)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	return db, dsn
}

func postgresDSN(port uint32, database string) string {
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s?sslmode=disable", port, database)
}

var (
	databaseSequence atomic.Uint64
	postgresOnce     sync.Once
	postgresServer   *embeddedpostgres.EmbeddedPostgres
	postgresRoot     string
	postgresPort     uint32
	postgresErr      error
)

// sharedPostgres starts the one server this binary uses and reports its port.
// Every caller after the first waits for that start rather than repeating it.
func sharedPostgres(t *testing.T) uint32 {
	t.Helper()
	postgresOnce.Do(func() {
		postgresRoot, postgresErr = os.MkdirTemp("", "testdb-postgres-")
		if postgresErr != nil {
			return
		}
		const maxStartAttempts = 3
		for attempt := range maxStartAttempts {
			postgresPort = freeTCPPort(t)
			config := embeddedpostgres.DefaultConfig().
				Version(embeddedpostgres.V16).
				Port(postgresPort).
				Database("integration").
				Username("postgres").
				Password("postgres").
				RuntimePath(filepath.Join(postgresRoot, fmt.Sprintf("postgres-%d", attempt))).
				StartTimeout(time.Minute).
				Logger(io.Discard)
			postgresServer = embeddedpostgres.NewDatabase(config)
			postgresErr = postgresServer.Start()
			if postgresErr == nil || !isPostgresPortConflict(postgresErr) {
				break
			}
		}
	})
	require.NoError(t, postgresErr, "start the shared postgres server")
	return postgresPort
}

// ShutdownPostgres stops the shared server and removes its data directory. It
// is a no-op for a binary whose tests never asked for PostgreSQL, so every
// TestMain can defer to it unconditionally. Call it from TestMain, after
// m.Run, and return m.Run's code: a test binary that exits without it leaves
// the server running.
func ShutdownPostgres() error {
	var stopErr error
	if postgresServer != nil {
		stopErr = postgresServer.Stop()
		postgresServer = nil
	}
	if postgresRoot != "" {
		stopErr = errors.Join(stopErr, removeEventually(postgresRoot, os.RemoveAll, time.Sleep))
		postgresRoot = ""
	}
	return stopErr
}

// postgresRemoveBudget bounds how long ShutdownPostgres waits for the data
// directory to become removable.
const postgresRemoveBudget = 10 * time.Second

// removeEventually removes path, retrying with a doubling delay until the
// budget is spent. Stop returns once the server has shut down, but on Windows
// its child processes can still hold the extracted binaries and libraries for a
// moment, and removing an open file there is refused. A directory that is still
// refused after the budget is reported with the last error, not ignored.
func removeEventually(path string, remove func(string) error, sleep func(time.Duration)) error {
	delay := 100 * time.Millisecond
	var waited time.Duration
	for {
		err := remove(path)
		if err == nil {
			return nil
		}
		if waited >= postgresRemoveBudget {
			return fmt.Errorf("remove %s after waiting %s: %w", path, waited, err)
		}
		sleep(delay)
		waited += delay
		delay = min(delay*2, 2*time.Second)
	}
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
	addr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address is not TCP")
	return uint32(addr.Port)
}
