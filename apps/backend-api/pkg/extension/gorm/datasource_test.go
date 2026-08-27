package gormx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Len(t, router.Writer, 1)
	require.Len(t, router.Reader, 1)
	assert.Same(t, router.Writer[0], router.Write())
	assert.Same(t, router.Reader[0], router.Read())
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
