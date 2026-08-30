package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	audit "7link.com/platform/internal/infra/audit"
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
	require.NoError(t, app.initializeDatabase(t.Context()))
	t.Cleanup(func() { _ = app.Db.Close() })

	assert.True(t, app.Db.HasWriter())
	assert.NotSame(t, app.Db.Write(), app.Db.Read())
}

func TestInitializeDatabaseFailsWhenEveryConfiguredWriterFails(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary-a": {Type: "oracle", Dsn: "unused", Writable: true},
		"primary-b": {Type: "oracle", Dsn: "unused", Writable: true},
	}}}

	err := app.initializeDatabase(t.Context())
	assert.ErrorContains(t, err, "no configured writer is available")
	assert.ErrorContains(t, err, `database "primary-a"`)
	assert.ErrorContains(t, err, `database "primary-b"`)
	assert.False(t, app.Db.HasWriter())
}

func TestInitializeDatabaseRejectsDatasourceWithoutRole(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"disabled": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "disabled.sqlite")},
	}}}

	err := app.initializeDatabase(t.Context())
	assert.ErrorContains(t, err, `datasource "disabled" must be readable or writable`)
	assert.Nil(t, app.Db)
}

func TestInitializeDatabaseKeepsHealthyWriterWhenAnotherFails(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary-broken": {Type: "oracle", Dsn: "unused", Writable: true},
		"primary-healthy": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "healthy.sqlite"), Writable: true,
		},
	}}}

	require.NoError(t, app.initializeDatabase(t.Context()))
	t.Cleanup(func() { _ = app.Db.Close() })
	assert.True(t, app.Db.HasWriter())
	assert.NotNil(t, app.Db.Write())
	snapshot := app.Db.Probe(t.Context())
	assert.NoError(t, snapshot.RoutingError())
	assert.Error(t, snapshot.DatasourceError("primary-broken"))
	assert.NoError(t, app.checkHealth(t.Context()))
}

func TestCheckHealthJoinsModuleErrors(t *testing.T) {
	first := errors.New("first unavailable")
	second := errors.New("second unavailable")
	app := &App{modules: registeredRegistry(
		&namedHealthModule{name: "first", err: first},
		&namedHealthModule{name: "second", err: second},
	)}

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
	app := &App{config: AppConfig{Audit: Audit{Enabled: true}}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	assert.ErrorIs(t, app.checkHealth(t.Context()), dlock.ErrDatabaseUnavailable)

	healthModule, ok := app.modules.ByName(health.Name)
	require.True(t, ok)
	module := healthModule.(*health.HealthModule)
	assert.Equal(t, healthservice.CheckData{
		Status: healthservice.StatusNotServing,
		Modules: []healthservice.ModuleStatus{
			{Name: "i18n", Status: healthservice.StatusUnknown},
			{Name: "respx", Status: healthservice.StatusUnknown},
			{Name: "dlock", Status: healthservice.StatusNotServing},
			{Name: "wuid", Status: healthservice.StatusNotServing},
			{Name: "guid", Status: healthservice.StatusNotServing},
			{Name: "telemetry", Status: healthservice.StatusUnknown},
			{Name: "audit", Status: healthservice.StatusUnknown},
			{Name: "health", Status: healthservice.StatusServing},
		},
	}, module.Service().Check(t.Context()))
}

func TestBootstrapAddsEnabledHTTPModules(t *testing.T) {
	app := &App{config: AppConfig{
		Pprof: Pprof{Enabled: true},
		Audit: Audit{Enabled: true},
		API: API{
			Metrics:             APIMetrics{Enabled: true},
			MigrationStatus:     APIMigrationStatus{Enabled: true},
			ConfigInspector:     APIConfigInspector{Enabled: true},
			DatasourceInspector: APIDatasourceInspector{Enabled: true},
		},
	}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	names := make([]string, 0)
	for _, appModule := range app.modules.Modules() {
		names = append(names, appModule.ModuleName())
	}
	assert.Equal(t, []string{
		"i18n", "respx", "dlock", "wuid", "guid", "metrics", "telemetry", "audit",
		"database-monitor", "migration-status", "config-inspector", "pprof", "health",
	}, names)
	_, ok := app.modules.ByName("metrics")
	assert.True(t, ok)
}

func TestBootstrapRegistersAuditRecorderWhenDisabled(t *testing.T) {
	app := &App{config: AppConfig{Audit: Audit{Enabled: false}}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	recorder, err := module.Resolve[audit.Recorder](app.modules.Services())
	require.NoError(t, err)
	require.NotNil(t, recorder)
	recorder.Record(t.Context(), audit.Event{Operation: "disabled"})

	auditModule, ok := app.modules.ByName(audit.Name)
	require.True(t, ok)
	assert.NotNil(t, auditModule.(*audit.Module).Collector())
}

func TestConfigureProcessRuntime(t *testing.T) {
	require.NoError(t, configureProcessRuntime("UTC", false, "test-api"))
	assert.ErrorContains(t, configureProcessRuntime("Invalid/Timezone", false, "test-api"), "initialize timezone")
	require.NoError(t, configureProcessRuntime("UTC", true, "test-api"))
}
