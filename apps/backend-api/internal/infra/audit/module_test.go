package audit

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestModule builds a module whose events are captured by a memory sink and
// whose dispatcher is running. Callers must Stop the module to drain.
func newTestModule(t *testing.T, config Config) (*Module, *memorySink) {
	t.Helper()
	sink := &memorySink{}
	module := NewModule("test-api", "1.0.0", nil, config, WithSinks(sink))
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
	auditModule := NewModule("svc", "1.0.0", nil, httpModuleConfig(), WithSinks(sink))
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
	auditModule := NewModule("svc", "1.0.0", nil, httpModuleConfig(), WithSinks(sink))
	services := module.NewServices()
	require.NoError(t, auditModule.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolved, err := module.Resolve[Recorder](services)
	require.NoError(t, err)
	require.NotNil(t, resolved)
}

func TestDisabledModuleProvidesNoopRecorderWithoutDispatcher(t *testing.T) {
	auditModule := NewModule("svc", "1.0.0", nil, Config{Enabled: false}, WithSinks(&memorySink{}))
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
	auditModule := NewModule("svc", "1.0.0", nil, httpModuleConfig(), WithSinks(sink))
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
	auditModule := NewModule("svc", "1.0.0", nil, httpModuleConfig(), WithSinks(sink))
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
	auditModule := NewModule("svc", "1", nil, Config{
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
	disabled := NewModule("svc", "1", nil, Config{Enabled: false}, WithSinks(sink))
	assert.False(t, disabled.cfg.Enabled)
}

func TestModuleGormSinkEndToEnd(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, Migrate(t.Context(), db))
	writer := writerFunc(func() *gorm.DB { return db })

	config := httpModuleConfig()
	config.Database = DatabaseConfig{Enabled: true}
	// WithSink appends a memory sink while keeping the configured gorm sink, so
	// the event must land in both.
	sink := &memorySink{}
	auditModule := NewModule("svc", "1", writer, config, WithSink(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(t.Context())) }()

	auditModule.Record(t.Context(), Event{ID: "777", Operation: "op", Access: AccessWrite})
	eventually(t, func() bool { return sink.count() == 1 })
	eventually(t, func() bool {
		var count int64
		if err := db.Model(&OperationLog{}).Where("id = ?", int64(777)).Count(&count).Error; err != nil {
			return false
		}
		return count == 1
	})
}

func TestModuleGormSinkSkippedWhenNoWriter(t *testing.T) {
	// A nil writer with the database sink enabled must not panic; the slog sink
	// remains and events still flush.
	sink := &memorySink{}
	config := httpModuleConfig()
	config.Database = DatabaseConfig{Enabled: true}
	auditModule := NewModule("svc", "1", nil, config, WithSinks(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	defer func() { require.NoError(t, auditModule.Stop(t.Context())) }()
	auditModule.Record(t.Context(), Event{Operation: "op"})
	eventually(t, func() bool { return sink.count() == 1 })
}

func TestMaxBodyBytesNegativeMeansUnlimitedCapture(t *testing.T) {
	module := &Module{cfg: Config{MaxBodyBytes: -5}, redactor: newRedactor(nil)}
	assert.Equal(t, 0, module.maxBodyBytes())
	body, truncated, err := module.redactAndTruncate([]byte(`{"a":"b"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":"b"}`, body)
	assert.False(t, truncated)
}

type writerFunc func() *gorm.DB

func (f writerFunc) Write() *gorm.DB { return f() }
