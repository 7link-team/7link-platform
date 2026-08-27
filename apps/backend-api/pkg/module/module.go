package module

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
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
)
