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

	configx "7link.com/platform/pkg/config"
	gormx "7link.com/platform/pkg/extension/gorm"
	otelx "7link.com/platform/pkg/extension/otel"
	slogx "7link.com/platform/pkg/extension/slog"
	"7link.com/platform/pkg/module"
	"7link.com/platform/pkg/timezone"
	"google.golang.org/grpc"
)

type App struct {
	// config is the parsed configuration handed to Run.
	config AppConfig

	// Db owns the application's primary/replica GORM connection pools.
	Db gormx.DatasourceRouter

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

	telemetry  *otelx.Module
	grpcServer *grpc.Server
	httpServer *http.Server
}

func (app *App) Run(config AppConfig) error {
	if err := configx.Process(&config); err != nil {
		return err
	}
	applyDebugDefaults(&config)
	if err := timezone.SetTimezone(config.Timezone); err != nil {
		return fmt.Errorf("initialize timezone %q: %w", config.Timezone, err)
	}
	app.config = config

	slog.SetDefault(slogx.NewText(os.Stdout, nil))
	app.telemetry = otelx.NewModule(config.Name, config.Version)
	app.mods = append(app.mods, app.telemetry)

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
		slog.Info("shutdown signal received, gracefully stopping servers")
	case err := <-app.serveErr:
		slog.Error("server failed, shutting down", "error", err)
		startErr = err
	}

	return errors.Join(startErr, app.shutdown())
}
