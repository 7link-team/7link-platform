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

// bootstrap opens shared infrastructure, builds and wires modules, optionally
// migrates them, and then runs their initialization and start phases.
func (app *App) bootstrap(ctx context.Context) error {
	if err := app.initializeDatabase(ctx); err != nil {
		return err
	}
	app.databaseMonitor = databasemonitor.NewModule(app.Db, databasemonitor.Config{
		Interval:      app.config.DatasourceMonitor.Interval,
		Timeout:       app.config.DatasourceMonitor.Timeout,
		StaleAfter:    app.config.DatasourceMonitor.StaleAfter,
		ExposeHTTP:    app.config.API.DatasourceInspector.Enabled,
		SQLCapacity:   app.config.DatasourceMonitor.SQLCapacity,
		SlowCapacity:  app.config.DatasourceMonitor.SlowCapacity,
		SlowThreshold: app.config.DatasourceMonitor.SlowSQLThreshold,
	})
	if app.config.API.DatasourceInspector.Enabled {
		if err := app.Db.RegisterQueryObserver(app.databaseMonitor); err != nil {
			return fmt.Errorf("register database query observer: %w", err)
		}
	}

	// Always register the module so dependent modules can resolve Recorder even
	// when automatic auditing is disabled. The disabled recorder is a no-op and
	// does not start a dispatcher.
	app.audit = audit.NewModule(app.config.Name, app.config.Version, app.Db, audit.Config{
		Enabled:         app.config.Audit.Enabled,
		Database:        audit.DatabaseConfig{Enabled: app.config.Audit.Database.Enabled},
		RecordReads:     app.config.Audit.RecordReads,
		RecordStreams:   app.config.Audit.RecordStreams,
		MaxBodyBytes:    app.config.Audit.MaxBodyBytes,
		SensitiveFields: app.config.Audit.SensitiveFields,
		SkipRoutes:      app.config.Audit.SkipRoutes,
		QueueSize:       app.config.Audit.QueueSize,
		BatchSize:       app.config.Audit.BatchSize,
		FlushInterval:   app.config.Audit.FlushInterval,
		FlushTimeout:    app.config.Audit.FlushTimeout,
		Overflow:        app.config.Audit.Overflow,
	})

	// Registration order follows dependency direction: i18n before respx,
	// wuid before guid, guid before audit. databaseMonitor is a no-dependency
	// query observer and sits after the core so shutdown releases it first.
	if app.config.Pprof.Enabled {
		app.AddModule(pprof.NewModule(app.config.Name))
	}

	app.AddModule(
		i18n.NewModule(i18n.Base),
		respx.NewModule(),

		dlock.NewModule(app.Db),
		wuid.NewModule(app.Db),
		guid.NewModule(),

		// .. basic modules
	)

	// telemetry is always registered: local trace propagation and the X-Trace-ID
	// response header stay available even when no exporter is configured — the
	// OTLP exporter itself is gated inside telemetry.Initialize. It is registered
	// before audit so protocol interceptors run inside telemetry's span context,
	// matching the HTTP middleware order (telemetry outside audit).
	app.telemetry = telemetry.NewModule(app.config.Name, app.config.Version, telemetry.WithConfig(telemetry.Config{
		OTLP: telemetry.OTLPConfig{
			Enabled:  app.config.Telemetry.OTLP.Enabled,
			Protocol: app.config.Telemetry.OTLP.Protocol,
			Endpoint: app.config.Telemetry.OTLP.Endpoint,
			Insecure: app.config.Telemetry.OTLP.Insecure,
			Headers:  app.config.Telemetry.OTLP.Headers,
			Timeout:  app.config.Telemetry.OTLP.Timeout,
		},
	}))
	app.AddModule(app.telemetry)

	app.AddModule(
		app.audit,
		app.databaseMonitor,

		// .. enhance modules
	)

	if app.config.API.Metrics.Enabled {
		app.metrics = metrics.NewModule(app.Db, app.databaseMonitor)
		app.AddModule(app.metrics)
		for _, appModule := range app.mods {
			collector, ok := appModule.(module.PrometheusCollectorModule)
			if !ok {
				continue
			}
			if err := app.metrics.RegisterCollector(collector.Collector()); err != nil {
				return fmt.Errorf("register module metrics: %w", err)
			}
		}
	}
	if app.config.API.MigrationStatus.Enabled {
		app.AddModule(migrationstatus.NewModule(func(ctx context.Context) ([]migrationstatus.Report, error) {
			reports, err := runModuleMigrations(ctx, app.Db.Write(), app.mods, MigrationConfig{
				Action: MigrationStatus,
				To:     -1,
			})
			result := make([]migrationstatus.Report, len(reports))
			for index, report := range reports {
				result[index] = migrationstatus.Report{Module: report.Module, Status: report.Status}
			}
			return result, err
		}))
	}
	if app.config.API.ConfigInspector.Enabled {
		inspector, err := configinspector.NewModule(app.config)
		if err != nil {
			return err
		}
		app.AddModule(inspector)
	}

	// app.AddModule(app.mods,
	// .. biz modules
	// )
	app.AddModule(
		health.NewModule(app.healthCheckers),
		// .. completion modules
	)

	if err := app.wireModules(); err != nil {
		return fmt.Errorf("wire modules: %w", err)
	}
	if app.config.Migrate {
		if _, err := runModuleMigrations(ctx, app.Db.Write(), app.mods, MigrationConfig{
			Action: MigrationAuto,
		}); err != nil {
			return fmt.Errorf("migrate modules: %w", err)
		}
	}
	if err := app.initializeModules(ctx); err != nil {
		return fmt.Errorf("initialize modules: %w", err)
	}
	if err := app.startModules(ctx); err != nil {
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
