package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/health"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/wuid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/i18n"
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

	app.mods = append(app.mods,
		app.databaseMonitor,
		i18n.NewModule(i18n.Base),
		respx.NewModule(),
		dlock.NewModule(app.Db),
		wuid.NewModule(app.Db),
		guid.NewModule(),

		// .. basic modules
	)

	if app.config.API.Metrics.Enabled {
		app.metrics = metrics.NewModule(app.Db, app.databaseMonitor)
		app.mods = append(app.mods, app.metrics)
	}
	if app.config.Pprof.Enabled {
		app.mods = append(app.mods, pprof.NewModule(app.config.Name))
	}
	if app.config.API.MigrationStatus.Enabled {
		app.mods = append(app.mods, migrationstatus.NewModule(func(ctx context.Context) ([]migrationstatus.Report, error) {
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
		app.mods = append(app.mods, inspector)
	}

	// app.mods = append(app.mods,
	// .. biz modules
	// )
	app.mods = append(app.mods,
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
