package app

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/identity"
	healthservice "7link.com/platform/internal/infra/health/service"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/pkg/module"
)

// provideCrossCutting registers the app-scoped cross-module views consumed by
// the health and migration-status modules. It runs inside Registry.Wire, after
// module ServiceProviders but before the registry is sealed, and propagates every
// error.
func (app *App) provideCrossCutting(services *module.Services) error {
	if err := module.Provide[identity.PrincipalAnonymizer](services, app.anonymizePrincipalData); err != nil {
		return err
	}
	if err := module.Provide[healthservice.CheckerProvider](services, app.healthCheckers); err != nil {
		return err
	}
	return module.Provide[migrationstatus.Inspector](services, app.migrationInspector)
}

// migrationInspector aggregates the migration status of every active module. It
// is provided as the migration-status module's inspector (lazy, evaluated per
// request).
func (app *App) migrationInspector(ctx context.Context) ([]migrationstatus.Report, error) {
	if app.runtime == nil || app.runtime.Database() == nil || !app.runtime.Database().HasWriter() {
		return nil, errors.New("migration: writable database is not configured")
	}
	reports, err := app.migrateModules(ctx, MigrationRequest{
		Action: MigrationStatus,
		To:     -1,
	})
	result := make([]migrationstatus.Report, len(reports))
	for index, report := range reports {
		result[index] = migrationstatus.Report{Module: report.Module, Status: report.Status}
	}
	return result, err
}

// collectMetrics aggregates every active PrometheusCollectorModule's collector
// into every active CollectorSink (the metrics module). It runs after Wire, so
// the active set is complete and registration order is irrelevant. Zero sinks is
// a no-op; multiple sinks each receive every collector.
func (app *App) collectMetrics() error {
	for _, sink := range app.modules.Modules() {
		collectorSink, ok := sink.(module.CollectorSink)
		if !ok {
			continue
		}
		for _, source := range app.modules.Modules() {
			collectorModule, ok := source.(module.PrometheusCollectorModule)
			if !ok {
				continue
			}
			if err := collectorSink.RegisterCollector(collectorModule.Collector()); err != nil {
				return err
			}
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

type healthCheckerProvider interface {
	HealthCheckers() []healthservice.Checker
}

// healthCheckers builds the health checkers from the active module set. Modules
// that expose their own HealthCheckers replace the generic per-module status.
// A zero-value App (no registry yet) reports no checkers.
func (app *App) healthCheckers() []healthservice.Checker {
	if app.modules == nil {
		return nil
	}
	checkers := make([]healthservice.Checker, 0)
	for _, appModule := range app.modules.Modules() {
		if provider, ok := appModule.(healthCheckerProvider); ok {
			checkers = append(checkers, provider.HealthCheckers()...)
			continue
		}
		checker := healthservice.Checker{Name: appModule.ModuleName()}
		if healthChecker, ok := appModule.(module.HealthChecker); ok {
			checker.Check = healthChecker.CheckHealth
		}
		checkers = append(checkers, checker)
	}
	return checkers
}
