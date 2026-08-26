package module

import (
	"context"
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
)
