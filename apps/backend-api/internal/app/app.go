package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"7link.com/platform/internal/infra/databasemonitor"
	metrics "7link.com/platform/internal/infra/metrics"
	telemetry "7link.com/platform/internal/infra/telemetry"
	configx "7link.com/platform/pkg/config"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"google.golang.org/grpc"
)

type App struct {
	// config is the parsed configuration handed to Run.
	config AppConfig

	// Db owns the application's primary/replica GORM connection pools.
	Db *gormx.DatasourceRouter

	// mods are the application modules, in registration order. Their lifecycle
	// phases (migrate/start/register/stop) are driven by the phase runners.
	mods []module.Module
	// services contains application-scoped contracts provided and consumed by
	// modules during the wiring phase.
	services *module.Services
	// initialized and started contain only modules that completed the matching
	// lifecycle phase. Shutdown consumes them in reverse order.
	initialized []module.Module
	started     []module.Module

	// ctx is the application's root context; cancel tears down background workers
	// (e.g. geoip database refresh) during shutdown. Set in Run.
	ctx    context.Context
	cancel context.CancelFunc

	// serveErr receives a fatal error that must bring the whole app down: a
	// server's Serve/ListenAndServe failing after bind. Buffered by the number of
	// server sources so no reporter blocks if both fail at once.
	serveErr chan error

	telemetry       *telemetry.Module
	databaseMonitor *databasemonitor.Module
	metrics         *metrics.Module
	grpcServer      *grpc.Server
	httpServer      *http.Server
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
	app.telemetry = telemetry.NewModule(config.Name, config.Version, telemetry.WithConfig(config.Telemetry))
	app.mods = append(app.mods, app.telemetry)

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
		slog.Info("shutdown signal received, gracefully stopping servers")
	case err := <-app.serveErr:
		slog.Error("server failed, shutting down", "error", err)
		startErr = err
	}

	return errors.Join(startErr, app.shutdown())
}
