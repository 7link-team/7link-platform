package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"7link.com/platform/internal/infra/health"
	healthgrpc "7link.com/platform/internal/infra/health/grpc"
	"google.golang.org/grpc"
)

type App struct {
	name string

	// config is the parsed configuration handed to Run.
	config AppConfig

	// Db DatasourceRouter

	// mods are the application modules, in registration order. Their lifecycle
	// phases (migrate/start/register/stop) are driven by the phase runners.
	mods []any

	// ctx is the application's root context; cancel tears down background workers
	// (e.g. geoip database refresh) during shutdown. Set in Run.
	ctx    context.Context
	cancel context.CancelFunc

	// serveErr receives a fatal error that must bring the whole app down: a
	// server's Serve/ListenAndServe failing after bind, or the worker-id lease
	// being lost. Buffered by the number of such sources (2 servers + worker) so
	// no reporter blocks if several fail at once.
	serveErr chan error

	grpcServer   *grpc.Server
	httpServer   *http.Server
	healthServer *healthgrpc.Server
}

func (app *App) Run(config AppConfig) error {
	app.config = config
	app.ctx, app.cancel = context.WithCancel(context.Background())
	app.serveErr = make(chan error, 2)

	app.mods = append(app.mods,
		health.NewModule(),
	)

	if err := app.initializeModules(app.ctx); err != nil {
		return err
	}
	if err := app.startModules(app.ctx); err != nil {
		return err
	}

	if err := app.startGRPCServer(); err != nil {
		return err
	}
	if err := app.startHTTPServer(app.ctx); err != nil {
		return err
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

func (app *App) shutdown() error {
	if app.cancel != nil {
		app.cancel()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var errs []error

	if app.httpServer != nil {
		if err := app.httpServer.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
	}
	if app.grpcServer != nil {
		app.grpcServer.GracefulStop()
	}

	if err := app.stopModules(shutdownCtx); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	slog.Info("shutdown complete")
	return nil
}
