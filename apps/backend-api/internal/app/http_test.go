package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/wuid"
	configx "7link.com/platform/pkg/config"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartHTTPServerReportsRegistrationAndListenErrors(t *testing.T) {
	registrationErr := errors.New("registration failed")
	app := &App{
		config: validTestAppConfig(),
		ctx:    t.Context(),
		mods:   []module.Module{&registrarModule{httpErr: registrationErr}},
	}
	assert.ErrorIs(t, app.startHTTPServer(), registrationErr)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	app.mods = nil
	app.config.HttpServer.Port = listener.Addr().(*net.TCPAddr).Port
	app.serveErr = make(chan error, 1)
	assert.ErrorContains(t, app.startHTTPServer(), "http listen")
}

func TestStartHTTPServerRequiresConfiguredMetricsModule(t *testing.T) {
	app := &App{config: validTestAppConfig()}
	app.config.API.Metrics.Enabled = true
	assert.EqualError(t, app.startHTTPServer(), "http: prometheus module is unavailable")
}

func TestStartHTTPServerAppliesHardeningConfiguration(t *testing.T) {
	app := &App{
		config:   validTestAppConfig(),
		ctx:      t.Context(),
		serveErr: make(chan error, 1),
	}
	app.config.HttpServer.Port = 0
	app.config.HttpServer.ReadHeaderTimeout = 3 * time.Second
	app.config.HttpServer.IdleTimeout = 45 * time.Second
	app.config.HttpServer.MaxHeaderBytes = 8192
	require.NoError(t, app.startHTTPServer())
	require.NotNil(t, app.httpServer)
	assert.Equal(t, 3*time.Second, app.httpServer.ReadHeaderTimeout)
	assert.Equal(t, 45*time.Second, app.httpServer.IdleTimeout)
	assert.Equal(t, 8192, app.httpServer.MaxHeaderBytes)
	require.NoError(t, app.httpServer.Shutdown(t.Context()))
}

func moduleRouter(t *testing.T, app *App) (*chi.Mux, huma.API) {
	t.Helper()
	router := chi.NewRouter()
	if app.metrics != nil {
		router.Use(app.metrics.HTTPMiddleware)
	}
	api := humax.New(router, humax.Config{
		Name:    app.config.Name,
		Version: app.config.Version,
		Landing: humax.LandingConfig{Enabled: false},
		Docs:    humax.EndpointConfig{Enabled: false},
		OpenAPI: humax.EndpointConfig{Enabled: true},
	})
	require.NoError(t, app.registerHTTPModules(t.Context(), router, api))
	return router, api
}

func TestConfigInspectorReturnsRedactedJSON(t *testing.T) {
	const (
		dsn    = "postgres://user:password@database/service"
		header = "Bearer private-token"
	)
	app := &App{config: validTestAppConfig()}
	app.config.API.ConfigInspector.Enabled = true
	app.config.Database = map[string]Database{"primary": {Type: "postgres", Dsn: dsn}}
	app.config.Telemetry.OTLP.Headers = map[string]string{"authorization": header}
	inspector, err := configinspector.NewModule(app.config)
	require.NoError(t, err)
	app.mods = append(app.mods, inspector)
	router, api := moduleRouter(t, app)

	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, configinspector.Path, nil))
	require.Equal(t, http.StatusOK, jsonResponse.Code)
	assert.Equal(t, "application/json", jsonResponse.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", jsonResponse.Header().Get("Cache-Control"))
	assert.NotContains(t, jsonResponse.Body.String(), dsn)
	assert.NotContains(t, jsonResponse.Body.String(), header)
	assert.Contains(t, jsonResponse.Body.String(), configx.RedactedValue)
	var envelope respx.Envelope[map[string]any]
	require.NoError(t, json.Unmarshal(jsonResponse.Body.Bytes(), &envelope))
	assert.Zero(t, envelope.Code)

	htmlRequest := httptest.NewRequest(http.MethodGet, configinspector.Path, nil)
	htmlRequest.Header.Set("Accept", "text/html")
	htmlResponse := httptest.NewRecorder()
	router.ServeHTTP(htmlResponse, htmlRequest)
	require.Equal(t, http.StatusOK, htmlResponse.Code)
	assert.Equal(t, "application/json", htmlResponse.Header().Get("Content-Type"))
	assert.NotContains(t, htmlResponse.Body.String(), dsn)
	assert.NotContains(t, htmlResponse.Body.String(), header)
	assert.Contains(t, htmlResponse.Body.String(), configx.RedactedValue)

	assert.Contains(t, api.OpenAPI().Paths, configinspector.Path)
}

