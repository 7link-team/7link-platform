package app

import (
	"context"
	"fmt"
	"log/slog"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/health"
	"7link.com/platform/internal/infra/wuid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
)

// bootstrap opens shared infrastructure, builds and wires modules, optionally
// migrates them, and then runs their initialization and start phases.
func (app *App) bootstrap(ctx context.Context) error {
	app.initializeDatabase(ctx)

	app.mods = append(app.mods,
		i18n.NewModule(i18n.Base),
		respx.NewModule(),
		health.NewModule(app.healthCheckers),
		dlock.NewModule(&app.Db),
		wuid.NewModule(&app.Db),
		guid.NewModule(),
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

func (app *App) initializeDatabase(ctx context.Context) {
	router, err := newDatasourceRouter(ctx, app.config)
	app.Db = router
	if err != nil {
		slog.Error("one or more database datasources are unavailable", "error", err)
	}
}

func newDatasourceRouter(ctx context.Context, config AppConfig) (gormx.DatasourceRouter, error) {
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
