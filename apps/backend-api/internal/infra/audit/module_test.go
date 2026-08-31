package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestModule builds a module whose events are captured by a memory sink and
// whose dispatcher is running. Callers must Stop the module to drain.
func newTestModule(t *testing.T, config Config) (*Module, *memorySink) {
	t.Helper()
	sink := &memorySink{}
	module := NewModule(appenv.NewRuntime(appenv.NewIdentity("test-api", "1.0.0")), config, WithSinks(sink))
	module.Start(t.Context())
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = module.Stop(stopCtx)
	})
	return module, sink
}

func TestModuleLifecycleAndRecord(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1.0.0")), httpModuleConfig(), WithSinks(sink))
	assert.Equal(t, "audit", auditModule.ModuleName())
	require.NoError(t, auditModule.Start(t.Context()))
	auditModule.Record(t.Context(), Event{Operation: "manual", Access: AccessWrite})
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, "svc", sink.snapshot()[0].Service)
	require.NoError(t, auditModule.Stop(t.Context()))
	// Stopping again is a no-op.
	require.NoError(t, auditModule.Stop(t.Context()))
}

func TestModuleProvideServices(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1.0.0")), httpModuleConfig(), WithSinks(sink))
	services := module.NewServices()
	require.NoError(t, auditModule.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolved, err := module.Resolve[Recorder](services)
	require.NoError(t, err)
	require.NotNil(t, resolved)
}

type fixedGUIDSource struct{ id int64 }

func (source fixedGUIDSource) Next() (int64, error) { return source.id, nil }

func TestModuleInjectsApplicationGUIDSource(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "")), httpModuleConfig(), WithSinks(sink))
	assert.Equal(t, []string{guid.Name}, auditModule.ModuleDependencies())
	services := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, fixedGUIDSource{id: 4242}))
	require.NoError(t, services.Seal())
	require.NoError(t, auditModule.InjectServices(services))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(context.Background())) }()
	auditModule.Record(t.Context(), Event{Operation: "injected"})
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, "4242", sink.snapshot()[0].ID)
}

func TestModuleOptionOverridesIDGenerator(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("", "")), httpModuleConfig(), WithSinks(sink), WithIDGenerator(func() (int64, error) {
		return 5150, nil
	}))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(context.Background())) }()
	auditModule.Record(t.Context(), Event{Operation: "option"})
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, "5150", sink.snapshot()[0].ID)
}

func TestDisabledModuleProvidesNoopRecorderWithoutDispatcher(t *testing.T) {
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1.0.0")), Config{Enabled: false}, WithSinks(&memorySink{}))
	services := module.NewServices()
	require.NoError(t, auditModule.ProvideServices(services))
	require.NoError(t, services.Seal())
	recorder, err := module.Resolve[Recorder](services)
	require.NoError(t, err)
	require.NoError(t, auditModule.Start(t.Context()))
	recorder.Record(t.Context(), Event{Operation: "disabled"})
	assert.Zero(t, auditModule.recorder.Stats().Queued)
	assert.Zero(t, auditModule.recorder.Stats().Dropped)
	assert.Zero(t, auditModule.recorder.Stats().Success)
	require.NoError(t, auditModule.Stop(t.Context()))
}

func TestModuleRunConvenience(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1.0.0")), httpModuleConfig(), WithSinks(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(t.Context())) }()

	err := auditModule.Run(t.Context(), Spec{Entry: KindSchedule, Operation: "cron.x"}, func(context.Context) error {
		return nil
	})
	require.NoError(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, KindSchedule, sink.snapshot()[0].Entry)
}

