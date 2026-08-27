package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
)

func (app *App) startHTTPServer() error {
	router := chi.NewRouter()
	if app.telemetry != nil {
		router.Use(app.telemetry.HTTPMiddleware)
	}
	api := humax.New(router, app.config.Name, app.config.Version)
	if err := app.registerHTTPModules(app.ctx, router, api); err != nil {
		return err
	}

	addr := net.JoinHostPort(app.config.HttpServer.Address, strconv.Itoa(app.config.HttpServer.Port))
	app.httpServer = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		slog.Info("http server listening", "addr", addr, "docs", app.config.HttpServer.Address+":"+strconv.Itoa(app.config.HttpServer.Port))
		if err := app.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	return nil
}
