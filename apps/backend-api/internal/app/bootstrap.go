package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
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
	"7link.com/platform/internal/todo"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/extension/huma/respx"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"7link.com/platform/pkg/timezone"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"gorm.io/gorm"
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
	if err := app.prepare(ctx); err != nil {
		return err
	}
	return app.lifecycle(ctx)
}

// prepare builds the application Runtime and drives the shared Module lifecycle
// prefix used by service startup and standalone commands.
func (app *App) prepare(ctx context.Context) error {
	if err := app.initializeRuntime(ctx); err != nil {
		return err
	}
	if err := app.composeModules(); err != nil {
		return err
	}
	return app.prepareModules()
}

// composeModules is the single application Module inventory used by every
// execution mode. Commands decide how far to drive the lifecycle after this
// composition step; they never maintain a second Module list.
func (app *App) composeModules() error {
	app.modules = module.NewRegistry()
	rt := app.runtime
	modules := []module.Module{
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
	}

	// Modules own activation through module.Enabler. The composition root only
	// declares the complete inventory; Registry.Activate validates the remaining
	// dependency graph after disabled modules have removed themselves.
	modules = append(modules, app.composeIAMRuntimeModules()...)

	modules = append(modules,
		todo.NewModule(rt),
		health.NewModule(rt),
	)
	return app.addModule(modules...)
}

// composeIAMRuntimeModules constructs the IAM graph once and returns it in the
// lifecycle order frozen by the migration plan.
func (app *App) composeIAMRuntimeModules() []module.Module {
	rt := app.runtime
	registry := authz.DefaultRegistry()

	var identityModule *identity.Module
	authnModule := iamauthn.NewModule(rt, iamAuthnConfig(app.config), iamauthn.WithRegistrationPrincipalCreator(
		func(ctx context.Context, db *gorm.DB, email, passwordHash, displayName string, now time.Time, ids guid.Source) (guid.ID, error) {
			if identityModule == nil || identityModule.Service == nil {
				return 0, authnext.ErrUnavailable
			}
			id, err := identityModule.CreatePendingPrincipal(ctx, db, email, passwordHash, displayName, now, ids)
			switch {
			case errors.Is(err, identity.ErrLoginConflict):
				return 0, authnext.ErrRegistrationConflict
			case errors.Is(err, identity.ErrInvalid):
				return 0, authnext.ErrInvalidRegistration
			default:
				return id, err
			}
		},
	))
	authorizer := core.NewAuthorizerWithRegistry(rt, registry)
	registrar := authz.NewRegistrar(registry, authnModule, authorizer, core.NewMembershipChecker(rt))
	app.authzRegistrar = registrar
	identityModule = identity.NewModule(rt, registrar, iamIdentityConfig(app.config))

	modules := []module.Module{
		iamaudit.NewModule(rt, registrar, iamAuditConfig(app.config)),
		core.NewModule(rt, registrar, authorizer, iamCoreConfig(app.config)),
		authnModule,
		identityModule,
		organization.NewModule(rt, registrar, iamOrganizationConfig(app.config)),
		oauth.NewModule(rt, registrar, iamOAuthConfig(app.config)),
		provisioning.NewModule(rt, registrar, iamProvisioningConfig(app.config)),
		governance.NewModule(rt, registrar, iamGovernanceConfig(app.config)),
		federation.NewModule(rt, registrar, iamFederationConfig(app.config)),
	}
	return modules
}

// composeIAMDeclarationModules builds only the six declaration-capable source
// modules. Authn, identity, and OAuth deliberately have no synthetic
// declaration constructors.
func composeIAMDeclarationModules() []module.Module {
	registrar := authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry())
	return []module.Module{
		iamaudit.NewDeclarationOnlyModule(registrar),
		core.NewDeclarationOnlyModule(registrar),
		organization.NewDeclarationOnlyModule(registrar),
		provisioning.NewDeclarationOnlyModule(registrar),
		governance.NewDeclarationOnlyModule(registrar),
		federation.NewDeclarationOnlyModule(registrar),
	}
}

// lifecycle drives the post-preparation phases through start.
func (app *App) lifecycle(ctx context.Context) error {
	if app.config.Migrate {
		if _, err := app.migrateModules(ctx, MigrationRequest{
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

// prepareModules drives the lifecycle prefix shared by service startup and
// standalone migration. Migration commands stop after this prefix and migration;
// they never initialize or start runtime work.
func (app *App) prepareModules() error {
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
	if err := goosex.ValidatePrefix(app.config.TablePrefix); err != nil {
		return err
	}
	app.runtime = appenv.NewRuntime(
		appenv.NewIdentity(app.config.Name, app.config.Version),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(app.config.TablePrefix),
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
