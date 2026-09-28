package gormx

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDatasourceOpenSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.sqlite")
	db, err := (Datasource{
		Type:            "sqlite3",
		DSN:             "sqlite://" + path,
		MaxOpenConns:    7,
		MaxIdleConns:    3,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Second,
	}).Open(t.Context())
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, sqlDB.PingContext(t.Context()))
	assert.Equal(t, 7, sqlDB.Stats().MaxOpenConnections)
}

func TestDatasourceOpenSQLiteEnforcesForeignKeysOnEveryConnection(t *testing.T) {
	db, err := (Datasource{
		Type:         "sqlite",
		DSN:          filepath.Join(t.TempDir(), "foreign-keys.sqlite"),
		MaxOpenConns: 3,
	}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	connections := make([]*sql.Conn, 0, 3)
	for range 3 {
		connection, connErr := sqlDB.Conn(t.Context())
		require.NoError(t, connErr)
		connections = append(connections, connection)
		t.Cleanup(func() { _ = connection.Close() })
	}
	for _, connection := range connections {
		var enabled int
		require.NoError(t, connection.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled))
		assert.Equal(t, 1, enabled)
	}

	require.NoError(t, connections[0].Close())
	require.NoError(t, db.Exec("CREATE TABLE parents (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE children (parent_id INTEGER NOT NULL REFERENCES parents(id))").Error)
	assert.ErrorIs(t, db.Exec("INSERT INTO children (parent_id) VALUES (404)").Error, gorm.ErrForeignKeyViolated)
}

func TestDatasourceOpenSQLiteRejectsDisabledForeignKeys(t *testing.T) {
	for _, parameter := range []string{
		"_foreign_keys=0", "_foreign_keys=no", "_foreign_keys=false", "_foreign_keys=off",
		"_fk=0", "_fk=no", "_fk=false", "_fk=off",
	} {
		t.Run(parameter, func(t *testing.T) {
			db, err := (Datasource{
				Type: "sqlite",
				DSN:  filepath.Join(t.TempDir(), "disabled.sqlite") + "?" + parameter,
			}).Open(t.Context())
			if db != nil {
				sqlDB, sqlErr := db.DB()
				if sqlErr == nil {
					_ = sqlDB.Close()
				}
			}
			assert.ErrorContains(t, err, "foreign keys cannot be disabled")
		})
	}
}

func TestDatasourceOpenSQLiteAcceptsEnabledForeignKeyAliases(t *testing.T) {
	for _, parameter := range []string{"_foreign_keys=yes", "_fk=true"} {
		t.Run(parameter, func(t *testing.T) {
			db, err := (Datasource{
				Type: "sqlite",
				DSN:  filepath.Join(t.TempDir(), "enabled.sqlite") + "?_busy_timeout=1000&" + parameter,
			}).Open(t.Context())
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			var enabled int
			require.NoError(t, sqlDB.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled))
			assert.Equal(t, 1, enabled)
		})
	}
}

func TestNormalizeSQLiteDSNDefaultsImmediateTransactions(t *testing.T) {
	for _, test := range []struct {
		name   string
		dsn    string
		wanted string
	}{
		{name: "default", dsn: "sqlite://database.sqlite", wanted: "immediate"},
		{name: "empty", dsn: "database.sqlite?_txlock=", wanted: "immediate"},
		{name: "explicit", dsn: "database.sqlite?_txlock=exclusive", wanted: "exclusive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn, err := normalizeSQLiteDSN(test.dsn)
			require.NoError(t, err)
			_, rawQuery, found := strings.Cut(dsn, "?")
			require.True(t, found)
			params, err := url.ParseQuery(rawQuery)
			require.NoError(t, err)
			assert.Equal(t, test.wanted, params.Get("_txlock"))
			assert.Equal(t, "on", params.Get("_foreign_keys"))
		})
	}
}

func TestDatasourceOpenSQLiteRejectsInvalidForeignKeyParameters(t *testing.T) {
	for _, query := range []string{
		"_foreign_keys=invalid",
		"_fk=",
		"_foreign_keys=on&_fk=off",
		"_foreign_keys=%zz",
	} {
		t.Run(query, func(t *testing.T) {
			_, err := (Datasource{
				Type: "sqlite",
				DSN:  filepath.Join(t.TempDir(), "invalid.sqlite") + "?" + query,
			}).Open(t.Context())
			assert.ErrorContains(t, err, "configure sqlite datasource")
		})
	}
}

