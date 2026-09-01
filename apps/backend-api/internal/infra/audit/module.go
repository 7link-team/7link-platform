// Package audit provides a transport-independent operation log (see audit.go).
package audit

//go:generate go run ./dbgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"7link.com/platform/internal/infra/audit/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"github.com/prometheus/client_golang/prometheus"
)

const Name = "audit"

var ErrDatabaseSinkUnavailable = errors.New("audit: configured database sink is unavailable")

// Module owns the audit pipeline and exposes it to the application service
// registry. The module is always registered so dependent modules can resolve
// the Recorder contract; Config.Enabled controls whether automatic capture and
// persistence are active.
type Module struct {
	runtime    *appenv.Runtime
	cfg        Config
	recorder   *AsyncRecorder
	redactor   *redactor
	skipRoutes []string
	idGen      func() (int64, error)

	sinks []Sink
}

// ModuleOption configures the module at construction time.
type ModuleOption func(*Module)

// WithSinks replaces the default sink set (the always-on slog sink, plus the
// gorm sink when configured). It is primarily used by tests to capture events
// without writing to the process log.
func WithSinks(sinks ...Sink) ModuleOption {
	return func(module *Module) {
		module.sinks = append([]Sink(nil), sinks...)
	}
}

// WithSink appends an extra sink to the pipeline while keeping the default
// sinks, so a test sink can observe events without disabling persistence.
func WithSink(sink Sink) ModuleOption {
	return func(module *Module) {
		if sink != nil {
			module.sinks = append(module.sinks, sink)
		}
	}
}

// WithIDGenerator overrides the snowflake ID generator used for event IDs. The
// application normally injects the wuid-managed guid.Source during service
// wiring; this option is for standalone construction and tests.
func WithIDGenerator(fn func() (int64, error)) ModuleOption {
	return func(module *Module) {
		if fn != nil {
			module.idGen = fn
		}
	}
}

var (
	_ module.Module          = (*Module)(nil)
	_ module.Depender        = (*Module)(nil)
	_ module.ServiceProvider = (*Module)(nil)
	_ module.ServiceInjector = (*Module)(nil)
	_ module.Initializer     = (*Module)(nil)
	_ module.Starter         = (*Module)(nil)
	_ module.Stopper         = (*Module)(nil)
)

// NewModule builds the audit module. The service identity and writable
// datasource are taken from the shared runtime; the gorm sink is installed only
// when Config.Database.Enabled and a writer exist. The slog sink is always
// installed.
func NewModule(rt *appenv.Runtime, cfg Config, options ...ModuleOption) *Module {
	cfg = normalizeConfig(cfg)
	module := &Module{
		runtime:    rt,
		cfg:        cfg,
		redactor:   newRedactor(cfg.SensitiveFields),
		skipRoutes: append([]string(nil), cfg.SkipRoutes...),
	}

	module.sinks = append(module.sinks, NewSlogSink())
	if cfg.Database.Enabled && rt.Database() != nil {
		module.sinks = append(module.sinks, newRoutedGormSink(rt.Database(), rt.TablePrefix()))
	}
	for _, option := range options {
		if option != nil {
			option(module)
		}
	}

	instance := instanceID()
	identity := rt.Identity()
	module.recorder = NewAsyncRecorder(identity.Name(), identity.Version(), instance, RecorderConfig{
		Enabled:       cfg.Enabled,
		QueueSize:     cfg.QueueSize,
		BatchSize:     cfg.BatchSize,
		FlushInterval: cfg.FlushInterval,
		FlushTimeout:  cfg.FlushTimeout,
		OverflowBlock: strings.EqualFold(cfg.Overflow, overflowBlock),
		IDGenerator:   module.idGen,
	}, module.sinks...)
	return module
}

func (*Module) ModuleName() string { return Name }

func (*Module) ModuleDependencies() []string { return []string{guid.Name} }

// Initialize validates an explicitly requested durable audit sink after
// migrations have run. A configuration that promises database audit records
// must not silently degrade to process logs.
func (m *Module) Initialize(context.Context) error {
	if !m.cfg.Enabled || !m.cfg.Database.Enabled {
		return nil
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return ErrDatabaseSinkUnavailable
	}
	db := m.runtime.Database().Write()
	if db == nil {
		return ErrDatabaseSinkUnavailable
	}
	table := m.runtime.TablePrefix() + model.OperationLog{}.TableName()
	if !db.Migrator().HasTable(table) {
		return fmt.Errorf("%w: table %s does not exist", ErrDatabaseSinkUnavailable, table)
	}
	return nil
}

