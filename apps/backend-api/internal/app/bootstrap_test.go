package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/health"
	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedHealthModule struct {
	name string
	err  error
}

type dependentAppModule struct {
	name       string
	dependency string
}

func (m *dependentAppModule) ModuleName() string { return m.name }
func (m *dependentAppModule) ModuleDependencies() []string {
	return []string{m.dependency}
}

type failingLifecycleAppModule struct {
	name     string
	initErr  error
	startErr error
}

func (m *failingLifecycleAppModule) ModuleName() string               { return m.name }
func (m *failingLifecycleAppModule) Initialize(context.Context) error { return m.initErr }
func (m *failingLifecycleAppModule) Start(context.Context) error      { return m.startErr }

type appCollectorModule struct{ name string }

func (m *appCollectorModule) ModuleName() string { return m.name }
func (*appCollectorModule) Collector() prometheus.Collector {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: "app_lifecycle_test"})
}

type failingCollectorSink struct {
	name string
	err  error
}

func (m *failingCollectorSink) ModuleName() string                           { return m.name }
func (m *failingCollectorSink) RegisterCollector(prometheus.Collector) error { return m.err }

func (m *namedHealthModule) ModuleName() string                { return m.name }
func (m *namedHealthModule) CheckHealth(context.Context) error { return m.err }

func TestInitializeRuntimeRoutesPrimaryAndReplica(t *testing.T) {
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
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.runtime.Close() })

	assert.True(t, app.runtime.Database().HasWriter())
	assert.NotSame(t, app.runtime.Database().Write(), app.runtime.Database().Read())
}

func TestLifecycleReportsActivationCollectionInitializationAndStartFailures(t *testing.T) {
	t.Run("activate", func(t *testing.T) {
		app := &App{modules: module.NewRegistry()}
		require.NoError(t, app.modules.Register(&dependentAppModule{name: "dependent", dependency: "missing"}))
		assert.ErrorContains(t, app.prepareModules(), "requires unregistered module")
	})

	t.Run("collect metrics", func(t *testing.T) {
		want := errors.New("collector registration failed")
		app := &App{modules: module.NewRegistry()}
		require.NoError(t, app.modules.Register(
			&appCollectorModule{name: "source"},
			&failingCollectorSink{name: "sink", err: want},
		))
		assert.ErrorIs(t, app.prepareModules(), want)
	})

	t.Run("initialize", func(t *testing.T) {
		want := errors.New("initialize failed")
		app := &App{modules: module.NewRegistry()}
		require.NoError(t, app.modules.Register(&failingLifecycleAppModule{name: "module", initErr: want}))
		require.NoError(t, app.prepareModules())
		assert.ErrorIs(t, app.lifecycle(t.Context()), want)
	})

	t.Run("start", func(t *testing.T) {
		want := errors.New("start failed")
		app := &App{modules: module.NewRegistry()}
		require.NoError(t, app.modules.Register(&failingLifecycleAppModule{name: "module", startErr: want}))
		require.NoError(t, app.prepareModules())
		assert.ErrorIs(t, app.lifecycle(t.Context()), want)
	})
}

func TestInitializeRuntimeFailsWhenEveryConfiguredWriterFails(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary-a": {Type: "oracle", Dsn: "unused", Writable: true},
		"primary-b": {Type: "oracle", Dsn: "unused", Writable: true},
	}}}

	err := app.initializeRuntime(t.Context())
	assert.ErrorContains(t, err, "no configured writer is available")
	assert.ErrorContains(t, err, `database "primary-a"`)
	assert.ErrorContains(t, err, `database "primary-b"`)
	assert.Nil(t, app.runtime)
}

func TestInitializeRuntimeRejectsDatasourceWithoutRole(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"disabled": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "disabled.sqlite")},
	}}}

	err := app.initializeRuntime(t.Context())
	assert.ErrorContains(t, err, `datasource "disabled" must be readable or writable`)
	assert.Nil(t, app.runtime)
}

func TestBootstrapPropagatesDatabaseConfigurationFailure(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"disabled": {Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "disabled.sqlite")},
	}}}
	assert.ErrorContains(t, app.bootstrap(t.Context()), "must be readable or writable")
}

func TestInitializeRuntimeKeepsHealthyWriterWhenAnotherFails(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{
		"primary-broken": {Type: "oracle", Dsn: "unused", Writable: true},
		"primary-healthy": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "healthy.sqlite"), Writable: true,
		},
	}}}

	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.runtime.Close() })
	assert.True(t, app.runtime.Database().HasWriter())
	assert.NotNil(t, app.runtime.Database().Write())
	snapshot := app.runtime.Database().Probe(t.Context())
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
	assert.False(t, app.runtime.Database().Write().Migrator().HasTable("dlocks"))
	assert.False(t, app.runtime.Database().Write().Migrator().HasTable("worker_ids"))
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
	migrationConfig := migrationTestConfig(database)
	migrationConfig.Action = MigrationUp
	_, err := (&App{}).RunMigrations(t.Context(), migrationConfig)
	require.NoError(t, err)

	app := &App{config: AppConfig{Timezone: "UTC", Database: database}}
	require.NoError(t, app.bootstrap(t.Context()))
	t.Cleanup(func() { _ = app.shutdown() })

	require.NotNil(t, app.runtime.Database().Write())
	assert.True(t, app.runtime.Database().Write().Migrator().HasTable("dlocks"))
	assert.True(t, app.runtime.Database().Write().Migrator().HasTable("worker_ids"))
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

	require.NotNil(t, app.runtime.Database().Write())
	assert.True(t, app.runtime.Database().Write().Migrator().HasTable("dlocks"))
	assert.True(t, app.runtime.Database().Write().Migrator().HasTable("worker_ids"))
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
	_, todoEnabled := app.modules.ByName("todo")
	assert.False(t, todoEnabled, "tasks must not be exposed without IAM")
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