func TestDatasourceTimezoneConfiguration(t *testing.T) {
	singapore, err := time.LoadLocation("Asia/Singapore")
	require.NoError(t, err)

	mysqlDSN, err := mysqlDSNWithTimezone("user:pass@tcp(localhost:3306)/app?parseTime=false", singapore)
	require.NoError(t, err)
	mysqlConfig, err := mysqldriver.ParseDSN(mysqlDSN)
	require.NoError(t, err)
	assert.True(t, mysqlConfig.ParseTime)
	assert.Equal(t, singapore.String(), mysqlConfig.Loc.String())
	assert.Equal(t, `'Asia/Singapore'`, mysqlConfig.Params["time_zone"])

	postgresConfig, err := postgresConfigWithTimezone(
		"postgres://user:pass@localhost:5432/app?timezone=America%2FNew_York",
		singapore,
	)
	require.NoError(t, err)
	assert.Equal(t, singapore.String(), postgresConfig.RuntimeParams["timezone"])
}

func TestDatasourceTimezoneDefaultsToUTCAndRejectsInvalidName(t *testing.T) {
	location, err := loadLocation("")
	require.NoError(t, err)
	assert.Equal(t, time.UTC, location)

	_, err = (Datasource{
		Type: "sqlite", DSN: filepath.Join(t.TempDir(), "invalid.sqlite"),
		Timezone: "Invalid/Timezone",
	}).Open(t.Context())
	assert.ErrorContains(t, err, "invalid datasource timezone")
}

func TestDatasourceValidation(t *testing.T) {
	_, err := (Datasource{Type: "oracle", DSN: "value"}).Open(t.Context())
	assert.ErrorContains(t, err, "unsupported datasource type")

	_, err = (Datasource{Type: "sqlite"}).Open(t.Context())
	assert.ErrorContains(t, err, "DSN is required")

	for _, dialect := range []string{"sqlite", "sqlite3", "mysql", "postgres", "postgresql", "pg", "pgsql"} {
		canonical, err := NormalizeDialect(dialect)
		require.NoError(t, err)
		assert.NotEmpty(t, canonical)
	}
}

func TestDatasourceDriverConfigurationErrors(t *testing.T) {
	_, err := (Datasource{Type: "mysql", DSN: "not-a-dsn"}).Open(t.Context())
	assert.ErrorContains(t, err, "configure mysql timezone")

	_, err = (Datasource{Type: "postgres", DSN: "%"}).Open(t.Context())
	assert.ErrorContains(t, err, "configure postgres timezone")
}

func TestDatasourcePingErrorsCloseConnectionPools(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := (Datasource{
		Type: "sqlite",
		DSN:  filepath.Join(t.TempDir(), "canceled.sqlite"),
	}).Open(canceled)
	assert.ErrorContains(t, err, "ping sqlite datasource")

	_, err = (Datasource{
		Type: "mysql",
		DSN:  "user:pass@tcp(127.0.0.1:1)/app?timeout=100ms",
	}).Open(t.Context())
	assert.ErrorContains(t, err, "mysql datasource")

	_, err = (Datasource{
		Type: "postgres",
		DSN:  "postgres://user:pass@127.0.0.1:1/app?connect_timeout=1",
	}).Open(t.Context())
	assert.ErrorContains(t, err, "ping postgres datasource")
}

func TestDatasourceRouterReadWriteFallbackAndClose(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"primary": {
			Type:     "sqlite",
			DSN:      filepath.Join(t.TempDir(), "primary.sqlite"),
			Writable: true,
		},
		"replica": {
			Type:     "sqlite",
			DSN:      filepath.Join(t.TempDir(), "replica.sqlite"),
			Readable: true,
		},
	})
	require.NoError(t, err)
	assert.True(t, router.HasWriter())
	writer := router.Write()
	reader := router.Read()
	assert.NotNil(t, writer)
	assert.NotNil(t, reader)
	assert.NotSame(t, writer, reader)
	require.NoError(t, router.Close())

	fallback, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"primary": {
			Type:     "sqlite",
			DSN:      filepath.Join(t.TempDir(), "fallback.sqlite"),
			Writable: true,
		},
	})
	require.NoError(t, err)
	assert.Same(t, fallback.Write(), fallback.Read())
	require.NoError(t, fallback.Close())
	require.NoError(t, fallback.Close())
}

