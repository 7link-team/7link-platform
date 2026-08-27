package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/wuid"
	configx "7link.com/platform/pkg/config"
	goosex "7link.com/platform/pkg/extension/goose"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/module"
	"7link.com/platform/pkg/timezone"
	"gorm.io/gorm"
)

type MigrationAction string

const (
	MigrationStatus   MigrationAction = "status"
	MigrationValidate MigrationAction = "validate"
	MigrationUpOnly   MigrationAction = "up-only"
	MigrationIgnore   MigrationAction = "ignore"
	MigrationDown     MigrationAction = "down"
)

type MigrationRequest struct {
	Action MigrationAction
	Module string
	To     int64
}

// MigrationConfig is the runtime configuration required by migration
// commands. Server and module-startup settings are intentionally excluded.
type MigrationConfig struct {
	Debug    bool                `mapstructure:"debug" short:"d" description:"debug mode" default:"false"`
	Timezone string              `mapstructure:"timezone" description:"timezone" default:"UTC"`
	Database map[string]Database `mapstructure:"database" group:"database" mapkey:"<dbkey>"`
}

type MigrationReport struct {
	Module string
	Status goosex.Status
}

type migrationSourceProvider interface {
	MigrationSource() goosex.Source
}

// RunMigrations executes one migration command without initializing or starting
// application modules and always closes the temporary datasource router.
func RunMigrations(ctx context.Context, config MigrationConfig, request MigrationRequest) ([]MigrationReport, error) {
	if err := configx.Process(&config); err != nil {
		return nil, err
	}
	appConfig := AppConfig{
		Debug:    config.Debug,
		Timezone: config.Timezone,
		Database: config.Database,
	}
	applyDebugDefaults(&appConfig)
	if err := timezone.SetTimezone(config.Timezone); err != nil {
		return nil, fmt.Errorf("initialize timezone %q: %w", config.Timezone, err)
	}
	slog.SetDefault(slogx.NewText(os.Stdout, nil))

	router, openErr := newDatasourceRouter(ctx, appConfig)
	defer func() { _ = router.Close() }()
	if openErr != nil {
		return nil, openErr
	}
	db := router.Write()
	if db == nil {
		return nil, errors.New("migration: writable database is not configured")
	}
	modules := []module.Module{dlock.NewModule(&router), wuid.NewModule(&router)}
	return runModuleMigrations(ctx, db, modules, request)
}

func runModuleMigrations(
	ctx context.Context,
	db *gorm.DB,
	modules []module.Module,
	request MigrationRequest,
) ([]MigrationReport, error) {
	if db == nil {
		return nil, errors.New("migration: writable database is unavailable")
	}
	action := request.Action
	if action == "" {
		action = MigrationUpOnly
	}
	if !slices.Contains([]MigrationAction{
		MigrationStatus, MigrationValidate, MigrationUpOnly, MigrationIgnore, MigrationDown,
	}, action) {
		return nil, fmt.Errorf("migration: unsupported action %q", action)
	}
	if action == MigrationDown && request.Module == "" {
		return nil, errors.New("migration: down requires a module")
	}
	if request.To < 0 {
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
		provider, ok := appModule.(migrationSourceProvider)
		if !ok || (request.Module != "" && appModule.ModuleName() != request.Module) {
			continue
		}
		found = true
		source := provider.MigrationSource()
		status, inspectErr := goosex.Inspect(ctx, sqlDB, source, dialect)
		if inspectErr != nil {
			errs = append(errs, fmt.Errorf("%s migration status: %w", appModule.ModuleName(), inspectErr))
			continue
		}

		switch action {
		case MigrationStatus, MigrationIgnore:
		case MigrationValidate:
			if status.State != goosex.StateClean {
				errs = append(errs, fmt.Errorf("%s migration state is %s", appModule.ModuleName(), status.State))
			}
		case MigrationUpOnly:
			if len(status.Unknown) > 0 {
				errs = append(errs, fmt.Errorf(
					"%s database has migrations unavailable in this artifact: %v",
					appModule.ModuleName(), status.Unknown,
				))
				break
			}
			if len(status.Pending) > 0 {
				if err := goosex.Up(ctx, sqlDB, source, dialect); err != nil {
					errs = append(errs, fmt.Errorf("%s migrate up: %w", appModule.ModuleName(), err))
					break
				}
				status, inspectErr = goosex.Inspect(ctx, sqlDB, source, dialect)
				if inspectErr != nil {
					errs = append(errs, fmt.Errorf("%s migration status after up: %w", appModule.ModuleName(), inspectErr))
					continue
				}
			}
		case MigrationDown:
			if len(status.Unknown) > 0 {
				errs = append(errs, fmt.Errorf(
					"%s cannot migrate down without sources for versions: %v",
					appModule.ModuleName(), status.Unknown,
				))
				break
			}
			if request.To > status.Target {
				errs = append(errs, fmt.Errorf(
					"%s target version %d exceeds embedded version %d",
					appModule.ModuleName(), request.To, status.Target,
				))
				break
			}
			if request.To != 0 && !slices.Contains(status.Embedded, request.To) {
				errs = append(errs, fmt.Errorf(
					"%s target version %d is not embedded",
					appModule.ModuleName(), request.To,
				))
				break
			}
			if err := goosex.DownSourceTo(ctx, sqlDB, source, dialect, request.To); err != nil {
				errs = append(errs, fmt.Errorf("%s migrate down to %d: %w", appModule.ModuleName(), request.To, err))
				break
			}
			status, inspectErr = goosex.Inspect(ctx, sqlDB, source, dialect)
			if inspectErr != nil {
				errs = append(errs, fmt.Errorf("%s migration status after down: %w", appModule.ModuleName(), inspectErr))
				continue
			}
		}
		reports = append(reports, MigrationReport{Module: appModule.ModuleName(), Status: status})
	}
	if !found {
		return reports, fmt.Errorf("migration: module %q is not registered", request.Module)
	}
	return reports, errors.Join(errs...)
}
