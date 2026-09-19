// Package databasemonitor provides cached datasource health and pool diagnostics.
package databasemonitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
)

const (
	Name        = "database-monitor"
	Path        = "/debug/datasources"
	SQLPath     = "/debug/sql"
	ConsolePath = "/debug/database/"

	defaultInterval   = 10 * time.Second
	defaultTimeout    = 2 * time.Second
	defaultStaleAfter = 30 * time.Second
)

var (
	ErrSnapshotUnavailable = errors.New("database monitor snapshot is unavailable")
	ErrSnapshotStale       = errors.New("database monitor snapshot is stale")
)

type Config struct {
	Interval      time.Duration
	Timeout       time.Duration
	StaleAfter    time.Duration
	ExposeHTTP    bool
	SQLCapacity   int
	SlowCapacity  int
	SlowThreshold time.Duration
}

type datasource interface {
	DatasourceNames() []string
	Probe(context.Context) gormx.HealthSnapshot
	DatasourceStats() []gormx.DatasourceStats
}

type DatasourceSnapshot struct {
	Name                string
	Readable            bool
	Writable            bool
	Up                  bool
	CheckedAt           time.Time
	LastSuccessAt       time.Time
	ProbeDuration       time.Duration
	ConsecutiveFailures uint64
	Err                 error
	Pool                gormx.DatasourceStats
}

type Snapshot struct {
	CheckedAt     time.Time
	ProbeDuration time.Duration
	Datasources   []DatasourceSnapshot
	routingErr    error
}

func (snapshot Snapshot) RoutingError() error { return snapshot.routingErr }

func (snapshot Snapshot) DatasourceError(name string) error {
	for _, item := range snapshot.Datasources {
		if item.Name == name {
			return item.Err
		}
	}
	return fmt.Errorf("database monitor: datasource %q is not configured", name)
}

type Module struct {
	runtime     *appenv.Runtime
	datasources datasource
	config      Config
	snapshot    atomic.Pointer[Snapshot]

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}

	sqlMu           sync.RWMutex
	sqlStats        map[string]*queryStat
	slowSQL         []SlowQuery
	queryTotals     map[queryMetricKey]*QueryMetric
	runningSQL      uint64
	maxRunning      uint64
	statisticsSince time.Time
}

var (
	_ module.Module               = (*Module)(nil)
	_ module.ServiceProvider      = (*Module)(nil)
	_ module.Initializer          = (*Module)(nil)
	_ module.Starter              = (*Module)(nil)
	_ module.Stopper              = (*Module)(nil)
	_ module.HTTPRegistrar        = (*Module)(nil)
	_ module.RawHTTPHandlerModule = (*Module)(nil)
)

// Monitor is the narrow contract consumed by the metrics module: the cached
// datasource health snapshot, staleness check, and accumulated SQL statistics.
type Monitor interface {
	Snapshot() Snapshot
	SnapshotStale(Snapshot) bool
	QueryMetrics() []QueryMetric
}

// NewModule builds the database monitor. The datasource router is taken from the
// shared runtime; without a router the module stays idle (Start no-ops).
func NewModule(rt *appenv.Runtime, config Config) *Module {
	config = normalizeConfig(config)
	return &Module{
		runtime: rt, datasources: rt.Database(), config: config,
		sqlStats:        make(map[string]*queryStat),
		queryTotals:     make(map[queryMetricKey]*QueryMetric),
		statisticsSince: time.Now().UTC(),
	}
}

func normalizeConfig(config Config) Config {
	if config.Interval <= 0 {
		config.Interval = defaultInterval
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.StaleAfter <= 0 {
		config.StaleAfter = defaultStaleAfter
	}
	if config.SQLCapacity <= 0 {
		config.SQLCapacity = 500
	}
	if config.SlowCapacity <= 0 {
		config.SlowCapacity = 200
	}
	if config.SlowThreshold <= 0 {
		config.SlowThreshold = 200 * time.Millisecond
	}
	return config
}

// ProvideServices exposes the monitor contract to dependent modules (metrics).
func (m *Module) ProvideServices(services *module.Services) error {
	return module.Provide[Monitor](services, m)
}

// Initialize registers the module as the datasource query observer when the
// inspector is exposed. The capability is required: if the datasource router
// does not support query observation while ExposeHTTP is true, startup fails
// instead of silently disabling SQL observation.
func (m *Module) Initialize(ctx context.Context) error {
	if !m.config.ExposeHTTP {
		return nil
	}
	if m.datasources == nil {
		// No datasources to observe; the diagnostics degrade instead of failing.
		return nil
	}
	registrar, ok := m.datasources.(interface {
		RegisterQueryObserver(gormx.QueryObserver) error
	})
	if !ok {
		return errors.New("database monitor: datasource does not support query observation")
	}
	if err := registrar.RegisterQueryObserver(m); err != nil {
		return fmt.Errorf("register database query observer: %w", err)
	}
	return nil
}

func (*Module) ModuleName() string { return Name }

func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done != nil || m.datasources == nil || len(m.datasources.DatasourceNames()) == 0 {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	m.probe(runCtx)
	go m.run(runCtx, m.done)
	return nil
}

func (m *Module) run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.probe(ctx)
		}
	}
}

