package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type httpStopper interface {
	Shutdown(context.Context) error
	Close() error
}

type grpcStopper interface {
	GracefulStop()
	Stop()
}

func (app *App) shutdown() error {
	timeout := app.config.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var errs []error
	if err := app.quiesceModules(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	if app.httpServer != nil || app.grpcServer != nil {
		if err := waitForDrain(shutdownCtx, app.config.DrainDelay); err != nil {
			errs = append(errs, err)
		}
	}
	if app.httpServer != nil {
		if err := stopHTTPServer(shutdownCtx, app.httpServer); err != nil {
			errs = append(errs, err)
		}
	}
	if app.grpcServer != nil {
		if err := stopGRPCServer(shutdownCtx, app.grpcServer); err != nil {
			errs = append(errs, err)
		}
	}
	if app.cancel != nil {
		app.cancel()
	}
	if err := app.stopModules(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	if err := app.deinitializeModules(shutdownCtx); err != nil {
		errs = append(errs, err)
	}
	if err := app.Db.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	slog.Info("shutdown complete")
	return nil
}

func waitForDrain(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown drain delay: %w", ctx.Err())
	}
}

func stopHTTPServer(ctx context.Context, server httpStopper) error {
	if err := server.Shutdown(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("http server graceful shutdown: %w", err),
			server.Close(),
		)
	}
	return nil
}

func stopGRPCServer(ctx context.Context, server grpcStopper) error {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		select {
		case <-done:
			return nil
		default:
		}
		server.Stop()
		<-done
		return fmt.Errorf("grpc server graceful shutdown: %w", ctx.Err())
	}
}
