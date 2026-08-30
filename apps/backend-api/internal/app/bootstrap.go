package app

import (
	"context"
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
	if err := app.initializeDatabase(ctx); err != nil {
		return err
	}
	rt := app.runtime()
	if err := app.addModule(
		i18n.NewModule(i18n.Base),
		respx.NewModule(),
		dlock.NewModule(rt),
		wuid.NewModule(rt),
		guid.NewModule(),

		// metrics before telemetry/audit keeps its HTTP middleware outermost.
		metrics.NewModule(rt, metricsConfig(app.config)),
		telemetry.NewModule(rt, telemetry.WithConfig(telemetryConfig(app.config))),
		audit.NewModule(rt, auditConfig(app.config)),
		databasemonitor.NewModule(rt, datasourceMonitorConfig(app.config)),

		migrationstatus.NewModule(migrationStatusConfig(app.config)),
		configinspector.NewModule(app.config, configInspectorConfig(app.config)),
		pprof.NewModule(rt, pprofConfig(app.config)),

		health.NewModule(),
	); err != nil {
		return err
	}
	return app.lifecycle(ctx)
}

// runtime builds the immutable app-level environment shared by modules. It is the
// only place app.config.* / app.Db are referenced during bootstrap.
func (app *App) runtime() module.Runtime {
	return module.Runtime{Name: app.config.Name, Version: app.config.Version, DB: app.Db}
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
		if _, err := runModuleMigrations(ctx, app.Db.Write(), app.modules.Modules(), MigrationConfig{
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

func (app *App) initializeDatabase(ctx context.Context) error {
	router, err := newDatasourceRouter(ctx, app.config)
	app.Db = router
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
			return fmt.Errorf("database: no configured writer is available: %w", err)
		}
		slog.Error("one or more database datasources are unavailable", "error", err)
	}
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
