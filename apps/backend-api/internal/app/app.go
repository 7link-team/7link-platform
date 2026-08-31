package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"7link.com/platform/pkg/appenv"
	configx "7link.com/platform/pkg/config"
	"7link.com/platform/pkg/module"
	"google.golang.org/grpc"
)

type App struct {
	// config is the parsed configuration handed to Run.
	config AppConfig

	// runtime is the single application-lifetime object shared by modules.
	runtime *appenv.Runtime

	// modules is the lifecycle container for the application's modules.
	// bootstrap registers the bill of materials; the registry owns activation,
	// wiring, migration, initialization, start, and shutdown.
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

// addModule declares modules for this run. Registration, activation, wiring, and
// lifecycle phases are owned by the module lifecycle (app.modules); bootstrap
// only lists the bill of materials.
func (app *App) addModule(mods ...module.Module) error {
	if app.modules == nil {
		app.modules = module.NewRegistry()
	}
	return app.modules.Register(mods...)
}

func (app *App) Run(config AppConfig) error {
	if err := configx.Process(&config); err != nil {
		return err
	}
	applyRuntimeDefaults(&config)
	applyDebugDefaults(&config)
	if err := validateRuntimeConfig(config); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := configureProcessRuntime(config.Timezone, config.Debug, config.Name); err != nil {
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

	return app.awaitShutdown()
}

func (app *App) awaitShutdown() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var startErr error
	select {
	case <-ctx.Done():
		if app.config.Shutdown.Graceful {
			slog.Info("shutdown signal received, gracefully stopping servers")
		} else {
			slog.Info("shutdown signal received, stopping servers immediately")
		}
	case err := <-app.serveErr:
		slog.Error("server failed, shutting down", "error", err)
		startErr = err
	}

	return errors.Join(startErr, app.shutdown())
}
