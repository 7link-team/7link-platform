package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/health"
	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedHealthModule struct {
	name string
	err  error
}

func (m *namedHealthModule) ModuleName() string                { return m.name }
func (m *namedHealthModule) CheckHealth(context.Context) error { return m.err }

type wiringService interface{ Ready() bool }

type wiringProvider struct{}

func (*wiringProvider) ModuleName() string { return "provider" }
func (*wiringProvider) Ready() bool        { return true }

func (provider *wiringProvider) ProvideServices(services *module.Services) error {
	return module.Provide[wiringService](services, provider)
}

type wiringConsumer struct{ service wiringService }

func (*wiringConsumer) ModuleName() string { return "consumer" }
func (consumer *wiringConsumer) InjectServices(services *module.Services) error {
	service, err := module.Resolve[wiringService](services)
	consumer.service = service
	return err
}

func TestWireModulesCollectsProvidersBeforeInjection(t *testing.T) {
	consumer := &wiringConsumer{}
	app := &App{mods: []module.Module{consumer, &wiringProvider{}}}
	require.NoError(t, app.wireModules())
	require.NotNil(t, consumer.service)
	assert.True(t, consumer.service.Ready())
}

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

func TestCheckHealthJoinsModuleErrors(t *testing.T) {
	first := errors.New("first unavailable")
	second := errors.New("second unavailable")
	app := &App{mods: []module.Module{
		&namedHealthModule{name: "first", err: first},
		&namedHealthModule{name: "second", err: second},
	}}

	err := app.checkHealth(t.Context())
	assert.ErrorIs(t, err, first)
	assert.ErrorIs(t, err, second)
}

func TestBootstrapRequiresExplicitMigrations(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "bootstrap.sqlite")
	app := &App{config: AppConfig{Timezone: "UTC", Database: map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      databasePath,
			Writable: true,
		},
	}}}
	assert.ErrorContains(t, app.bootstrap(t.Context()), "no such table")
	t.Cleanup(func() { _ = app.shutdown() })
	assert.False(t, app.Db.Write().Migrator().HasTable("dlocks"))
	assert.False(t, app.Db.Write().Migrator().HasTable("worker_ids"))
}

func TestBootstrapStartsAfterExplicitMigrations(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "bootstrap.sqlite")
	database := map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      databasePath,
			Writable: true,
		},
	}
	_, err := RunMigrations(t.Context(), MigrationConfig{
		Action:   MigrationUp,
		Timezone: "UTC",
		Database: database,
	})
	require.NoError(t, err)

	app := &App{config: AppConfig{Timezone: "UTC", Database: database}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	require.NotNil(t, app.Db.Write())
	assert.True(t, app.Db.Write().Migrator().HasTable("dlocks"))
	assert.True(t, app.Db.Write().Migrator().HasTable("worker_ids"))
	assert.NoError(t, app.checkHealth(t.Context()))
}

func TestBootstrapMigratesBeforeStartingWhenEnabled(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "bootstrap-auto.sqlite")
	app := &App{config: AppConfig{
		Migrate:  true,
		Timezone: "UTC",
		Database: map[string]Database{
			"primary": {
				Type:     "sqlite",
				Dsn:      databasePath,
				Writable: true,
			},
		},
	}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	require.NotNil(t, app.Db.Write())
	assert.True(t, app.Db.Write().Migrator().HasTable("dlocks"))
	assert.True(t, app.Db.Write().Migrator().HasTable("worker_ids"))
	assert.NoError(t, app.checkHealth(t.Context()))
}

func TestBootstrapWithoutWriterReportsUnhealthy(t *testing.T) {
	app := &App{}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	assert.ErrorIs(t, app.checkHealth(t.Context()), dlock.ErrDatabaseUnavailable)

	var healthModule *health.HealthModule
	for _, appModule := range app.mods {
		if candidate, ok := appModule.(*health.HealthModule); ok {
			healthModule = candidate
			break
		}
	}
	require.NotNil(t, healthModule)
	assert.Equal(t, healthservice.CheckData{
		Status: healthservice.StatusNotServing,
		Modules: []healthservice.ModuleStatus{
			{Name: "i18n", Status: healthservice.StatusUnknown},
			{Name: "respx", Status: healthservice.StatusUnknown},
			{Name: "dlock", Status: healthservice.StatusNotServing},
			{Name: "wuid", Status: healthservice.StatusNotServing},
			{Name: "guid", Status: healthservice.StatusNotServing},
			{Name: "health", Status: healthservice.StatusServing},
		},
	}, healthModule.Service().Check(t.Context()))
}

func TestBootstrapAddsEnabledHTTPModules(t *testing.T) {
	app := &App{config: AppConfig{
		Pprof: Pprof{Enabled: true},
		API: API{
			Metrics:         APIMetrics{Enabled: true},
			MigrationStatus: APIMigrationStatus{Enabled: true},
			ConfigInspector: APIConfigInspector{Enabled: true},
		},
	}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	names := make([]string, len(app.mods))
	for index, appModule := range app.mods {
		names[index] = appModule.ModuleName()
	}
	assert.Equal(t, []string{
		"i18n", "respx", "dlock", "wuid", "guid", "metrics", "pprof",
		"migration-status", "config-inspector", "health",
	}, names)
	assert.NotNil(t, app.metrics)
}

func TestConfigureProcessRuntime(t *testing.T) {
	require.NoError(t, configureProcessRuntime("UTC"))
	assert.ErrorContains(t, configureProcessRuntime("Invalid/Timezone"), "initialize timezone")
	require.NoError(t, configureProcessRuntime("UTC"))
}
