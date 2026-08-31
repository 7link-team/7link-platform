package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/health"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	telemetry "7link.com/platform/internal/infra/telemetry"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"7link.com/platform/pkg/timezone"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

// configureProcessRuntime applies the process-wide settings shared by the
// serve and standalone migration entry points.
func configureProcessRuntime(timezoneName string, debug bool, label string) error {
	if err := timezone.SetTimezone(timezoneName); err != nil {
		return fmt.Errorf("initialize timezone %q: %w", timezoneName, err)
	}
	slog.SetDefault(slogx.NewConsole(processConsoleWriter(), debug, label, nil))
	return nil
}

func processConsoleWriter() io.Writer {
	return colorable.NewColorable(os.Stdout)
}

func processConsoleColorsEnabled(debug bool) bool {
	return debug || isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// bootstrap opens shared infrastructure, declares the module bill of materials,
// and drives the module lifecycle (activate -> wire -> collect metrics ->
// migrate -> initialize -> start).
func (app *App) bootstrap(ctx context.Context) error {
	app.modules = module.NewRegistry()
	if err := app.initializeRuntime(ctx); err != nil {
		return err
	}
	rt := app.runtime
	if err := app.addModule(
		i18n.NewModule(rt, i18n.Base),
		respx.NewModule(rt),
		dlock.NewModule(rt),
		wuid.NewModule(rt),
		guid.NewModule(rt),

		// metrics before telemetry/audit keeps its HTTP middleware outermost.
		metrics.NewModule(rt, metricsConfig(app.config)),
		telemetry.NewModule(rt, telemetry.WithConfig(telemetryConfig(app.config))),
		audit.NewModule(rt, auditConfig(app.config)),
		databasemonitor.NewModule(rt, datasourceMonitorConfig(app.config)),

		migrationstatus.NewModule(rt, migrationStatusConfig(app.config)),
		configinspector.NewModule(rt, app.config, configInspectorConfig(app.config)),
		pprof.NewModule(rt, pprofConfig(app.config)),

		health.NewModule(rt),
	); err != nil {
		return err
	}
	return app.lifecycle(ctx)
}

// lifecycle drives the module lifecycle from activation through start.
func (app *App) lifecycle(ctx context.Context) error {
	if err := app.modules.Activate(); err != nil {
		return err
	}
	if err := app.modules.Wire(app.provideCrossCutting); err != nil {
		return fmt.Errorf("wire modules: %w", err)
	}
	// Collector aggregation runs after Wire so the rollback list exists: a
	// failure here cannot strand construction-owned module resources.
	if err := app.collectMetrics(); err != nil {
		return fmt.Errorf("collect module metrics: %w", err)
	}
	if app.config.Migrate {
		if _, err := runModuleMigrations(ctx, app.runtime.Database().Write(), app.modules.Modules(), MigrationConfig{
			Action: MigrationAuto,
		}); err != nil {
			return fmt.Errorf("migrate modules: %w", err)
		}
	}
	if err := app.modules.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize modules: %w", err)
	}
	if err := app.modules.Start(ctx); err != nil {
		return fmt.Errorf("start modules: %w", err)
	}
	return nil
}

// initializeRuntime opens application-level infrastructure and publishes the
// single immutable environment shared by modules.
func (app *App) initializeRuntime(ctx context.Context) error {
	router, err := newDatasourceRouter(ctx, app.config)
	if err != nil {
		if router == nil {
			return fmt.Errorf("database configuration: %w", err)
		}
		configuredWriters := 0
		for _, datasource := range app.config.Database {
			if datasource.Writable {
				configuredWriters++
			}
		}
		if configuredWriters > 0 && !router.HasWriter() {
			closeErr := router.Close()
			return errors.Join(fmt.Errorf("database: no configured writer is available: %w", err), closeErr)
		}
		slog.Error("one or more database datasources are unavailable", "error", err)
	}
	app.runtime = appenv.NewRuntime(
		appenv.NewIdentity(app.config.Name, app.config.Version),
		appenv.WithDatabase(router),
	)
	return nil
}

func newDatasourceRouter(ctx context.Context, config AppConfig) (*gormx.DatasourceRouter, error) {
	datasources := make(map[string]gormx.Datasource, len(config.Database))
	for name, database := range config.Database {
		datasources[name] = gormx.Datasource{
			Type:            database.Type,
			DSN:             database.Dsn,
			Timezone:        config.Timezone,
			Writable:        database.Writable,
			Readable:        database.Readable,
			MaxOpenConns:    database.MaxOpenConns,
			MaxIdleConns:    database.MaxIdleConns,
			ConnMaxLifetime: database.ConnMaxLifetime,
			ConnMaxIdleTime: database.ConnMaxIdleTime,
		}
	}
	return gormx.NewDatasourceRouter(ctx, config.Debug, datasources)
}