func TestModuleCollectorExportsCounters(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1.0.0")), httpModuleConfig(), WithSinks(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(t.Context())) }()

	auditModule.Record(t.Context(), Event{Operation: "op", Access: AccessWrite})
	eventually(t, func() bool { return sink.count() == 1 })

	collector := auditModule.Collector()

	// Describe must announce exactly the five pipeline metric families. The
	// registry is what closes the channel, so collect a fixed count directly.
	descChannel := make(chan *prometheus.Desc)
	go collector.Describe(descChannel)
	var described []string
	for range 5 {
		select {
		case desc := <-descChannel:
			described = append(described, desc.String())
		case <-time.After(time.Second):
			t.Fatal("Describe did not emit all descriptors")
		}
	}
	require.Len(t, described, 5)

	// Collect must emit one metric per family.
	metricChannel := make(chan prometheus.Metric)
	go collector.Collect(metricChannel)
	emitted := 0
	for range 5 {
		select {
		case <-metricChannel:
			emitted++
		case <-time.After(time.Second):
			t.Fatal("Collect did not emit all metrics")
		}
	}
	assert.Equal(t, 5, emitted)
	assert.GreaterOrEqual(t, auditModule.recorder.Stats().Success, uint64(1))
}

func TestModuleNormalizesConfiguration(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1")), Config{
		Enabled:      true,
		MaxBodyBytes: -1,
		QueueSize:    0,
		BatchSize:    0,
		Overflow:     "weird",
	}, WithSinks(sink))
	assert.Equal(t, 8192, auditModule.cfg.MaxBodyBytes)
	assert.Equal(t, 4096, auditModule.cfg.QueueSize)
	assert.Equal(t, 200, auditModule.cfg.BatchSize)
	assert.Equal(t, overflowDrop, auditModule.cfg.Overflow)
	// Sensitive fields normalize to empty (no extra entries).
	assert.Empty(t, auditModule.cfg.SensitiveFields)

	// A disabled config still builds a module.
	disabled := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1")), Config{Enabled: false}, WithSinks(sink))
	assert.False(t, disabled.cfg.Enabled)
}

func TestModuleGormSinkEndToEnd(t *testing.T) {
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "audit.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	require.NoError(t, Migrate(t.Context(), router.Write()))

	config := httpModuleConfig()
	config.Database = DatabaseConfig{Enabled: true}
	// WithSink appends a memory sink while keeping the configured gorm sink, so
	// the event must land in both.
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1"), appenv.WithDatabase(router)), config, WithSink(sink))
	require.NoError(t, auditModule.Initialize(t.Context()))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(t.Context())) }()

	auditModule.Record(t.Context(), Event{ID: "777", Operation: "op", Access: AccessWrite})
	eventually(t, func() bool { return sink.count() == 1 })
	eventually(t, func() bool {
		var count int64
		if err := router.Write().Model(&OperationLog{}).Where("id = ?", int64(777)).Count(&count).Error; err != nil {
			return false
		}
		return count == 1
	})
}

func TestModuleDatabaseSinkFailsInitializationWithoutWriter(t *testing.T) {
	config := httpModuleConfig()
	config.Database = DatabaseConfig{Enabled: true}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1")), config, WithSinks(&memorySink{}))
	require.ErrorIs(t, auditModule.Initialize(t.Context()), ErrDatabaseSinkUnavailable)
}

func TestModuleDatabaseSinkFailsInitializationWithoutMigration(t *testing.T) {
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "unmigrated.sqlite"), Writable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	config := httpModuleConfig()
	config.Database.Enabled = true
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("", ""), appenv.WithDatabase(router)), config)
	err = auditModule.Initialize(t.Context())
	require.ErrorIs(t, err, ErrDatabaseSinkUnavailable)
	assert.ErrorContains(t, err, operationLogsTable)
}

func TestMaxBodyBytesNegativeMeansUnlimitedCapture(t *testing.T) {
	module := &Module{cfg: Config{MaxBodyBytes: -5}, redactor: newRedactor(nil)}
	assert.Equal(t, 0, module.maxBodyBytes())
	body, truncated, err := module.redactAndTruncate([]byte(`{"a":"b"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":"b"}`, body)
	assert.False(t, truncated)
}
