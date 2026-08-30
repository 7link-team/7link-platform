package module

import (
	"context"

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

	HTTPRegistrar interface {
		RegisterHTTP(ctx context.Context, router chi.Router, api huma.API) error
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
)