func TestIAMCompositionUsesFrozenRuntimeInventory(t *testing.T) {
	app := &App{config: AppConfig{
		Authn:      IAMAuthn{Enabled: true},
		Federation: IAMFederation{Enabled: true},
		Database: map[string]Database{"primary": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-composition.sqlite"), Writable: true, Readable: true,
		}},
	}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.runtime.Close() })
	require.NoError(t, app.composeModules())
	require.NoError(t, app.modules.Activate())
	require.NoError(t, app.modules.Wire(app.provideCrossCutting))

	names := make([]string, 0, len(app.modules.Modules()))
	for _, current := range app.modules.Modules() {
		names = append(names, current.ModuleName())
	}
	assert.Subset(t, names, []string{
		iamaudit.Name, core.Name, iamauthn.Name, identity.Name, organization.Name,
		"oauth", "provisioning", governance.Name, federation.Name,
	})
	assertIAMOrder(t, names)
}

func TestIAMCompositionDelegatesActivationToModules(t *testing.T) {
	app := &App{config: AppConfig{Database: map[string]Database{"primary": {
		Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-disabled.sqlite"), Writable: true, Readable: true,
	}}}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.runtime.Close() })
	require.NoError(t, app.composeModules())

	for _, name := range []string{
		iamaudit.Name, core.Name, iamauthn.Name, identity.Name, organization.Name,
		"oauth", "provisioning", governance.Name, federation.Name,
	} {
		_, registered := app.modules.ByName(name)
		assert.True(t, registered, "%s must be registered before activation", name)
	}

	require.NoError(t, app.modules.Activate())
	for _, name := range []string{
		iamaudit.Name, core.Name, iamauthn.Name, identity.Name, organization.Name,
		"oauth", "provisioning", governance.Name, federation.Name,
	} {
		_, active := app.modules.ByName(name)
		assert.False(t, active, "%s must own its disabled activation state", name)
	}
}

func TestIAMCompositionRejectsEnabledDependentWithDisabledDependencies(t *testing.T) {
	app := &App{config: AppConfig{
		Federation: IAMFederation{Enabled: true},
		Database: map[string]Database{"primary": {
			Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "iam-invalid-activation.sqlite"), Writable: true,
		}},
	}}
	require.NoError(t, app.initializeRuntime(t.Context()))
	t.Cleanup(func() { _ = app.runtime.Close() })
	require.NoError(t, app.composeModules())

	err := app.modules.Activate()
	assert.ErrorContains(t, err, `module "federation" requires unregistered module "iam-audit"`)
}

func TestIAMCompositionReportsEveryMissingBusinessProvider(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{iamaudit.Name, "resolve IAM audit appender"},
		{core.Name, "resolve audit authorization registrar"},
		{iamauthn.Name, "resolve authentication verifier"},
		{identity.Name, "resolve authn registration principal creator"},
		{organization.Name, "resolve provisioning group provisioner"},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			app := &App{config: AppConfig{
				Authn: IAMAuthn{Enabled: true}, Federation: IAMFederation{Enabled: true},
				Database: map[string]Database{"primary": {
					Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "missing.sqlite"), Writable: true,
				}},
			}}
			require.NoError(t, app.initializeRuntime(t.Context()))
			t.Cleanup(func() { _ = app.runtime.Close() })
			require.NoError(t, app.composeModules())

			modules := app.modules.Modules()
			for index, current := range modules {
				if current.ModuleName() == test.provider {
					modules[index] = &namedHealthModule{name: test.provider}
				}
			}
			app.modules = module.NewRegistry()
			require.NoError(t, app.modules.Register(modules...))
			require.NoError(t, app.modules.Activate())
			err := app.modules.Wire(app.provideCrossCutting)
			assert.ErrorContains(t, err, test.want)
			assert.ErrorContains(t, err, "service is not registered")
		})
	}
}

func assertIAMOrder(t *testing.T, names []string) {
	t.Helper()
	want := []string{iamaudit.Name, core.Name, iamauthn.Name, identity.Name, organization.Name, "oauth", "provisioning", governance.Name, federation.Name}
	positions := make(map[string]int, len(names))
	for index, name := range names {
		positions[name] = index
	}
	for index := 1; index < len(want); index++ {
		assert.Less(t, positions[want[index-1]], positions[want[index]], want[index])
	}
}

func TestConfigureProcessRuntime(t *testing.T) {
	require.NoError(t, configureProcessRuntime("UTC", false, "test-api"))
	assert.ErrorContains(t, configureProcessRuntime("Invalid/Timezone", false, "test-api"), "initialize timezone")
	require.NoError(t, configureProcessRuntime("UTC", true, "test-api"))
}