func TestMigrationStatusReturnsCleanJSON(t *testing.T) {
	app := &App{config: validTestAppConfig()}
	app.config.API.MigrationStatus.Enabled = true
	app.config.Database = map[string]Database{
		"primary": {
			Type:     "sqlite",
			Dsn:      filepath.Join(t.TempDir(), "status.sqlite"),
			Writable: true,
			Readable: true,
		},
	}
	routerDB, err := newDatasourceRouter(t.Context(), app.config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, routerDB.Close()) })
	app.Db = routerDB
	app.mods = []module.Module{dlock.NewModule(app.Db), wuid.NewModule(app.Db)}
	_, err = runModuleMigrations(t.Context(), app.Db.Write(), app.mods, MigrationConfig{Action: MigrationUp})
	require.NoError(t, err)
	app.mods = append(app.mods, migrationStatusModule(app))
	router, api := moduleRouter(t, app)

	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, migrationstatus.Path, nil))
	require.Equal(t, http.StatusOK, jsonResponse.Code)
	assert.Contains(t, jsonResponse.Body.String(), `"state":"clean"`)
	assert.Contains(t, jsonResponse.Body.String(), `"module":"dlock"`)

	htmlRequest := httptest.NewRequest(http.MethodGet, migrationstatus.Path, nil)
	htmlRequest.Header.Set("Accept", "text/html")
	htmlResponse := httptest.NewRecorder()
	router.ServeHTTP(htmlResponse, htmlRequest)
	require.Equal(t, http.StatusOK, htmlResponse.Code)
	assert.Equal(t, "application/json", htmlResponse.Header().Get("Content-Type"))
	assert.Contains(t, htmlResponse.Body.String(), `"state":"clean"`)
	assert.Contains(t, htmlResponse.Body.String(), "dlock")
	assert.Contains(t, api.OpenAPI().Paths, migrationstatus.Path)
}

func TestMigrationStatusReportsUnavailableDatabase(t *testing.T) {
	app := &App{config: validTestAppConfig()}
	app.mods = append(app.mods, migrationStatusModule(app))
	router, _ := moduleRouter(t, app)

	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, httptest.NewRequest(http.MethodGet, migrationstatus.Path, nil))
	assert.Equal(t, http.StatusServiceUnavailable, jsonResponse.Code)
	assert.Contains(t, jsonResponse.Body.String(), `"code":503`)
	assert.NotContains(t, jsonResponse.Body.String(), "writable database")

	htmlRequest := httptest.NewRequest(http.MethodGet, migrationstatus.Path, nil)
	htmlRequest.Header.Set("Accept", "text/html")
	htmlResponse := httptest.NewRecorder()
	router.ServeHTTP(htmlResponse, htmlRequest)
	assert.Equal(t, http.StatusServiceUnavailable, htmlResponse.Code)
	assert.Equal(t, "application/json", htmlResponse.Header().Get("Content-Type"))
	assert.NotContains(t, htmlResponse.Body.String(), "writable database")
}

func TestMetricsRegistrationAndDisabledModuleRoutes(t *testing.T) {
	enabled := &App{config: validTestAppConfig()}
	enabled.metrics = metrics.NewModule(nil)
	enabled.mods = append(enabled.mods, enabled.metrics)
	router, api := moduleRouter(t, enabled)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, metrics.Path, nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "go_goroutines")
	assert.Contains(t, api.OpenAPI().Paths, metrics.Path)

	disabled := &App{config: validTestAppConfig()}
	disabledRouter, disabledAPI := moduleRouter(t, disabled)
	for _, path := range []string{metrics.Path, pprof.Path, migrationstatus.Path, configinspector.Path} {
		response := httptest.NewRecorder()
		disabledRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, response.Code, path)
		assert.NotContains(t, disabledAPI.OpenAPI().Paths, path)
	}
}

func TestLandingResourcesRequireEnabledAndAdvertised(t *testing.T) {
	settings := API{
		Metrics:         APIMetrics{Enabled: true, Advertise: true},
		MigrationStatus: APIMigrationStatus{Enabled: true, Advertise: true},
		ConfigInspector: APIConfigInspector{Enabled: true, Advertise: true},
	}
	resources := landingResources(settings)
	require.Len(t, resources, 3)
	assert.Equal(t, metrics.Path, resources[0].Href)
	assert.Equal(t, migrationstatus.Path, resources[1].Href)
	assert.Equal(t, configinspector.Path, resources[2].Href)

	settings.Metrics.Enabled = false
	settings.MigrationStatus.Advertise = false
	settings.ConfigInspector.Enabled = false
	assert.Empty(t, landingResources(settings))
}

func migrationStatusModule(app *App) *migrationstatus.Module {
	return migrationstatus.NewModule(func(ctx context.Context) ([]migrationstatus.Report, error) {
		reports, err := runModuleMigrations(ctx, app.Db.Write(), app.mods, MigrationConfig{
			Action: MigrationStatus,
			To:     -1,
		})
		result := make([]migrationstatus.Report, len(reports))
		for index, report := range reports {
			result[index] = migrationstatus.Report{Module: report.Module, Status: report.Status}
		}
		return result, err
	})
}
