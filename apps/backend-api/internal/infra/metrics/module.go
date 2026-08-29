// Package metrics provides process-local Prometheus instrumentation.
package metrics

import (
	"context"
	"net/http"
	"strconv"

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

func NewModule(datasources *gormx.DatasourceRouter) *Module {
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
		registry.MustRegister(newDatasourceCollector(datasources))
	}
	return &Module{
		registry:     registry,
		requests:     requests,
		duration:     duration,
		responseSize: responseSize,
	}
}

type datasourceCollector struct {
	router *gormx.DatasourceRouter
	up     *prometheus.Desc
	max    *prometheus.Desc
	open   *prometheus.Desc
	inUse  *prometheus.Desc
	idle   *prometheus.Desc
	waits  *prometheus.Desc
	wait   *prometheus.Desc
}

func newDatasourceCollector(router *gormx.DatasourceRouter) *datasourceCollector {
	labels := []string{"datasource", "readable", "writable"}
	return &datasourceCollector{
		router: router,
		up:     prometheus.NewDesc("database_datasource_up", "Whether the datasource passed its most recent health check.", labels, nil),
		max:    prometheus.NewDesc("database_pool_max_open_connections", "Maximum number of open datasource connections.", labels, nil),
		open:   prometheus.NewDesc("database_pool_open_connections", "Number of established datasource connections.", labels, nil),
		inUse:  prometheus.NewDesc("database_pool_in_use_connections", "Number of datasource connections currently in use.", labels, nil),
		idle:   prometheus.NewDesc("database_pool_idle_connections", "Number of idle datasource connections.", labels, nil),
		waits:  prometheus.NewDesc("database_pool_wait_count_total", "Total waits for a datasource connection.", labels, nil),
		wait:   prometheus.NewDesc("database_pool_wait_duration_seconds_total", "Total time blocked waiting for datasource connections.", labels, nil),
	}
}

func (collector *datasourceCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.up
	ch <- collector.max
	ch <- collector.open
	ch <- collector.inUse
	ch <- collector.idle
	ch <- collector.waits
	ch <- collector.wait
}

func (collector *datasourceCollector) Collect(ch chan<- prometheus.Metric) {
	for _, stats := range collector.router.DatasourceStats() {
		labels := []string{stats.Name, strconv.FormatBool(stats.Readable), strconv.FormatBool(stats.Writable)}
		up := 0.0
		if stats.Up {
			up = 1
		}
		ch <- prometheus.MustNewConstMetric(collector.up, prometheus.GaugeValue, up, labels...)
		ch <- prometheus.MustNewConstMetric(collector.max, prometheus.GaugeValue, float64(stats.Pool.MaxOpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(collector.open, prometheus.GaugeValue, float64(stats.Pool.OpenConnections), labels...)
		ch <- prometheus.MustNewConstMetric(collector.inUse, prometheus.GaugeValue, float64(stats.Pool.InUse), labels...)
		ch <- prometheus.MustNewConstMetric(collector.idle, prometheus.GaugeValue, float64(stats.Pool.Idle), labels...)
		ch <- prometheus.MustNewConstMetric(collector.waits, prometheus.CounterValue, float64(stats.Pool.WaitCount), labels...)
		ch <- prometheus.MustNewConstMetric(collector.wait, prometheus.CounterValue, stats.Pool.WaitDuration.Seconds(), labels...)
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
