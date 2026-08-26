package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"7link.com/platform/internal/infra/docs"
	healthhttp "7link.com/platform/internal/infra/health/http"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

func (app *App) startHTTPServer(ctx context.Context) error {
	gw := runtime.NewServeMux()
	if err := healthhttp.RegisterGateway(ctx, gw, app.healthServer); err != nil {
		return fmt.Errorf("register health gateway: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", app.config.HttpServer.Address, app.config.HttpServer.Port)
	app.httpServer = &http.Server{
		Addr: addr,
		// docs owns /, /docs*, /openapi.json and passes everything else to the
		// generated gateway routes.
		Handler: docs.Handler(app.name, gw),
	}

	go func() {
		slog.Info("http server listening", "addr", addr, "docs", "http://"+addr+"/docs")
		if err := app.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	return nil
}
