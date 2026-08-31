package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	configx "7link.com/platform/pkg/config"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type MigrationAction string

const (
	MigrationAuto     MigrationAction = "auto"
	MigrationStatus   MigrationAction = "status"
	MigrationValidate MigrationAction = "validate"
	MigrationUp       MigrationAction = "up"
	MigrationUpTo     MigrationAction = "up-to"
	MigrationDown     MigrationAction = "down"
	MigrationDownTo   MigrationAction = "down-to"
)

// MigrationConfig combines the application's normal configuration with the
// migration command's operation-only settings. AppConfig is squashed so serve
// and migrate read the same top-level configuration shape.
type MigrationConfig struct {
	AppConfig        `mapstructure:",squash"`
	MigrationRequest `mapstructure:",squash"`
}

// MigrationRequest contains only one migration operation's control data.
type MigrationRequest struct {
	Action MigrationAction `mapstructure:"-" hidden:"true"`
	Module string          `mapstructure:"module" description:"limit the operation to one module" trim:"true"`
	To     int64           `mapstructure:"to" description:"target version for up-to or down-to" default:"-1"`
}

type MigrationReport struct {
	Module string        `json:"module"`
	Status goosex.Status `json:"status"`
}

// RunMigrations executes one migration command without initializing or starting
// application modules. It composes and prepares the same Module Registry as the
// service path, executes only the migration phase, and shuts down through the
// normal application cleanup path.
func (app *App) RunMigrations(ctx context.Context, config MigrationConfig) (reports []MigrationReport, resultErr error) {
	if err := configx.Process(&config.MigrationRequest); err != nil {
		return nil, err
	}
	if err := configureApplication(&config.AppConfig); err != nil {
		return nil, err
	}
	app.config = config.AppConfig
	defer func() {
		resultErr = errors.Join(resultErr, app.shutdown())
	}()
	if err := app.prepare(ctx); err != nil {
		return nil, fmt.Errorf("prepare migration application: %w", err)
	}
	return app.migrateModules(ctx, config.MigrationRequest)
}

// migrateModules executes one migration action against this application's active
// Module Registry. Runtime database ownership and Module discovery never enter
// through a second command-specific path.
func (app *App) migrateModules(ctx context.Context, request MigrationRequest) ([]MigrationReport, error) {
	if app.runtime == nil || app.runtime.Database() == nil || app.runtime.Database().Write() == nil {
		return nil, errors.New("migration: writable database is not configured")
	}
	if app.modules == nil {
		return nil, errors.New("migration: modules are not composed")
	}
	return executeModuleMigrations(ctx, app.runtime.Database().Write(), app.modules.Modules(), request)
}

func executeModuleMigrations(
	ctx context.Context,
	db *gorm.DB,
	modules []module.Module,
	request MigrationRequest,
) ([]MigrationReport, error) {
	if db == nil {
		return nil, errors.New("migration: writable database is unavailable")
	}
	action := request.Action
	if !slices.Contains([]MigrationAction{
		MigrationAuto,
		MigrationStatus,
		MigrationValidate,
		MigrationUp,
		MigrationUpTo,
		MigrationDown,
		MigrationDownTo,
	}, action) {
		return nil, fmt.Errorf("migration: unsupported action %q", action)
	}
	if slices.Contains([]MigrationAction{MigrationDown, MigrationDownTo}, action) && request.Module == "" {
		return nil, errors.New("migration: down requires a module")
	}
	if slices.Contains([]MigrationAction{MigrationUpTo, MigrationDownTo}, action) && request.To < 0 {
		return nil, errors.New("migration: target version cannot be negative")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("migration: get database pool: %w", err)
	}
	dialect := db.Dialector.Name()
	reports := make([]MigrationReport, 0)
	found := request.Module == ""
	var errs []error
	for _, appModule := range modules {
		provider, ok := appModule.(module.MigrationSourceProvider)
		if !ok || (request.Module != "" && appModule.ModuleName() != request.Module) {
			continue
		}
		found = true
		source := provider.MigrationSource()
		var status goosex.Status
		var migrationErr error
		switch action {
		case MigrationAuto:
			migrationErr = goosex.Migrate(ctx, sqlDB, source, dialect)
		case MigrationStatus:
		case MigrationValidate:
		case MigrationUp:
			migrationErr = goosex.MigrateUp(ctx, sqlDB, source, dialect)
		case MigrationUpTo:
			migrationErr = goosex.MigrateUpTo(ctx, sqlDB, source, dialect, request.To)
		case MigrationDown:
			migrationErr = goosex.MigrateDown(ctx, sqlDB, source, dialect)
		case MigrationDownTo:
			migrationErr = goosex.MigrateDownTo(ctx, sqlDB, source, dialect, request.To)
		}
		if migrationErr != nil {
			errs = append(errs, fmt.Errorf("%s migration %s: %w", appModule.ModuleName(), action, migrationErr))
		}

		status, inspectErr := goosex.Inspect(ctx, sqlDB, source, dialect)
		if inspectErr != nil {
			errs = append(errs, fmt.Errorf("%s migration status: %w", appModule.ModuleName(), inspectErr))
			continue
		}
		if action == MigrationValidate && status.State != goosex.StateClean {
			errs = append(errs, fmt.Errorf("%s migration state is %s", appModule.ModuleName(), status.State))
		}
		reports = append(reports, MigrationReport{Module: appModule.ModuleName(), Status: status})
	}
	if !found {
		return reports, fmt.Errorf("migration: module %q is not registered", request.Module)
	}
	return reports, errors.Join(errs...)
}
