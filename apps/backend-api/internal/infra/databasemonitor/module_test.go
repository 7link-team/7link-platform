package databasemonitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeDatasource struct {
	mu     sync.Mutex
	calls  int
	health gormx.HealthSnapshot
	stats  []gormx.DatasourceStats
}

type observerDatasource struct {
	*fakeDatasource
	err      error
	observer gormx.QueryObserver
}

func (datasource *observerDatasource) RegisterQueryObserver(observer gormx.QueryObserver) error {
	datasource.observer = observer
	return datasource.err
}

func (fake *fakeDatasource) DatasourceNames() []string {
	return []string{"primary", "replica"}
}

func (fake *fakeDatasource) Probe(context.Context) gormx.HealthSnapshot {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	result := fake.health
	result.CheckedAt = time.Now().UTC()
	return result
}

func (fake *fakeDatasource) DatasourceStats() []gormx.DatasourceStats {
	return append([]gormx.DatasourceStats(nil), fake.stats...)
}

func (fake *fakeDatasource) callCount() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.calls
}

func newFakeDatasource() *fakeDatasource {
	return &fakeDatasource{
		health: gormx.HealthSnapshot{
			Duration: 5 * time.Millisecond,
			Datasources: []gormx.DatasourceHealth{
				{Name: "primary", Duration: time.Millisecond},
				{Name: "replica", Duration: 2 * time.Millisecond, Err: errors.New("password=private")},
			},
		},
		stats: []gormx.DatasourceStats{
			{Name: "primary", Readable: true, Writable: true, Up: true, Pool: sql.DBStats{MaxOpenConnections: 8, OpenConnections: 2, InUse: 1, Idle: 1}},
			{Name: "replica", Readable: true, Pool: sql.DBStats{MaxOpenConnections: 4}},
		},
	}
}

func newTestModule(datasources datasource, config Config) *Module {
	config = normalizeConfig(config)
	return &Module{
		datasources: datasources, config: config,
		sqlStats:        make(map[string]*queryStat),
		queryTotals:     make(map[queryMetricKey]*QueryMetric),
		statisticsSince: time.Now().UTC(),
	}
}

func TestModulePollsAndHealthCheckersShareSnapshot(t *testing.T) {
	fake := newFakeDatasource()
	module := newTestModule(fake, Config{Interval: 5 * time.Millisecond, Timeout: time.Second, StaleAfter: time.Second})
	require.NoError(t, module.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, module.Stop(context.Background())) })
	// Probe increments its counter before the resulting snapshot is published.
	require.Eventually(t, func() bool {
		snapshot := module.Snapshot()
		return len(snapshot.Datasources) == 2 && snapshot.Datasources[1].ConsecutiveFailures >= 2
	}, time.Second, time.Millisecond)
	// Quiesce scheduled probes before asserting that cached reads add no calls.
	require.NoError(t, module.Stop(context.Background()))

	calls := fake.callCount()
	checkers := module.HealthCheckers()
	require.Len(t, checkers, 3)
	assert.NoError(t, checkers[0].Check(t.Context()))
	assert.NoError(t, checkers[1].Check(t.Context()))
	assert.Error(t, checkers[2].Check(t.Context()))
	assert.Equal(t, calls, fake.callCount(), "health reads must not probe the database")

	snapshot := module.Snapshot()
	require.Len(t, snapshot.Datasources, 2)
	assert.GreaterOrEqual(t, snapshot.Datasources[1].ConsecutiveFailures, uint64(2))
	assert.False(t, snapshot.Datasources[0].LastSuccessAt.IsZero())
}

func TestModuleReportsStaleSnapshots(t *testing.T) {
	module := newTestModule(newFakeDatasource(), Config{StaleAfter: time.Second})
	module.snapshot.Store(&Snapshot{CheckedAt: time.Now().Add(-2 * time.Second)})
	assert.ErrorIs(t, module.snapshotError(module.Snapshot()), ErrSnapshotStale)
	assert.Equal(t, statusStale, module.Report().Status)
}

