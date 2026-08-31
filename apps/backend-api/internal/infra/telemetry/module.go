package telemetry

import (
	"context"
	"net/http"

	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
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

const Name = "telemetry"

type moduleConfig struct {
	idGenerator IDGenerator
	exporters   []sdktrace.SpanExporter
	settings    Config
}

type Option func(*moduleConfig)

// WithIDGenerator overrides the UUID v4 generator used for new root traces.
// The function must return a UUID or a 32-character hexadecimal trace ID.
func WithIDGenerator(generate IDGenerator) Option {
	return func(config *moduleConfig) {
		if generate != nil {
			config.idGenerator = generate
		}
	}
}

// WithSpanExporter adds a batch processor for exported spans. A nil exporter
// is ignored so callers can assemble optional telemetry without branching.
func WithSpanExporter(exporter sdktrace.SpanExporter) Option {
	return func(config *moduleConfig) {
		if exporter != nil {
			config.exporters = append(config.exporters, exporter)
		}
	}
}

// WithConfig applies exporter configuration owned by the telemetry module.
func WithConfig(settings Config) Option {
	return func(config *moduleConfig) {
		config.settings = settings
	}
}

// Module owns the application's tracing provider and protocol instrumentation.
type Module struct {
	runtime    *appenv.Runtime
	config     moduleConfig
	provider   *sdktrace.TracerProvider
	propagator propagation.TextMapPropagator
}

var (
	_ module.Module        = (*Module)(nil)
	_ module.Initializer   = (*Module)(nil)
	_ module.Deinitializer = (*Module)(nil)
)

func (*Module) ModuleName() string { return Name }

// NewModule builds the telemetry module without acquiring resources or changing
// process-global telemetry state. Initialize reads the application identity
// from the shared runtime and publishes the configured provider.
func NewModule(rt *appenv.Runtime, options ...Option) *Module {
	config := moduleConfig{idGenerator: defaultIDGenerator}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return &Module{runtime: rt, config: config}
}

// Initialize creates the provider only after every configured exporter is
// available, then publishes the complete tracing state before protocol servers
// begin accepting requests.
func (m *Module) Initialize(ctx context.Context) error {
	if m.provider != nil {
		return nil
	}

	exporters := append([]sdktrace.SpanExporter(nil), m.config.exporters...)
	if m.config.settings.OTLP.Enabled {
		exporter, err := newOTLPExporter(ctx, m.config.settings.OTLP)
		if err != nil {
			return err
		}
		exporters = append(exporters, exporter)
	}

	idGenerator := &traceIDGenerator{generate: m.config.idGenerator}
	identity := m.runtime.Identity()
	providerOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithIDGenerator(idGenerator),
		sdktrace.WithResource(resource.NewSchemaless(
			semconv.ServiceName(identity.Name()),
			semconv.ServiceVersion(identity.Version()),
		)),
	}
	for _, exporter := range exporters {
		providerOptions = append(providerOptions, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(providerOptions...)
	propagator := propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		traceIDPropagator{idGenerator: idGenerator},
		propagation.Baggage{},
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)
	m.provider = provider
	m.propagator = propagator
	return nil
}

func (m *Module) Deinitialize(ctx context.Context) error {
	provider := m.provider
	m.provider = nil
	m.propagator = nil
	if provider == nil {
		return nil
	}
	return provider.Shutdown(ctx)
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
