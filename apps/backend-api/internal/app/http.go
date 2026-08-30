package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/go-chi/chi/v5"
)

func (app *App) startHTTPServer() error {
	router := chi.NewRouter()
	// Server-wide middleware in registration order: metrics (outermost),
	// telemetry, audit, then the recovery guard, then huma's timing/locale.
	if app.modules != nil {
		for _, middleware := range app.modules.HTTPMiddlewares() {
			router.Use(middleware)
		}
	}
	router.Use(respx.Recovery)
	apiSettings := app.config.API
	apiConfig := humax.Config{
		Name:    app.config.Name,
		Version: app.config.Version,
		Landing: humax.LandingConfig{
			Enabled:         apiSettings.Landing.Enabled,
			AdvertiseHealth: apiSettings.Landing.AdvertiseHealth,
			Resources:       landingResources(apiSettings),
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
	if app.modules != nil {
		if err := app.modules.RegisterHTTP(app.ctx, api); err != nil {
			return err
		}
		for _, route := range app.modules.RawHTTPRoutes() {
			route.Mount(router)
		}
	}
	addr := net.JoinHostPort(app.config.HttpServer.Address, strconv.Itoa(app.config.HttpServer.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("http listen %s: %w", addr, err)
	}
	app.httpServer = &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: app.config.HttpServer.ReadHeaderTimeout,
		IdleTimeout:       app.config.HttpServer.IdleTimeout,
		MaxHeaderBytes:    app.config.HttpServer.MaxHeaderBytes,
	}

	go func() {
		slog.Info("http server listening",
			"addr", addr,
			"landing", apiConfig.Landing.Enabled,
			"docs", apiConfig.Docs.Enabled,
			"openapi", apiConfig.OpenAPI.Enabled,
			"metrics", apiSettings.Metrics.Enabled,
			"migration_status", apiSettings.MigrationStatus.Enabled,
			"config_inspector", apiSettings.ConfigInspector.Enabled,
			"datasource_inspector", apiSettings.DatasourceInspector.Enabled,
		)
		if err := app.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			app.serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	return nil
}

func landingResources(settings API) []humax.LandingResource {
	resources := make([]humax.LandingResource, 0, 4)
	if settings.Metrics.Enabled && settings.Metrics.Advertise {
		resources = append(resources, humax.LandingResource{
			ID: "metrics", Title: "Prometheus metrics", Description: "Process and HTTP server metrics", Href: metrics.Path,
		})
	}
	if settings.MigrationStatus.Enabled && settings.MigrationStatus.Advertise {
		resources = append(resources, humax.LandingResource{
			ID: "migrations", Title: "Migration status", Description: "Embedded and applied database versions", Href: migrationstatus.Path,
		})
	}
	if settings.ConfigInspector.Enabled && settings.ConfigInspector.Advertise {
		resources = append(resources, humax.LandingResource{
			ID: "config", Title: "Config inspector", Description: "Redacted effective configuration", Href: configinspector.Path,
		})
	}
	if settings.DatasourceInspector.Enabled && settings.DatasourceInspector.Advertise {
		resources = append(resources, humax.LandingResource{
			ID: "datasources", Title: "Database console", Description: "Datasource, connection-pool, and SQL diagnostics", Href: databasemonitor.ConsolePath,
		})
	}
	return resources
}
