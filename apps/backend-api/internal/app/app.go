package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/pkg/appenv"
	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/module"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ErrMaintenanceSchemaNotReady identifies a dirty or unmigrated module graph.
var ErrMaintenanceSchemaNotReady = errors.New("maintenance schema is not ready")

// MaintenanceOperation is an out-of-band use case executed against the same
// initialized services as the server, without opening network listeners.
type MaintenanceOperation func(*module.Services, *gorm.DB, *dlock.Module) error

// RunMaintenance owns the complete lifecycle for one synchronous operational
// use case. Business decisions remain in internal/maintenance.
func RunMaintenance(ctx context.Context, config AppConfig, operation MaintenanceOperation) (resultErr error) {
	if operation == nil {
		return errors.New("maintenance operation is required")
	}
	if err := configureApplication(&config); err != nil {
		return err
	}
	application := &App{config: config}
	defer func() { resultErr = errors.Join(resultErr, application.shutdown()) }()
	if err := application.prepare(ctx); err != nil {
		return fmt.Errorf("prepare maintenance application: %w", err)
	}
	if _, err := application.migrateModules(ctx, MigrationRequest{Action: MigrationValidate}); err != nil {
		return errors.Join(ErrMaintenanceSchemaNotReady, err)
	}
	if err := application.modules.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize maintenance modules: %w", err)
	}
	if err := application.modules.Start(ctx); err != nil {
		return fmt.Errorf("start maintenance modules: %w", err)
	}
	if application.runtime == nil || application.runtime.Database() == nil || application.runtime.Database().Write() == nil {
		return errors.New("maintenance requires a writable database")
	}
	lockModule, found := application.modules.ByName(dlock.Name)
	locker, ok := lockModule.(*dlock.Module)
	if !found || !ok {
		return errors.New("maintenance requires the distributed lock module")
	}
	return operation(application.modules.Services(), application.runtime.Database().Write(), locker)
}

type App struct {
	// config is the parsed configuration handed to Run.
	config AppConfig

	// runtime is the single application-lifetime object shared by modules.
	runtime *appenv.Runtime

	// modules is the lifecycle container for the application's modules.
	// bootstrap registers the bill of materials; the registry owns module phase
	// state while App orchestrates migration between wiring and initialization.
	modules *module.Registry

	// ctx is the application's root context; cancel tears down background workers
	// during shutdown. Set in Run.
	ctx    context.Context
	cancel context.CancelFunc

	// serveErr receives a fatal error that must bring the whole app down: a
	// server's Serve/ListenAndServe failing after bind. Buffered by the number of
	// server sources so no reporter blocks if both fail at once.
	serveErr chan error

	grpcServer *grpc.Server
	httpServer *http.Server
}

// New constructs an application lifecycle with immutable caller-owned
// configuration. Bootstrap or Run performs the process and module setup.
func New(config AppConfig) *App { return &App{config: config} }

// Config returns the effective application configuration.
func (app *App) Config() AppConfig { return app.config }

// Runtime returns the application-lifetime runtime after Bootstrap.
func (app *App) Runtime() *appenv.Runtime { return app.runtime }

// Modules returns the composed module registry after Bootstrap.
func (app *App) Modules() *module.Registry { return app.modules }

// Bootstrap prepares, migrates, initializes, and starts the configured module
// graph without opening network listeners.
func (app *App) Bootstrap(ctx context.Context) error { return app.bootstrap(ctx) }

// Shutdown stops modules and releases application-lifetime resources.
func (app *App) Shutdown() error { return app.shutdown() }

// addModule declares modules for this run. Registration, activation, wiring, and
// lifecycle phases are owned by the module lifecycle (app.modules); bootstrap
// only lists the bill of materials.
func (app *App) addModule(mods ...module.Module) error {
	if app.modules == nil {
		return errors.New("module registry is not composed")
	}
	return app.modules.Register(mods...)
}

func (app *App) Run(config AppConfig) error {
	if err := configureApplication(&config); err != nil {
		return err
	}
	if err := writeBanner(processConsoleWriter(), config.Banner, config.Name, config.Version,
		processConsoleColorsEnabled(config.Debug)); err != nil {
		return err
	}
	app.config = config

	app.ctx, app.cancel = context.WithCancel(context.Background())

	app.serveErr = make(chan error, 2)
	if err := app.bootstrap(app.ctx); err != nil {
		return errors.Join(err, app.shutdown())
	}

	if err := app.startGRPCServer(); err != nil {
		return errors.Join(err, app.shutdown())
	}
	if err := app.startHTTPServer(); err != nil {
		return errors.Join(err, app.shutdown())
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	return app.awaitShutdown(signals)
}

// configureApplication applies the configuration and process-wide setup shared
// by every application execution mode.
func configureApplication(config *AppConfig) error {
	if err := configx.Process(config); err != nil {
		return err
	}
	applyRuntimeDefaults(config)
	applyDebugDefaults(config)
	if err := validateRuntimeConfig(*config); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return configureProcessRuntime(config.Timezone, config.Debug, config.Name)
}

// awaitShutdown blocks until a shutdown signal or a fatal serve error arrives,
// then shuts the application down. The signal source is injected: production
// wires it to os/signal.Notify in Run, while tests deliver signal values into
// the channel directly — Windows does not support process-level SIGTERM
// delivery, so the channel keeps the shutdown logic platform-independent.
func (app *App) awaitShutdown(signals <-chan os.Signal) error {
	var startErr error
	select {
	case signalValue := <-signals:
		if app.config.Shutdown.Graceful {
			slog.Info("shutdown signal received, gracefully stopping servers", "signal", signalValue)
		} else {
			slog.Info("shutdown signal received, stopping servers immediately", "signal", signalValue)
		}
	case err := <-app.serveErr:
		slog.Error("server failed, shutting down", "error", err)
		startErr = err
	}

	return errors.Join(startErr, app.shutdown())
}
