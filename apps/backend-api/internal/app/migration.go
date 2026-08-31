package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/pkg/appenv"
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

// MigrationConfig is the runtime configuration required by migration
// commands. Server and module-startup settings are intentionally excluded.
type MigrationConfig struct {
	Action   MigrationAction     `mapstructure:"-" hidden:"true"`
	Module   string              `mapstructure:"module" description:"limit the operation to one module" trim:"true"`
	To       int64               `mapstructure:"to" description:"target version for up-to or down-to" default:"-1"`
	Debug    bool                `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	Timezone string              `mapstructure:"timezone" description:"timezone" default:"UTC"`
	Database map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
}

type MigrationReport struct {
	Module string        `json:"module"`
	Status goosex.Status `json:"status"`
}

// migrationSourceProvider is satisfied by modules that own an embedded
// migration source (see module.MigrationSourceProvider in pkg/module).
type migrationSourceProvider = module.MigrationSourceProvider

// RunMigrations executes one migration command without initializing or starting
// application modules and always closes the temporary datasource router.
func RunMigrations(ctx context.Context, config MigrationConfig) ([]MigrationReport, error) {
	if err := configx.Process(&config); err != nil {
		return nil, err
	}
	appConfig := AppConfig{
		Debug:    config.Debug,
		Timezone: config.Timezone,
		Database: config.Database,
	}
	applyDebugDefaults(&appConfig)
	if err := configureProcessRuntime(config.Timezone, config.Debug, "migration"); err != nil {
		return nil, err
	}

	router, openErr := newDatasourceRouter(ctx, appConfig)
	defer func() { _ = router.Close() }()
	if openErr != nil {
		return nil, openErr
	}
	db := router.Write()
	if db == nil {
		return nil, errors.New("migration: writable database is not configured")
	}
	rt := appenv.NewRuntime(appenv.NewIdentity("", ""), appenv.WithDatabase(router))
	modules := []module.Module{
		dlock.NewModule(rt),
		wuid.NewModule(rt),
		audit.NewModule(rt, audit.Config{}),
	}
	return runModuleMigrations(ctx, db, modules, config)
}

func runModuleMigrations(
	ctx context.Context,
	db *gorm.DB,
	modules []module.Module,
	config MigrationConfig,
) ([]MigrationReport, error) {
	if db == nil {
		return nil, errors.New("migration: writable database is unavailable")
	}
	action := config.Action
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
	if slices.Contains([]MigrationAction{MigrationDown, MigrationDownTo}, action) && config.Module == "" {
		return nil, errors.New("migration: down requires a module")
	}
	if slices.Contains([]MigrationAction{MigrationUpTo, MigrationDownTo}, action) && config.To < 0 {
		return nil, errors.New("migration: target version cannot be negative")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("migration: get database pool: %w", err)
	}
	dialect := db.Dialector.Name()
	reports := make([]MigrationReport, 0)
	found := config.Module == ""
	var errs []error
	for _, appModule := range modules {
		provider, ok := appModule.(migrationSourceProvider)
		if !ok || (config.Module != "" && appModule.ModuleName() != config.Module) {
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
			migrationErr = goosex.MigrateUpTo(ctx, sqlDB, source, dialect, config.To)
		case MigrationDown:
			migrationErr = goosex.MigrateDown(ctx, sqlDB, source, dialect)
		case MigrationDownTo:
			migrationErr = goosex.MigrateDownTo(ctx, sqlDB, source, dialect, config.To)
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
		return reports, fmt.Errorf("migration: module %q is not registered", config.Module)
	}
	return reports, errors.Join(errs...)
}
