// Package metrics provides process-local Prometheus instrumentation.
package metrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"7link.com/platform/internal/infra/databasemonitor"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/felixge/httpsnoop"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	Name           = "metrics"
	Path           = "/metrics"
	unmatchedRoute = "unmatched"
)

// Module owns an isolated registry and HTTP server metrics.
type Module struct {
	registry     *prometheus.Registry
	requests     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	responseSize *prometheus.HistogramVec
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.HTTPRegistrar = (*Module)(nil)
)

func (*Module) ModuleName() string { return Name }

func NewModule(datasources *gormx.DatasourceRouter, monitors ...*databasemonitor.Module) *Module {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_server_requests_total",
		Help: "Total number of HTTP requests handled by the server.",
	}, []string{"method", "route", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_request_duration_seconds",
		Help:    "HTTP server request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route", "status"})
	responseSize := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_server_response_size_bytes",
		Help:    "HTTP server response size in bytes.",
		Buckets: prometheus.ExponentialBuckets(128, 2, 16),
	}, []string{"method", "route", "status"})
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		requests,
		duration,
		responseSize,
	)
	if datasources != nil {
		var monitor *databasemonitor.Module
		if len(monitors) > 0 {
			monitor = monitors[0]
		}
		registry.MustRegister(newDatasourceCollector(datasources, monitor))
	}
	return &Module{
		registry:     registry,
		requests:     requests,
		duration:     duration,
		responseSize: responseSize,
	}
}

// RegisterCollector adds an application-owned collector (e.g. the audit
// pipeline counters) to the metrics registry. A duplicate registration is a
// no-op, so the method is safe to call from bootstrap even if the collector was
// already registered.
func (m *Module) RegisterCollector(collector prometheus.Collector) error {
	if m == nil || m.registry == nil || collector == nil {
		return nil
	}
	if err := m.registry.Register(collector); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return nil
		}
		return err
	}
	return nil
}

type datasourceCollector struct {
	router             *gormx.DatasourceRouter
	monitor            *databasemonitor.Module
	up                 *prometheus.Desc
	probeDuration      *prometheus.Desc
	lastProbe          *prometheus.Desc
	lastSuccess        *prometheus.Desc
	consecutiveFailure *prometheus.Desc
	max                *prometheus.Desc
	open               *prometheus.Desc
	inUse              *prometheus.Desc
	idle               *prometheus.Desc
	waits              *prometheus.Desc
	wait               *prometheus.Desc
	maxIdleClosed      *prometheus.Desc
	maxIdleTimeClosed  *prometheus.Desc
	maxLifetimeClosed  *prometheus.Desc
	queryExecutions    *prometheus.Desc
	querySuccesses     *prometheus.Desc
	queryErrors        *prometheus.Desc
	queryDuration      *prometheus.Desc
	queryDurationHist  *prometheus.Desc
	queryMinDuration   *prometheus.Desc
	queryMaxDuration   *prometheus.Desc
	queryRunning       *prometheus.Desc
	queryConcurrentMax *prometheus.Desc
	queryRows          *prometheus.Desc
	queryMaxRows       *prometheus.Desc
}

