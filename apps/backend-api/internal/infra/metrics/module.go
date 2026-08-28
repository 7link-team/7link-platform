// Package metrics provides process-local Prometheus instrumentation.
package metrics

import (
	"context"
	"net/http"
	"strconv"

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

func NewModule() *Module {
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
	return &Module{
		registry:     registry,
		requests:     requests,
		duration:     duration,
		responseSize: responseSize,
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
		Tags:        []string{"Operations"},
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
