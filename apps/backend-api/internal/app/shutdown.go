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
	timeout := app.config.Shutdown.Timeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var errs []error
	if app.config.Shutdown.Graceful {
		if app.modules != nil {
			if err := app.modules.Quiesce(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
		}
		if app.httpServer != nil || app.grpcServer != nil {
			if err := waitForDrain(shutdownCtx, app.config.Shutdown.DrainDelay); err != nil {
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
	} else {
		if app.httpServer != nil {
			if err := stopHTTPServerImmediately(app.httpServer); err != nil {
				errs = append(errs, err)
			}
		}
		if app.grpcServer != nil {
			stopGRPCServerImmediately(app.grpcServer)
		}
	}
	if app.cancel != nil {
		app.cancel()
	}
	if app.modules != nil {
		if err := app.modules.Stop(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
		if err := app.modules.Deinitialize(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
	}
	if app.runtime != nil {
		if err := app.runtime.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	slog.Info("shutdown complete")
	return nil
}

func stopHTTPServerImmediately(server httpStopper) error {
	if err := server.Close(); err != nil {
		return fmt.Errorf("http server immediate close: %w", err)
	}
	return nil
}

func stopGRPCServerImmediately(server grpcStopper) {
	server.Stop()
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