func TestDatasourceDiagnosticsAreRedactedAndOptional(t *testing.T) {
	fake := newFakeDatasource()
	disabled := newTestModule(fake, Config{})
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	require.NoError(t, disabled.RegisterHTTP(t.Context(), api))
	for _, route := range disabled.RawHTTPRoutes() {
		router.Method(route.Method, route.Path, route.Handler)
	}
	assert.NotContains(t, api.OpenAPI().Paths, Path)

	enabled := newTestModule(fake, Config{ExposeHTTP: true})
	require.NoError(t, enabled.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, enabled.Stop(context.Background())) })
	router = chi.NewRouter()
	api = humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	require.NoError(t, enabled.RegisterHTTP(t.Context(), api))
	for _, route := range enabled.RawHTTPRoutes() {
		router.Method(route.Method, route.Path, route.Handler)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Contains(t, response.Body.String(), `"status":"DEGRADED"`)
	assert.Contains(t, response.Body.String(), `"max_open_connections":8`)
	assert.NotContains(t, response.Body.String(), "password")
	assert.NotContains(t, response.Body.String(), "private")
	require.Contains(t, api.OpenAPI().Paths, Path)
	assert.Equal(t, []string{"diagnostics"}, api.OpenAPI().Paths[Path].Get.Tags)
	require.Contains(t, api.OpenAPI().Paths, SQLPath)
	assert.Equal(t, []string{"diagnostics"}, api.OpenAPI().Paths[SQLPath].Get.Tags)
	assert.NotContains(t, api.OpenAPI().Paths, ConsolePath)

	sqlDiagnostics := httptest.NewRecorder()
	router.ServeHTTP(sqlDiagnostics, httptest.NewRequest(http.MethodGet, SQLPath, nil))
	assert.Equal(t, http.StatusOK, sqlDiagnostics.Code)
	assert.Contains(t, sqlDiagnostics.Body.String(), `"slow_queries":[]`)

	console := httptest.NewRecorder()
	router.ServeHTTP(console, httptest.NewRequest(http.MethodGet, ConsolePath, nil))
	assert.Equal(t, http.StatusOK, console.Code)
	assert.Contains(t, console.Body.String(), "Database Console")
	assert.Contains(t, console.Body.String(), `data-view="pools">Connection Pools</button>`)
	assert.Contains(t, console.Body.String(), `data-view="operations">SQL Operations</button>`)
	assert.Contains(t, console.Body.String(), `class="tab active" data-view="sql">SQL Statistics</button>`)
	assert.Contains(t, console.Body.String(), `data-view="slow">Slow SQL</button>`)
	assert.Contains(t, console.Body.String(), `id="sql" class="view active"`)
	assert.Contains(t, console.Header().Get("Content-Security-Policy"), "default-src 'self'")
}

func TestSQLStatisticsAreBoundedAndDoNotRetainErrors(t *testing.T) {
	module := newTestModule(nil, Config{SQLCapacity: 2, SlowCapacity: 1, SlowThreshold: 10 * time.Millisecond})
	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "SELECT", Table: "widgets",
		Statement: "SELECT * FROM widgets WHERE id = ?", StartedAt: time.Now().UTC(),
		Duration: 20 * time.Millisecond, Rows: 3,
	})
	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "SELECT", Table: "widgets",
		Statement: "SELECT * FROM widgets WHERE id = ?", StartedAt: time.Now().UTC(),
		Duration: 30 * time.Millisecond, Err: errors.New("password=private"),
	})
	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "UPDATE", Table: "widgets",
		Statement: "UPDATE widgets SET name = ? WHERE id = ?", StartedAt: time.Now().UTC(),
		Duration: time.Millisecond, Rows: 1,
	})
	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "DELETE", Table: "widgets",
		Statement: "DELETE FROM widgets WHERE id = ?", StartedAt: time.Now().UTC(),
		Duration: time.Millisecond, Rows: 1,
	})

	report := module.SQLReport()
	assert.Equal(t, 2, report.Capacity)
	assert.Len(t, report.Statements, 2)
	assert.Len(t, report.SlowQueries, 1)
	encoded := fmt.Sprintf("%+v", report)
	assert.NotContains(t, encoded, "password")
	assert.NotContains(t, encoded, "private")
	metrics := module.QueryMetrics()
	assert.Len(t, metrics, 3, "low-cardinality totals survive fingerprint eviction")
}

