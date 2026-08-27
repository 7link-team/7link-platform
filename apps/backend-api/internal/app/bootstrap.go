package app

import (
	"context"
	"fmt"
	"log/slog"

	"7link.com/platform/internal/infra/dlock"
	grpcuimodule "7link.com/platform/internal/infra/grpcui"
	"7link.com/platform/internal/infra/health"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// bootstrap opens shared infrastructure, builds modules, injects their
// database dependencies, and runs their initialization and start phases.
func (app *App) bootstrap(ctx context.Context) error {
	app.initializeDatabase(ctx)

	app.mods = append(app.mods,
		health.NewModule(app.checkHealth),
		grpcuimodule.NewModule(app.grpcDialTarget()),
		dlock.New(&app.Db),
	)

	if err := app.initializeModules(ctx); err != nil {
		return fmt.Errorf("initialize modules: %w", err)
	}
	if err := app.startModules(ctx); err != nil {
		return fmt.Errorf("start modules: %w", err)
	}
	return nil
}

func (app *App) initializeDatabase(ctx context.Context) {
	datasources := make(map[string]gormx.Datasource, len(app.config.Database))
	for name, database := range app.config.Database {
		writable, readable := database.Writable, database.Readable
		if !writable && !readable {
			writable, readable = true, true
		}
		datasources[name] = gormx.Datasource{
			Type:            database.Type,
			DSN:             database.Dsn,
			Writable:        writable,
			Readable:        readable,
			MaxOpenConns:    database.MaxOpenConns,
			MaxIdleConns:    database.MaxIdleConns,
			ConnMaxLifetime: database.ConnMaxLifetime,
			ConnMaxIdleTime: database.ConnMaxIdleTime,
		}
	}

	router, err := gormx.NewDatasourceRouter(ctx, app.config.Debug, datasources)
	app.Db = router
	if err != nil {
		slog.Error("one or more database datasources are unavailable", "error", err)
	}
}
