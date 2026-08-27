package module

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type (
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