func TestSQLReportIncludesDruidStyleExecutionStatistics(t *testing.T) {
	module := newTestModule(nil, Config{SQLCapacity: 10, SlowCapacity: 10, SlowThreshold: time.Millisecond})
	activity := gormx.QueryActivity{Datasource: "primary", Operation: "SELECT", StartedAt: time.Now().UTC()}
	module.QueryStarted(t.Context(), activity)
	module.QueryStarted(t.Context(), activity)

	live := module.SQLReport()
	assert.Equal(t, uint64(2), live.Summary.Running)
	assert.Equal(t, uint64(2), live.Summary.ConcurrentMax)
	require.Len(t, live.Operations, 1)
	assert.Equal(t, uint64(2), live.Operations[0].Running)
	assert.Equal(t, uint64(2), live.Operations[0].ConcurrentMax)

	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "SELECT", Table: "widgets",
		Statement: "SELECT * FROM widgets WHERE id = ?", StartedAt: activity.StartedAt,
		Duration: 500 * time.Microsecond, Rows: 3,
	})
	module.ObserveQuery(t.Context(), gormx.QueryEvent{
		Datasource: "primary", Operation: "SELECT", Table: "widgets",
		Statement: "SELECT * FROM widgets WHERE id = ?", StartedAt: activity.StartedAt.Add(time.Second),
		Duration: 20 * time.Millisecond, Err: context.DeadlineExceeded,
	})
	module.QueryFinished(t.Context(), activity)
	module.QueryFinished(t.Context(), activity)

	report := module.SQLReport()
	assert.Equal(t, uint64(2), report.Summary.Executions)
	assert.Equal(t, uint64(1), report.Summary.Successes)
	assert.Equal(t, uint64(1), report.Summary.Errors)
	assert.InDelta(t, 0.5, report.Summary.ErrorRate, 1e-9)
	assert.Zero(t, report.Summary.Running)
	assert.InDelta(t, 0.5, report.Summary.MinimumDurationMS, 1e-9)
	assert.InDelta(t, 20.0, report.Summary.MaxDurationMS, 1e-9)
	require.Len(t, report.Statements, 1)
	statement := report.Statements[0]
	assert.Equal(t, uint64(2), statement.Executions)
	assert.Equal(t, uint64(1), statement.Successes)
	assert.Equal(t, uint64(1), statement.Errors)
	assert.InDelta(t, 0.5, statement.ErrorRate, 1e-9)
	assert.Equal(t, int64(3), statement.TotalRows)
	assert.Equal(t, int64(3), statement.MaxRows)
	assert.InDelta(t, 0.5, statement.MinimumDurationMS, 1e-9)
	assert.InDelta(t, 10.25, statement.AverageDurationMS, 1e-9)
	assert.InDelta(t, 20.0, statement.MaxDurationMS, 1e-9)
	assert.Equal(t, uint64(1), statement.DurationBuckets.LessThan1MS)
	assert.Equal(t, uint64(1), statement.DurationBuckets.From10To100MS)
	assert.Equal(t, "timeout", statement.LastErrorClass)
	require.NotNil(t, statement.LastErrorAt)
	require.Len(t, report.SlowQueries, 1)
	assert.Equal(t, "timeout", report.SlowQueries[0].ErrorClass)
	assert.False(t, report.StatisticsSince.IsZero())
	assert.Equal(t, 10, report.SlowCapacity)
	assert.InDelta(t, 1.0, report.SlowThresholdMS, 1e-9)
}

func TestModuleContractAndObserverRegistration(t *testing.T) {
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	m := NewModule(rt, Config{})
	assert.Same(t, rt, m.runtime)
	assert.Equal(t, Name, m.ModuleName())
	require.NoError(t, m.Initialize(t.Context()))
	assert.False(t, m.SnapshotStale(Snapshot{CheckedAt: time.Now()}))
	assert.True(t, m.SnapshotStale(Snapshot{}))

	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	resolved, err := module.Resolve[Monitor](services)
	require.NoError(t, err)
	assert.Same(t, m, resolved)

	require.NoError(t, newTestModule(nil, Config{ExposeHTTP: true}).Initialize(t.Context()))
	assert.ErrorContains(t, newTestModule(newFakeDatasource(), Config{ExposeHTTP: true}).Initialize(t.Context()), "does not support")

	registrar := &observerDatasource{fakeDatasource: newFakeDatasource()}
	registered := newTestModule(registrar, Config{ExposeHTTP: true})
	require.NoError(t, registered.Initialize(t.Context()))
	assert.Same(t, registered, registrar.observer)

	registrar.err = assert.AnError
	assert.ErrorIs(t, newTestModule(registrar, Config{ExposeHTTP: true}).Initialize(t.Context()), assert.AnError)
}

func TestDurationBucketsAndSQLHelpersCoverBoundaryClasses(t *testing.T) {
	var buckets DurationBuckets
	for _, duration := range []time.Duration{
		time.Nanosecond,
		time.Millisecond,
		10 * time.Millisecond,
		100 * time.Millisecond,
		time.Second,
		10 * time.Second,
	} {
		buckets.Observe(duration)
	}
	assert.Equal(t, map[float64]uint64{
		0.001: 1,
		0.01:  2,
		0.1:   3,
		1:     4,
		10:    5,
	}, buckets.CumulativeSeconds())

	for err, want := range map[error]string{
		nil:                        "",
		context.Canceled:           "canceled",
		gorm.ErrRecordNotFound:     "not_found",
		gorm.ErrDuplicatedKey:      "duplicate_key",
		gorm.ErrForeignKeyViolated: "foreign_key",
		gorm.ErrInvalidTransaction: "invalid_transaction",
		assert.AnError:             "database_error",
	} {
		assert.Equal(t, want, classifyQueryError(err))
	}

	assert.Equal(t, "abc", truncateSQL("abc", 3))
	assert.Equal(t, "你", truncateSQL("你好", 4))
}