func newDatasourceCollector(router *gormx.DatasourceRouter, monitor *databasemonitor.Module) *datasourceCollector {
	labels := []string{"datasource", "readable", "writable"}
	return &datasourceCollector{
		router: router, monitor: monitor,
		up:                 prometheus.NewDesc("database_datasource_up", "Whether the datasource passed its most recent health check.", labels, nil),
		probeDuration:      prometheus.NewDesc("database_probe_duration_seconds", "Duration of the most recent datasource health probe.", labels, nil),
		lastProbe:          prometheus.NewDesc("database_last_probe_timestamp_seconds", "Unix timestamp of the most recent datasource health probe.", labels, nil),
		lastSuccess:        prometheus.NewDesc("database_last_success_timestamp_seconds", "Unix timestamp of the most recent successful datasource health probe.", labels, nil),
		consecutiveFailure: prometheus.NewDesc("database_consecutive_probe_failures", "Number of consecutive failed datasource health probes.", labels, nil),
		max:                prometheus.NewDesc("database_pool_max_open_connections", "Maximum number of open datasource connections.", labels, nil),
		open:               prometheus.NewDesc("database_pool_open_connections", "Number of established datasource connections.", labels, nil),
		inUse:              prometheus.NewDesc("database_pool_in_use_connections", "Number of datasource connections currently in use.", labels, nil),
		idle:               prometheus.NewDesc("database_pool_idle_connections", "Number of idle datasource connections.", labels, nil),
		waits:              prometheus.NewDesc("database_pool_wait_count_total", "Total waits for a datasource connection.", labels, nil),
		wait:               prometheus.NewDesc("database_pool_wait_duration_seconds_total", "Total time blocked waiting for datasource connections.", labels, nil),
		maxIdleClosed:      prometheus.NewDesc("database_pool_max_idle_closed_total", "Connections closed because the idle pool limit was exceeded.", labels, nil),
		maxIdleTimeClosed:  prometheus.NewDesc("database_pool_max_idle_time_closed_total", "Connections closed because the idle timeout was exceeded.", labels, nil),
		maxLifetimeClosed:  prometheus.NewDesc("database_pool_max_lifetime_closed_total", "Connections closed because the maximum lifetime was exceeded.", labels, nil),
		queryExecutions:    prometheus.NewDesc("database_sql_executions_total", "Total SQL executions by datasource and operation.", []string{"datasource", "operation"}, nil),
		querySuccesses:     prometheus.NewDesc("database_sql_successes_total", "Total successful SQL executions by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryErrors:        prometheus.NewDesc("database_sql_errors_total", "Total failed SQL executions by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryDuration:      prometheus.NewDesc("database_sql_duration_seconds_total", "Total SQL execution duration by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryDurationHist:  prometheus.NewDesc("database_sql_duration_seconds", "SQL execution duration distribution by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryMinDuration:   prometheus.NewDesc("database_sql_min_duration_seconds", "Shortest completed SQL execution by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryMaxDuration:   prometheus.NewDesc("database_sql_max_duration_seconds", "Longest completed SQL execution by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryRunning:       prometheus.NewDesc("database_sql_running", "SQL executions currently running by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryConcurrentMax: prometheus.NewDesc("database_sql_concurrent_max", "Maximum observed concurrent SQL executions by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryRows:          prometheus.NewDesc("database_sql_rows_total", "Total rows returned or affected by datasource and operation.", []string{"datasource", "operation"}, nil),
		queryMaxRows:       prometheus.NewDesc("database_sql_max_rows", "Maximum rows returned or affected by one execution.", []string{"datasource", "operation"}, nil),
	}
}

func (collector *datasourceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.up
	ch <- collector.probeDuration
	ch <- collector.lastProbe
	ch <- collector.lastSuccess
	ch <- collector.consecutiveFailure
	ch <- collector.max
	ch <- collector.open
	ch <- collector.inUse
	ch <- collector.idle
	ch <- collector.waits
	ch <- collector.wait
	ch <- collector.maxIdleClosed
	ch <- collector.maxIdleTimeClosed
	ch <- collector.maxLifetimeClosed
	ch <- collector.queryExecutions
	ch <- collector.querySuccesses
	ch <- collector.queryErrors
	ch <- collector.queryDuration
	ch <- collector.queryDurationHist
	ch <- collector.queryMinDuration
	ch <- collector.queryMaxDuration
	ch <- collector.queryRunning
	ch <- collector.queryConcurrentMax
	ch <- collector.queryRows
	ch <- collector.queryMaxRows
}

func (collector *datasourceCollector) Collect(ch chan<- prometheus.Metric) {
	healthByName := make(map[string]databasemonitor.DatasourceSnapshot)
	monitorStale := false
	if collector.monitor != nil {
		snapshot := collector.monitor.Snapshot()
		monitorStale = collector.monitor.SnapshotStale(snapshot)
		for _, health := range snapshot.Datasources {
			healthByName[health.Name] = health
		}
	}
	for _, stats := range collector.router.DatasourceStats() {
		labels := []string{stats.Name, strconv.FormatBool(stats.Readable), strconv.FormatBool(stats.Writable)}
		up := 0.0
		health, monitored := healthByName[stats.Name]
		if monitored && health.Up && !monitorStale || !monitored && stats.Up {
			up = 1
		}
		ch <- prometheus.MustNewConstMetric(collector.up, prometheus.GaugeValue, up, labels...)
		if monitored {
			lastSuccess := 0.0
			if !health.LastSuccessAt.IsZero() {
				lastSuccess = float64(health.LastSuccessAt.Unix())
			}
			ch <- prometheus.MustNewConstMetric(collector.probeDuration, prometheus.GaugeValue, health.ProbeDuration.Seconds(), labels...)
			ch <- prometheus.MustNewConstMetric(collector.lastProbe, prometheus.GaugeValue, float64(health.CheckedAt.Unix()), labels...)
			ch <- prometheus.MustNewConstMetric(collector.lastSuccess, prometheus.GaugeValue, lastSuccess, labels...)
			ch <- prometheus.MustNewConstMetric(collector.consecutiveFailure, prometheus.GaugeValue, float64(health.ConsecutiveFailures), labels...)
		}
		ch <- prometheus.MustNewConstMetric(collector.max, prometheus.GaugeValue, float64(stats.Pool.MaxOpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(collector.open, prometheus.GaugeValue, float64(stats.Pool.OpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(collector.inUse, prometheus.GaugeValue, float64(stats.Pool.InUse), labels...)
		ch <- prometheus.MustNewConstMetric(collector.idle, prometheus.GaugeValue, float64(stats.Pool.Idle), labels...)
		ch <- prometheus.MustNewConstMetric(collector.waits, prometheus.CounterValue, float64(stats.Pool.WaitCount), labels...)
		ch <- prometheus.MustNewConstMetric(collector.wait, prometheus.CounterValue, stats.Pool.WaitDuration.Seconds(), labels...)
		ch <- prometheus.MustNewConstMetric(collector.maxIdleClosed, prometheus.CounterValue, float64(stats.Pool.MaxIdleClosed), labels...)
		ch <- prometheus.MustNewConstMetric(collector.maxIdleTimeClosed, prometheus.CounterValue, float64(stats.Pool.MaxIdleTimeClosed), labels...)
		ch <- prometheus.MustNewConstMetric(collector.maxLifetimeClosed, prometheus.CounterValue, float64(stats.Pool.MaxLifetimeClosed), labels...)
	}
	if collector.monitor != nil {
		for _, metric := range collector.monitor.QueryMetrics() {
			labels := []string{metric.Datasource, metric.Operation}
			ch <- prometheus.MustNewConstMetric(collector.queryExecutions, prometheus.CounterValue, float64(metric.Executions), labels...)
			ch <- prometheus.MustNewConstMetric(collector.querySuccesses, prometheus.CounterValue, float64(metric.Executions-metric.Errors), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryErrors, prometheus.CounterValue, float64(metric.Errors), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryDuration, prometheus.CounterValue, metric.TotalDuration.Seconds(), labels...)
			ch <- prometheus.MustNewConstHistogram(collector.queryDurationHist, metric.Executions, metric.TotalDuration.Seconds(), metric.DurationBuckets.CumulativeSeconds(), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryMinDuration, prometheus.GaugeValue, metric.MinDuration.Seconds(), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryMaxDuration, prometheus.GaugeValue, metric.MaxDuration.Seconds(), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryRunning, prometheus.GaugeValue, float64(metric.Running), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryConcurrentMax, prometheus.GaugeValue, float64(metric.ConcurrentMax), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryRows, prometheus.CounterValue, float64(metric.TotalRows), labels...)
			ch <- prometheus.MustNewConstMetric(collector.queryMaxRows, prometheus.GaugeValue, float64(metric.MaxRows), labels...)
		}
	}
}

func (m *Module) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		Registry:          m.registry,
	})
}

// RegisterHTTP mounts the scrape handler and describes it in the API contract.
func (m *Module) RegisterHTTP(_ context.Context, router chi.Router, api huma.API) error {
	router.Handle(Path, m.Handler())
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "get-prometheus-metrics",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Collect Prometheus metrics",
		Description: "Returns process, Go runtime, and HTTP server metrics in Prometheus exposition format.",
		Tags:        []string{"Metrics"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Prometheus metrics",
				Content: map[string]*huma.MediaType{
					"text/plain": {Schema: &huma.Schema{Type: huma.TypeString}},
				},
			},
		},
	})
	return nil
}

func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		metrics := httpsnoop.CaptureMetrics(next, response, request)
		route := chi.RouteContext(request.Context()).RoutePattern()
		if route == "" {
			route = unmatchedRoute
		}
		labels := []string{request.Method, route, strconv.Itoa(metrics.Code)}
		m.requests.WithLabelValues(labels...).Inc()
		m.duration.WithLabelValues(labels...).Observe(metrics.Duration.Seconds())
		m.responseSize.WithLabelValues(labels...).Observe(float64(metrics.Written))
	})
}
