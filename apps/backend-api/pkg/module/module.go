package module

import (
	"context"
	"net/http"

	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

type (
	// Module is the minimum contract every application module must satisfy.
	// Lifecycle and protocol capabilities remain optional interfaces below.
	Module interface {
		ModuleName() string
	}

	// Depender declares lifecycle dependencies. Dependencies must be registered
	// before dependents so forward startup and reverse shutdown remain correct.
	Depender interface {
		ModuleDependencies() []string
	}

	// ServiceProvider contributes application-scoped services before module
	// initialization. Register narrow contracts rather than concrete modules.
	ServiceProvider interface {
		ProvideServices(services *Services) error
	}

	// ServiceInjector resolves a module's required services after all providers
	// have registered and before module initialization.
	ServiceInjector interface {
		InjectServices(services *Services) error
	}

	// Initializer runs setup logic after migration but before starting.
	Initializer interface {
		Initialize(ctx context.Context) error
	}

	// Starter starts a module's background/runtime work.
	Starter interface {
		Start(ctx context.Context) error
	}

	HealthChecker interface {
		CheckHealth(ctx context.Context) error
	}

	// Quiescer stops new work before protocol servers drain existing requests.
	Quiescer interface {
		Quiesce(ctx context.Context) error
	}

	// Stopper releases a module's resources on shutdown.
	Stopper interface {
		Stop(ctx context.Context) error
	}

	Deinitializer interface {
		Deinitialize(ctx context.Context) error
	}

	// HTTPRegistrar registers typed operations through huma only. huma is
	// adapter-agnostic (chi/gin/fiber/...), so modules never bind to a router
	// implementation. Raw, non-typed handlers are contributed separately via
	// RawHTTPHandlerModule and mounted by the HTTP server.
	HTTPRegistrar interface {
		RegisterHTTP(ctx context.Context, api huma.API) error
	}

	GRPCRegistrar interface {
		RegisterGRPC(registrar grpc.ServiceRegistrar)
	}

	// UnaryInterceptorModule contributes a gRPC unary server interceptor. The
	// method signature is the interceptor itself, so a module's handler method
	// satisfies the interface directly.
	UnaryInterceptorModule interface {
		Module
		UnaryServerInterceptor(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error)
	}

	// StreamInterceptorModule contributes a gRPC stream server interceptor.
	StreamInterceptorModule interface {
		Module
		StreamServerInterceptor(service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error
	}

	// StatsHandlerModule contributes a gRPC stats handler.
	StatsHandlerModule interface {
		Module
		GRPCServerStatsHandler() stats.Handler
	}

	// PrometheusCollectorModule contributes a collector to the application
	// metrics registry. The metrics module registers every module collector
	// after its own collectors.
	PrometheusCollectorModule interface {
		Module
		Collector() prometheus.Collector
	}

	// Config is the base configuration inherited by conditional modules. It
	// carries only the activation switch. Always-registered modules (audit,
	// telemetry, database-monitor, health, ...) do NOT embed it and MUST NOT
	// implement Enabler.
	Config struct {
		Enabled bool
	}

	// Enabler exposes the activation switch to the lifecycle. Pure capability
	// interface (asserted against module.Module values, like Initializer or
	// Starter), so it does not embed Module.
	Enabler interface {
		Enabled() bool
	}

	// HTTPMiddlewareModule contributes a server-wide HTTP middleware.
	// Middlewares run in registration order: earlier module = outer middleware.
	// respx.Recovery is always appended innermost by the HTTP server.
	HTTPMiddlewareModule interface {
		Module
		HTTPMiddleware(next http.Handler) http.Handler
	}

	// CollectorSink accepts Prometheus collectors contributed by other modules.
	// The metrics module implements it; the lifecycle aggregates every active
	// PrometheusCollectorModule into every active CollectorSink after wiring.
	CollectorSink interface {
		Module
		RegisterCollector(collector prometheus.Collector) error
	}

	// RawHTTPHandlerModule contributes raw, non-typed HTTP handlers (metrics
	// text, pprof, HTML consoles). The HTTP server mounts them on its adapter
	// router — the only place a router implementation appears.
	RawHTTPHandlerModule interface {
		Module
		RawHTTPRoutes() []RawRoute
	}

	// RawRoute is one raw HTTP route contributed by a module. An empty Method
	// mounts the handler for every HTTP verb (chi router.Handle) — used by
	// handlers that serve multiple methods, such as the pprof analysis UI,
	// which also handles capture POSTs.
	RawRoute struct {
		Method  string
		Path    string
		Handler http.Handler
	}

	// MigrationSourceProvider exposes a module's embedded migration source so
	// the migration runner and the migration-status inspector can discover it
	// alongside the other module capabilities.
	MigrationSourceProvider interface {
		MigrationSource() goosex.Source
	}
)

// Mount registers a RawRoute on a chi router. It is the single place the server
// (and tests) turn a RawRoute into a mounted route: an empty Method mounts the
// handler for every verb (router.Handle), so every-method routes such as the
// pprof analysis UI work consistently.
func (route RawRoute) Mount(router chi.Router) {
	if route.Method == "" {
		router.Handle(route.Path, route.Handler)
		return
	}
	router.Method(route.Method, route.Path, route.Handler)
}
