package otelx

import (
	"context"
	"net/http"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/stats"
)

type config struct {
	idGenerator IDGenerator
}

type Option func(*config)

// WithIDGenerator overrides the UUID v4 generator used for new root traces.
// The function must return a UUID or a 32-character hexadecimal trace ID.
func WithIDGenerator(generate IDGenerator) Option {
	return func(config *config) {
		if generate != nil {
			config.idGenerator = generate
		}
	}
}

// Module owns the application's tracing provider and protocol instrumentation.
type Module struct {
	provider   *sdktrace.TracerProvider
	propagator propagation.TextMapPropagator
}

func (*Module) ModuleName() string { return "telemetry" }

func NewModule(serviceName, serviceVersion string, options ...Option) *Module {
	config := config{idGenerator: defaultIDGenerator}
	for _, option := range options {
		option(&config)
	}

	idGenerator := &traceIDGenerator{generate: config.idGenerator}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithIDGenerator(idGenerator),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		)),
	)
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		traceIDPropagator{idGenerator: idGenerator},
		propagation.Baggage{},
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)
	return &Module{provider: provider, propagator: propagator}
}

func (m *Module) Deinitialize(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}

func (m *Module) HTTPMiddleware(next http.Handler) http.Handler {
	traced := otelhttp.NewMiddleware(
		"http.server",
		otelhttp.WithTracerProvider(m.provider),
		otelhttp.WithPropagators(m.propagator),
		otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			return "HTTP " + request.Method
		}),
	)(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		m.setHTTPTraceHeaders(w.Header(), request)
		metrics := httpsnoop.CaptureMetrics(next, w, request)
		LogHTTP(request.Context(), request, metrics)
	}))
	return traced
}

func (m *Module) setHTTPTraceHeaders(headers http.Header, request *http.Request) {
	spanContext := trace.SpanContextFromContext(request.Context())
	if !spanContext.IsValid() {
		return
	}
	request.Header.Set(TraceIDHeader, spanContext.TraceID().String())
	headers.Add("Access-Control-Expose-Headers", TraceIDHeader+", "+TraceparentHeader)
	carrier := propagation.HeaderCarrier(headers)
	propagation.TraceContext{}.Inject(request.Context(), carrier)
	traceIDPropagator{}.Inject(request.Context(), carrier)
}

// HTTPTransport creates an instrumented client transport that forwards both
// traceparent and X-Trace-ID. A nil base uses http.DefaultTransport.
func (m *Module) HTTPTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base,
		otelhttp.WithTracerProvider(m.provider),
		otelhttp.WithPropagators(m.propagator),
	)
}

func (m *Module) GRPCServerStatsHandler() stats.Handler {
	return otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(m.provider),
		otelgrpc.WithPropagators(m.propagator),
	)
}

func (m *Module) GRPCClientStatsHandler() stats.Handler {
	return otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(m.provider),
		otelgrpc.WithPropagators(m.propagator),
	)
}

func (m *Module) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return m.provider.Tracer(name, options...)
}
