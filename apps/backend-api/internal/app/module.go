package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	healthservice "7link.com/platform/internal/infra/health/service"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

// wireModules builds the application service graph before any module is
// initialized. Providers are collected first so dependency resolution is not
// coupled to module registration order.
func (app *App) wireModules() error {
	app.services = module.NewServices()
	indices := make(map[string]int, len(app.mods))
	for index, appModule := range app.mods {
		if isNilModule(appModule) {
			return errors.New("module: registered module is nil")
		}
		name := appModule.ModuleName()
		if name == "" {
			return fmt.Errorf("%T: module name is empty", appModule)
		}
		if _, exists := indices[name]; exists {
			return fmt.Errorf("module: duplicate module name %q", name)
		}
		indices[name] = index
		// A Deinitializer without an Initializer owns resources from construction
		// time and must always be included in shutdown rollback.
		if _, cleanup := appModule.(module.Deinitializer); cleanup {
			if _, initialized := appModule.(module.Initializer); !initialized {
				app.initialized = append(app.initialized, appModule)
			}
		}
	}
	for index, appModule := range app.mods {
		if depender, ok := appModule.(module.Depender); ok {
			for _, dependency := range depender.ModuleDependencies() {
				dependencyIndex, exists := indices[dependency]
				if !exists {
					return fmt.Errorf("module %q requires unregistered module %q", appModule.ModuleName(), dependency)
				}
				if dependencyIndex >= index {
					return fmt.Errorf("module %q must be registered after dependency %q", appModule.ModuleName(), dependency)
				}
			}
		}
		if provider, ok := appModule.(module.ServiceProvider); ok {
			if err := provider.ProvideServices(app.services); err != nil {
				return fmt.Errorf("%T provide services: %w", appModule, err)
			}
		}
	}
	if err := app.services.Seal(); err != nil {
		return err
	}
	for _, appModule := range app.mods {
		if injector, ok := appModule.(module.ServiceInjector); ok {
			if err := injector.InjectServices(app.services); err != nil {
				return fmt.Errorf("%T inject services: %w", appModule, err)
			}
		}
	}
	return nil
}

// initializeModules runs the Initialize phase, stopping at the first failure so the
// app never starts against a half-initialized state.
func (app *App) initializeModules(ctx context.Context) error {
	for _, m := range app.mods {
		if x, ok := m.(module.Initializer); ok {
			if err := x.Initialize(ctx); err != nil {
				return fmt.Errorf("%T initialize: %w", m, err)
			}
			app.initialized = append(app.initialized, m)
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
			app.started = append(app.started, m)
		}
	}
	return nil
}

// checkHealth runs the HealthCheck phase for all modules that implement HealthChecker.
func (app *App) checkHealth(ctx context.Context) error {
	var errs []error
	for _, checker := range app.healthCheckers() {
		if checker.Check == nil || checker.Optional {
			continue
		}
		if err := checker.Check(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s health: %w", checker.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (app *App) healthCheckers() []healthservice.Checker {
	checkers := make([]healthservice.Checker, 0, len(app.mods)+1)
	checkers = append(checkers, datasourceHealthCheckers(app.Db)...)
	for _, appModule := range app.mods {
		checker := healthservice.Checker{Name: appModule.ModuleName()}
		if healthChecker, ok := appModule.(module.HealthChecker); ok {
			checker.Check = healthChecker.CheckHealth
		}
		checkers = append(checkers, checker)
	}
	return checkers
}

type datasourceHealthProber interface {
	DatasourceNames() []string
	Probe(context.Context) gormx.HealthSnapshot
}

func datasourceHealthCheckers(datasources datasourceHealthProber) []healthservice.Checker {
	if datasources == nil {
		return nil
	}
	names := datasources.DatasourceNames()
	if len(names) == 0 {
		return nil
	}
	checkers := make([]healthservice.Checker, 0, len(names)+1)
	var once sync.Once
	var snapshot gormx.HealthSnapshot
	probe := func(ctx context.Context) gormx.HealthSnapshot {
		once.Do(func() { snapshot = datasources.Probe(ctx) })
		return snapshot
	}
	checkers = append(checkers, healthservice.Checker{
		Name: "database",
		Check: func(ctx context.Context) error {
			return probe(ctx).RoutingError()
		},
	})
	for _, datasourceName := range names {
		name := datasourceName
		checkers = append(checkers, healthservice.Checker{
			Name: "database." + name,
			Check: func(ctx context.Context) error {
				return probe(ctx).DatasourceError(name)
			},
			Optional: true,
		})
	}
	return checkers
}

func isNilModule(value module.Module) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

// stopModules runs the Stop phase in reverse registration order (so dependents
// stop before their dependencies), joining every error rather than bailing on
// the first so one failing module can't strand the rest.
func (app *App) stopModules(ctx context.Context) error {
	var errs []error
	for i := len(app.started) - 1; i >= 0; i-- {
		m := app.started[i]
		if x, ok := m.(module.Stopper); ok {
			if err := x.Stop(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T stop: %w", m, err))
			}
		}
	}
	app.started = nil
	return errors.Join(errs...)
}

// quiesceModules disables new work in reverse dependency order while protocol
// listeners remain available for readiness checks during the drain delay.
func (app *App) quiesceModules(ctx context.Context) error {
	var errs []error
	for i := len(app.started) - 1; i >= 0; i-- {
		m := app.started[i]
		if x, ok := m.(module.Quiescer); ok {
			if err := x.Quiesce(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T quiesce: %w", m, err))
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
	for i := len(app.initialized) - 1; i >= 0; i-- {
		m := app.initialized[i]
		if x, ok := m.(module.Deinitializer); ok {
			if err := x.Deinitialize(ctx); err != nil {
				errs = append(errs, fmt.Errorf("%T deinitialize: %w", m, err))
			}
		}
	}
	app.initialized = nil
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
