package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type healthModule struct{ err error }

func (m *healthModule) CheckHealth(context.Context) error { return m.err }

func TestInitializeDatabaseRoutesPrimaryAndReplica(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "primary.sqlite"),
			Writable: true,
		},
		"replica": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "replica.sqlite"),
			Readable: true,
		},
	}}}
	app.initializeDatabase(t.Context())
	t.Cleanup(func() { _ = app.Db.Close() })

	require.Len(t, app.Db.Writer, 1)
	require.Len(t, app.Db.Reader, 1)
	assert.NotSame(t, app.Db.Writer[0], app.Db.Reader[0])
}

func TestInitializeDatabaseDefaultsUnspecifiedRolesToReadWrite(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary": {
			Type: "sqlite",
			Dsn:  filepath.Join(t.TempDir(), "legacy.sqlite"),
		},
	}}}
	app.initializeDatabase(t.Context())
	t.Cleanup(func() { _ = app.Db.Close() })

	require.Len(t, app.Db.Writer, 1)
	require.Len(t, app.Db.Reader, 1)
	assert.Same(t, app.Db.Writer[0], app.Db.Reader[0])
}

func TestCheckHealthJoinsModuleErrors(t *testing.T) {
	first := errors.New("first unavailable")
	second := errors.New("second unavailable")
	app := &App{mods: []any{
		&healthModule{err: first},
		struct{}{},
		&healthModule{err: second},
	}}

	err := app.checkHealth(t.Context())
	assert.ErrorIs(t, err, first)
	assert.ErrorIs(t, err, second)
}

func TestBootstrapMigratesDatabaseAndBecomesHealthy(t *testing.T) {
	require.NoError(t, i18n.InitEmbedded(i18n.Base))
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "bootstrap.sqlite"),
			Writable: true,
		},
	}}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	require.NotNil(t, app.Db.Write())
	assert.True(t, app.Db.Write().Migrator().HasTable("dlocks"))
	assert.NoError(t, app.checkHealth(t.Context()))
}

func TestBootstrapWithoutWriterReportsUnhealthy(t *testing.T) {
	require.NoError(t, i18n.InitEmbedded(i18n.Base))
	app := &App{}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	assert.ErrorIs(t, app.checkHealth(t.Context()), dlock.ErrDatabaseUnavailable)
}
