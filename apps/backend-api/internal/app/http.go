package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/felixge/httpsnoop"
	"github.com/go-chi/chi/v5"
)

func (app *App) startHTTPServer() error {
	router := chi.NewRouter()
	if err := app.mountHTTPMiddlewares(router); err != nil {
		return err
	}
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
		if err := agents.FinalizeMountedOperations(app.modules.Services(), api); err != nil {
			return err
		}
		for _, route := range app.modules.RawHTTPRoutes() {
			mountRawRoute(router, route)
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

// mountHTTPMiddlewares adapts module-provided net/http middleware and the
// metrics observation service to the selected chi server. Adapter-specific
// route-pattern extraction stays here rather than leaking into Modules.
func (app *App) mountHTTPMiddlewares(router chi.Router) error {
	// Server-wide middleware order remains metrics (outermost), telemetry,
	// audit, recovery, then huma's timing/locale middleware.
	if app.modules != nil {
		observer, err := module.Resolve[metrics.HTTPObserver](app.modules.Services())
		if err == nil {
			router.Use(httpMetricsMiddleware(observer))
		} else if !errors.Is(err, module.ErrServiceMissing) {
			return fmt.Errorf("resolve HTTP metrics observer: %w", err)
		}
		for _, middleware := range app.modules.HTTPMiddlewares() {
			router.Use(middleware)
		}
	}
	router.Use(respx.Recovery)
	return nil
}

// RegisterHTTP mounts the composed application's middleware, typed operations,
// and raw protocol routes on a caller-owned router without opening a listener.
func (app *App) RegisterHTTP(ctx context.Context, router chi.Router) error {
	if app == nil || app.modules == nil {
		return errors.New("application modules are not composed")
	}
	if err := app.mountHTTPMiddlewares(router); err != nil {
		return err
	}
	api := humax.New(router, humax.Config{Name: app.config.Name, Version: app.config.Version})
	if err := app.modules.RegisterHTTP(ctx, api); err != nil {
		return err
	}
	if err := agents.FinalizeMountedOperations(app.modules.Services(), api); err != nil {
		return err
	}
	for _, route := range app.modules.RawHTTPRoutes() {
		mountRawRoute(router, route)
	}
	return nil
}

func httpMetricsMiddleware(observer metrics.HTTPObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			captured := httpsnoop.CaptureMetrics(next, response, request)
			observer.ObserveHTTP(
				request.Method,
				chi.RouteContext(request.Context()).RoutePattern(),
				captured.Code,
				captured.Duration,
				captured.Written,
			)
		})
	}
}

func mountRawRoute(router chi.Router, route module.RawRoute) {
	if route.Method == "" {
		router.Handle(route.Path, route.Handler)
		return
	}
	router.Method(route.Method, route.Path, route.Handler)
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
