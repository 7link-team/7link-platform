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
	apiSettings := app.config.API
	apiConfig := humax.Config{
		Name:    app.config.Name,
		Version: app.config.Version,
		Landing: humax.LandingConfig{
			Enabled:         apiSettings.Landing.Enabled,
			AdvertiseHealth: apiSettings.Landing.AdvertiseHealth,
		},
		Docs: humax.EndpointConfig{
			Enabled:   apiSettings.Docs.Enabled,
			Advertise: apiSettings.Docs.Advertise,
		},
		OpenAPI: humax.EndpointConfig{
			Enabled:   apiSettings.OpenAPI.Enabled,
			Advertise: apiSettings.OpenAPI.Advertise,
		},
	}
	api := humax.New(router, apiConfig)
	if err := app.registerHTTPModules(app.ctx, router, api); err != nil {
		return err
	}
	registerPprof(router, api, app.config.Name, app.config.Pprof.Enabled)

	addr := net.JoinHostPort(app.config.HttpServer.Address, strconv.Itoa(app.config.HttpServer.Port))
	app.httpServer = &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		slog.Info("http server listening",
			"addr", addr,
			"landing", apiConfig.Landing.Enabled,
			"docs", apiConfig.Docs.Enabled,
			"openapi", apiConfig.OpenAPI.Enabled,
		)
		if err := app.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	return nil
}
