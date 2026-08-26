package app

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/pkg/module"
)

// initializeModules runs the Initialize phase, stopping at the first failure so the
// app never starts against a half-initialized state.
func (app *App) initializeModules(ctx context.Context) error {
	for _, m := range app.mods {
		if x, ok := m.(module.Initializer); ok {
			if err := x.Initialize(ctx); err != nil {
				return fmt.Errorf("%T initialize: %w", m, err)
			}
		}
	}
	return nil
}

// startModules runs the Start phase, stopping at the first failure.
func (app *App) startModules(ctx context.Context) error {
	for _, m := range app.mods {
		if x, ok := m.(module.Starter); ok {
			if err := x.Start(ctx); err != nil {
				return fmt.Errorf("%T start: %w", m, err)
			}
		}
	}
	return nil
}

// checkHealth runs the HealthCheck phase for all modules that implement HealthChecker.
func (app *App) checkHealth(ctx context.Context) error {
	var errs []error
	for _, m := range app.mods {
		if x, ok := m.(module.HealthChecker); ok {
			if err := x.CheckHealth(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T health: %w", m, err))
			}
		}
	}
	return errors.Join(errs...)
}

// stopModules runs the Stop phase in reverse registration order (so dependents
// stop before their dependencies), joining every error rather than bailing on
// the first so one failing module can't strand the rest.
func (app *App) stopModules(ctx context.Context) error {
	var errs []error
	for i := len(app.mods) - 1; i >= 0; i-- {
		m := app.mods[i]
		if x, ok := m.(module.Stopper); ok {
			if err := x.Stop(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T stop: %w", m, err))
			}
		}
	}
	return errors.Join(errs...)
}

// deinitializeModules runs the Deinitialize phase in reverse registration order (so dependents
// deinitialize before their dependencies), joining every error rather than bailing on
// the first so one failing module can't strand the rest.
func (app *App) deinitializeModules(ctx context.Context) error {
	var errs []error
	for i := len(app.mods) - 1; i >= 0; i-- {
		m := app.mods[i]
		if x, ok := m.(module.Deinitializer); ok {
			if err := x.Deinitialize(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T deinitialize: %w", m, err))
			}
		}
	}
	return errors.Join(errs...)
}