// ProvideServices exposes the Recorder contract to dependent modules.
func (m *Module) ProvideServices(services *module.Services) error {
	return module.Provide[Recorder](services, m.recorder)
}

// InjectServices binds the recorder to the application's wuid-managed snowflake
// generator so event IDs use per-replica worker IDs and stay unique across
// processes.
func (m *Module) InjectServices(services *module.Services) error {
	source, err := module.Resolve[guid.Source](services)
	if err != nil {
		return err
	}
	m.recorder.SetIDGenerator(source.Next)
	return nil
}

// Start launches the asynchronous dispatcher.
func (m *Module) Start(context.Context) error {
	m.recorder.Start()
	return nil
}

// Stop drains pending events and stops the dispatcher. It runs after the
// protocol servers have stopped, so no new requests are being audited.
func (m *Module) Stop(ctx context.Context) error {
	return m.recorder.Stop(ctx)
}

// Record enqueues an event produced by application code.
func (m *Module) Record(ctx context.Context, event Event) {
	m.recorder.Record(ctx, event)
}

// Run runs a value-less operation (job, task, consumer) under audit using this
// module's recorder. Operations that return a value use the package-level
// audit.Do with the recorder resolved from the service registry.
func (m *Module) Run(ctx context.Context, spec Spec, fn func(context.Context) error) error {
	return Run(ctx, m.recorder, spec, fn)
}

// Collector returns the Prometheus collector exposing pipeline health. It is
// registered by the metrics module when both are enabled.
func (m *Module) Collector() prometheus.Collector {
	return newCollector(m.recorder)
}

func (m *Module) enabled() bool       { return m.cfg.Enabled }
func (m *Module) recordReads() bool   { return m.cfg.RecordReads }
func (m *Module) recordStreams() bool { return m.cfg.RecordStreams }
func (m *Module) captureBodies() bool { return m.cfg.CaptureBodies }

func (m *Module) maxBodyBytes() int {
	if m.cfg.MaxBodyBytes < 0 {
		return 0
	}
	return m.cfg.MaxBodyBytes
}

// redactAndTruncate redacts a complete JSON body and caps the result. Invalid
// JSON is never returned verbatim because it may contain unredacted secrets.
func (m *Module) redactAndTruncate(raw []byte) (string, bool, error) {
	if raw == nil {
		return "", false, nil
	}
	redacted, err := m.redactor.RedactJSON(raw)
	if err != nil {
		return "", false, err
	}
	if limit := m.maxBodyBytes(); limit > 0 && len(redacted) > limit {
		return truncateString(redacted, limit), true, nil
	}
	return redacted, false, nil
}

func (m *Module) record(ctx context.Context, event *Event) {
	m.recorder.Record(ctx, *event)
}

// instanceID identifies this process instance in the audit trail.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// collector is the Prometheus collector for pipeline health.
type collector struct {
	recorder *AsyncRecorder
	flushed  *prometheus.Desc
	dropped  *prometheus.Desc
	failed   *prometheus.Desc
	queued   *prometheus.Desc
	sinkErrs *prometheus.Desc
}

func newCollector(recorder *AsyncRecorder) *collector {
	return &collector{
		recorder: recorder,
		flushed:  prometheus.NewDesc("audit_events_flushed_total", "Audit events successfully persisted to at least one sink.", nil, nil),
		dropped:  prometheus.NewDesc("audit_events_dropped_total", "Audit events dropped at enqueue without reaching a sink.", nil, nil),
		failed:   prometheus.NewDesc("audit_events_failed_total", "Audit events that no sink accepted.", nil, nil),
		queued:   prometheus.NewDesc("audit_queue_depth", "Audit events currently queued for persistence.", nil, nil),
		sinkErrs: prometheus.NewDesc("audit_sink_errors_total", "Audit sink write failures.", nil, nil),
	}
}

func (collector *collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.flushed
	ch <- collector.dropped
	ch <- collector.failed
	ch <- collector.queued
	ch <- collector.sinkErrs
}

func (collector *collector) Collect(ch chan<- prometheus.Metric) {
	stats := collector.recorder.Stats()
	ch <- prometheus.MustNewConstMetric(collector.flushed, prometheus.CounterValue, float64(stats.Success))
	ch <- prometheus.MustNewConstMetric(collector.dropped, prometheus.CounterValue, float64(stats.Dropped))
	ch <- prometheus.MustNewConstMetric(collector.failed, prometheus.CounterValue, float64(stats.Failed))
	ch <- prometheus.MustNewConstMetric(collector.queued, prometheus.GaugeValue, float64(stats.Queued))
	ch <- prometheus.MustNewConstMetric(collector.sinkErrs, prometheus.CounterValue, float64(stats.SinkErrs))
}