func TestDatasourceRouterKeepsHealthyConnections(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"broken": {Type: "oracle", DSN: "unused", Writable: true},
		"healthy": {
			Type:     "sqlite",
			DSN:      filepath.Join(t.TempDir(), "healthy.sqlite"),
			Writable: true,
		},
	})
	assert.ErrorContains(t, err, `database "broken"`)
	require.NotNil(t, router.Write())
	require.NoError(t, router.Close())
	assert.NoError(t, (&DatasourceRouter{}).Close())
}

func TestDatasourceRouterRejectsUnusedDatasource(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"unused": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "unused.sqlite")},
	})
	assert.Nil(t, router)
	assert.ErrorContains(t, err, `datasource "unused" must be readable or writable`)

	empty, err := NewDatasourceRouter(t.Context(), false, nil)
	require.NoError(t, err)
	assert.False(t, empty.HasWriter())
	assert.Nil(t, empty.Write())
	assert.Nil(t, (*DatasourceRouter)(nil).Write())
	assert.Nil(t, (*DatasourceRouter)(nil).Read())
	assert.False(t, (*DatasourceRouter)(nil).HasWriter())
	require.NoError(t, (*DatasourceRouter)(nil).Close())
	require.NoError(t, (&DatasourceRouter{nodes: []*datasourceNode{{name: "nil"}}}).Close())
}

func TestDatasourceRouterNamedHealthAndStats(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"writer-broken": {Type: "oracle", DSN: "unused", Writable: true},
		"writer-healthy": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "writer.sqlite"),
			Writable: true, MaxOpenConns: 7,
		},
		"reader": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "reader.sqlite"),
			Readable: true, MaxOpenConns: 3,
		},
	})
	t.Cleanup(func() { _ = router.Close() })
	require.ErrorContains(t, err, `database "writer-broken"`)

	assert.Equal(t, []string{"reader", "writer-broken", "writer-healthy"}, router.DatasourceNames())
	assert.True(t, router.HasWriter())
	snapshot := router.Probe(t.Context())
	assert.False(t, snapshot.CheckedAt.IsZero())
	assert.NoError(t, snapshot.RoutingError(), "a startup-failed node is not part of the routing set")
	assert.Error(t, snapshot.DatasourceError("writer-broken"))
	assert.ErrorContains(t, snapshot.DatasourceError("missing"), "is not configured")

	stats := router.DatasourceStats()
	require.Len(t, stats, 3)
	assert.Equal(t, "reader", stats[0].Name)
	assert.True(t, stats[0].Up)
	assert.Equal(t, 3, stats[0].Pool.MaxOpenConnections)
	assert.Equal(t, "writer-broken", stats[1].Name)
	assert.False(t, stats[1].Up)
	assert.Equal(t, "writer-healthy", stats[2].Name)
	assert.True(t, stats[2].Up)
	assert.Equal(t, 7, stats[2].Pool.MaxOpenConnections)
}

func TestDatasourceRouterProbeFailsWhenRoutedNodeBecomesUnhealthy(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"writer-a": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "a.sqlite"), Writable: true},
		"writer-b": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "b.sqlite"), Writable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })

	pool, err := router.nodes[0].db.DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	snapshot := router.Probe(t.Context())
	assert.ErrorContains(t, snapshot.RoutingError(), `routed datasource "writer-a" (writer) is unhealthy`)
	assert.Error(t, snapshot.DatasourceError("writer-a"))
	assert.NoError(t, snapshot.DatasourceError("writer-b"))
}

func TestDatasourceRouterRoundRobinStateIsPerRouter(t *testing.T) {
	newRouter := func(prefix string) *DatasourceRouter {
		router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
			"a": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), prefix+"-a.sqlite"), Writable: true},
			"b": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), prefix+"-b.sqlite"), Writable: true},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = router.Close() })
		return router
	}

	first := newRouter("first")
	second := newRouter("second")
	firstA, firstB := first.Write(), first.Write()
	assert.NotSame(t, firstA, firstB)
	assert.Same(t, firstA, first.Write())
	secondA, secondB := second.Write(), second.Write()
	assert.NotSame(t, secondA, secondB)
	assert.Same(t, secondA, second.Write())
}
