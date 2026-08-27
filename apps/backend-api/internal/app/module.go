package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"reflect"

	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
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
	for _, checker := range app.healthCheckers() {
		if checker.Check == nil {
			continue
		}
		if err := checker.Check(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s health: %w", checker.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (app *App) healthCheckers() []healthservice.Checker {
	checkers := make([]healthservice.Checker, 0, len(app.mods))
	for _, appModule := range app.mods {
		checker := healthservice.Checker{Name: moduleName(appModule)}
		if healthChecker, ok := appModule.(module.HealthChecker); ok {
			checker.Check = healthChecker.CheckHealth
		}
		checkers = append(checkers, checker)
	}
	return checkers
}

func moduleName(value any) string {
	if named, ok := value.(module.Namer); ok && named.ModuleName() != "" {
		return named.ModuleName()
	}
	typeOf := reflect.TypeOf(value)
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if name := path.Base(typeOf.PkgPath()); name != "." && name != "" {
		return name
	}
	return typeOf.Name()
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

func (app *App) registerHTTPModules(ctx context.Context, router chi.Router, api huma.API) error {
	for _, m := range app.mods {
		if x, ok := m.(module.HTTPRegistrar); ok {
			if err := x.RegisterHTTP(ctx, router, api); err != nil {
				return fmt.Errorf("%T register HTTP: %w", m, err)
			}
		}
	}
	return nil
}

func (app *App) registerGRPCModules(registrar grpc.ServiceRegistrar) {
	for _, m := range app.mods {
		if x, ok := m.(module.GRPCRegistrar); ok {
			x.RegisterGRPC(registrar)
		}
	}
}
