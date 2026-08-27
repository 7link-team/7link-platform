package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
)

func (app *App) startHTTPServer() error {
	router := chi.NewRouter()
	api := humax.New(router, app.config.Name, app.config.Version)
	if err := app.registerHTTPModules(app.ctx, router, api); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", app.config.HttpServer.Address, app.config.HttpServer.Port)
	app.httpServer = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		slog.Info("http server listening", "addr", addr, "docs", "http://"+addr+"/docs")
		if err := app.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	return nil
}