func (m *Module) probe(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, m.config.Timeout)
	defer cancel()
	health := m.datasources.Probe(ctx)
	poolByName := make(map[string]gormx.DatasourceStats)
	for _, stats := range m.datasources.DatasourceStats() {
		poolByName[stats.Name] = stats
	}
	previous := m.snapshot.Load()
	result := &Snapshot{
		CheckedAt: health.CheckedAt, ProbeDuration: health.Duration,
		Datasources: make([]DatasourceSnapshot, 0, len(health.Datasources)),
		routingErr:  health.RoutingError(),
	}
	for _, item := range health.Datasources {
		current := DatasourceSnapshot{
			Name: item.Name, Up: item.Err == nil, CheckedAt: health.CheckedAt,
			ProbeDuration: item.Duration, Err: item.Err, Pool: poolByName[item.Name],
		}
		current.Readable = current.Pool.Readable
		current.Writable = current.Pool.Writable
		if before, ok := previousDatasource(previous, item.Name); ok {
			current.LastSuccessAt = before.LastSuccessAt
			current.ConsecutiveFailures = before.ConsecutiveFailures
		}
		if current.Up {
			current.LastSuccessAt = health.CheckedAt
			current.ConsecutiveFailures = 0
		} else {
			current.ConsecutiveFailures++
		}
		result.Datasources = append(result.Datasources, current)
	}
	m.snapshot.Store(result)
}

func previousDatasource(snapshot *Snapshot, name string) (DatasourceSnapshot, bool) {
	if snapshot == nil {
		return DatasourceSnapshot{}, false
	}
	for _, item := range snapshot.Datasources {
		if item.Name == name {
			return item, true
		}
	}
	return DatasourceSnapshot{}, false
}

func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop database monitor: %w", ctx.Err())
	}
}

func (m *Module) Snapshot() Snapshot {
	stored := m.snapshot.Load()
	if stored == nil {
		return Snapshot{}
	}
	result := *stored
	result.Datasources = append([]DatasourceSnapshot(nil), stored.Datasources...)
	return result
}

func (m *Module) snapshotError(snapshot Snapshot) error {
	if snapshot.CheckedAt.IsZero() {
		return ErrSnapshotUnavailable
	}
	if time.Since(snapshot.CheckedAt) > m.config.StaleAfter {
		return ErrSnapshotStale
	}
	return snapshot.RoutingError()
}

func (m *Module) SnapshotStale(snapshot Snapshot) bool {
	err := m.snapshotError(snapshot)
	return errors.Is(err, ErrSnapshotUnavailable) || errors.Is(err, ErrSnapshotStale)
}

func (m *Module) HealthCheckers() []healthservice.Checker {
	if m.datasources == nil {
		return nil
	}
	names := m.datasources.DatasourceNames()
	if len(names) == 0 {
		return nil
	}
	var once sync.Once
	var snapshot Snapshot
	load := func() Snapshot {
		once.Do(func() { snapshot = m.Snapshot() })
		return snapshot
	}
	checkers := []healthservice.Checker{{
		Name: "database",
		Check: func(context.Context) error {
			return m.snapshotError(load())
		},
	}}
	for _, datasourceName := range names {
		name := datasourceName
		checkers = append(checkers, healthservice.Checker{
			Name: "database." + name, Optional: true,
			Check: func(context.Context) error {
				current := load()
				if err := m.snapshotError(current); errors.Is(err, ErrSnapshotUnavailable) || errors.Is(err, ErrSnapshotStale) {
					return err
				}
				return current.DatasourceError(name)
			},
		})
	}
	return checkers
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if !m.config.ExposeHTTP {
		return nil
	}
	for _, operation := range []*huma.Operation{{
		OperationID: "get-datasource-diagnostics",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Inspect database datasources",
		Description: "Returns redacted datasource health and connection-pool diagnostics.",
		Tags:        []string{"diagnostics"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Datasource diagnostics",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
				},
			},
		},
	}, {
		OperationID: "get-sql-diagnostics",
		Method:      http.MethodGet,
		Path:        SQLPath,
		Summary:     "Inspect SQL execution statistics",
		Description: "Returns bounded, parameter-redacted SQL fingerprint and slow-query statistics.",
		Tags:        []string{"diagnostics"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "SQL diagnostics",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: &huma.Schema{Type: huma.TypeObject}},
				},
			},
		},
	}} {
		api.OpenAPI().AddOperation(operation)
	}
	return nil
}

// RawHTTPRoutes contributes the diagnostics handlers and the embedded console,
// mounted on the server's router when the inspector is exposed.
func (m *Module) RawHTTPRoutes() []module.RawRoute {
	if !m.config.ExposeHTTP {
		return nil
	}
	return []module.RawRoute{
		{Method: http.MethodGet, Path: Path, Handler: http.HandlerFunc(m.handle)},
		{Method: http.MethodGet, Path: SQLPath, Handler: http.HandlerFunc(m.handleSQL)},
		{Method: http.MethodGet, Path: strings.TrimSuffix(ConsolePath, "/"), Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, ConsolePath, http.StatusMovedPermanently)
		})},
		{Method: http.MethodGet, Path: ConsolePath, Handler: http.HandlerFunc(m.handleConsole)},
	}
}

func (m *Module) handle(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	respx.WriteOK(writer, request, m.Report())
}

func (m *Module) handleSQL(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	respx.WriteOK(writer, request, m.SQLReport())
}

func (m *Module) handleConsole(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	_, _ = writer.Write([]byte(consoleHTML))
}
