package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	audit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/configinspector"
	"7link.com/platform/internal/infra/databasemonitor"
	"7link.com/platform/internal/infra/dlock"
	metrics "7link.com/platform/internal/infra/metrics"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/pprof"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/pkg/appenv"
	configx "7link.com/platform/pkg/config"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type registrarModule struct {
	httpErr       error
	httpCalled    bool
	grpcCalled    bool
	registeredCtx context.Context
}

func (*registrarModule) ModuleName() string { return "registrar" }

func (m *registrarModule) RegisterHTTP(ctx context.Context, _ huma.API) error {
	m.httpCalled = true
	m.registeredCtx = ctx
	return m.httpErr
}

func (m *registrarModule) RegisterGRPC(grpc.ServiceRegistrar) { m.grpcCalled = true }

func TestStartHTTPServerReportsRegistrationAndListenErrors(t *testing.T) {
	registrationErr := errors.New("registration failed")
	app := &App{
		config:  validTestAppConfig(),
		ctx:     t.Context(),
		modules: wiredRegistry(t, &registrarModule{httpErr: registrationErr}),
	}
	assert.ErrorIs(t, app.startHTTPServer(), registrationErr)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	app.modules = wiredRegistry(t)
	app.config.HttpServer.Port = listener.Addr().(*net.TCPAddr).Port
	app.serveErr = make(chan error, 1)
	assert.ErrorContains(t, app.startHTTPServer(), "http listen")
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

// wiredRegistry builds a Registry in the Started state so HTTP/gRPC registration
// is legal.
func wiredRegistry(t *testing.T, mods ...module.Module) *module.Registry {
	t.Helper()
	registry := module.NewRegistry()
	require.NoError(t, registry.Register(mods...))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(func(*module.Services) error { return nil }))
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	return registry
}

func moduleRouter(t *testing.T, app *App) (*chi.Mux, huma.API) {
	t.Helper()
	router := chi.NewRouter()
	require.NoError(t, app.mountHTTPMiddlewares(router))
	api := humax.New(router, humax.Config{
		Name:    app.config.Name,
		Version: app.config.Version,
		Landing: humax.LandingConfig{Enabled: false},
		Docs:    humax.EndpointConfig{Enabled: false},
		OpenAPI: humax.EndpointConfig{Enabled: true},
	})
	if app.modules != nil {
		require.NoError(t, app.modules.RegisterHTTP(t.Context(), api))
		for _, route := range app.modules.RawHTTPRoutes() {
			mountRawRoute(router, route)
		}
	}
	return router, api
}

type rawRouteModule struct{}

func (*rawRouteModule) ModuleName() string { return "raw-route" }

func (*rawRouteModule) RawHTTPRoutes() []module.RawRoute {
	return []module.RawRoute{{
		Method: http.MethodGet,
		Path:   "/widgets/{id}",
		Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
			_, _ = response.Write([]byte("created"))
		}),
	}}
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
	rt := appenv.NewRuntime(appenv.NewIdentity(app.config.Name, app.config.Version))
	inspector := configinspector.NewModule(rt, app.config, configInspectorConfig(app.config))
	app.modules = wiredRegistry(t, inspector)
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
	app.runtime = appenv.NewRuntime(
		appenv.NewIdentity(app.config.Name, app.config.Version),
		appenv.WithDatabase(routerDB),
	)

	rt := app.runtime
	app.modules = module.NewRegistry()
	require.NoError(t, app.modules.Register(
		dlock.NewModule(rt),
		wuid.NewModule(rt),
		migrationstatus.NewModule(rt, migrationStatusConfig(app.config)),
	))
	require.NoError(t, app.prepareModules())
	_, err = app.migrateModules(t.Context(), MigrationRequest{Action: MigrationUp})
	require.NoError(t, err)
	require.NoError(t, app.modules.Initialize(t.Context()))
	require.NoError(t, app.modules.Start(t.Context()))
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
	app.config.API.MigrationStatus.Enabled = true
	app.runtime = appenv.NewRuntime(appenv.NewIdentity(app.config.Name, app.config.Version))
	app.modules = module.NewRegistry()
	require.NoError(t, app.modules.Register(migrationstatus.NewModule(app.runtime, migrationStatusConfig(app.config))))
	require.NoError(t, app.modules.Activate())
	require.NoError(t, app.modules.Wire(func(services *module.Services) error {
		return module.Provide[migrationstatus.Inspector](services, app.migrationInspector)
	}))
	require.NoError(t, app.modules.Initialize(t.Context()))
	require.NoError(t, app.modules.Start(t.Context()))
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
	enabled.config.API.Metrics.Enabled = true
	metricsModule := metrics.NewModule(appenv.NewRuntime(appenv.NewIdentity("", "")), metricsConfig(enabled.config))
	enabled.modules = wiredRegistry(t, metricsModule, &rawRouteModule{})
	router, api := moduleRouter(t, enabled)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/widgets/123?token=secret", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing/456", nil))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, metrics.Path, nil))
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "go_goroutines")
	assert.Contains(t, response.Body.String(), `http_server_requests_total{method="GET",route="/widgets/{id}",status="201"} 1`)
	assert.Contains(t, response.Body.String(), `http_server_requests_total{method="GET",route="unmatched",status="404"} 1`)
	assert.NotContains(t, response.Body.String(), "/widgets/123")
	assert.NotContains(t, response.Body.String(), "/missing/456")
	assert.NotContains(t, response.Body.String(), "secret")
	assert.Contains(t, api.OpenAPI().Paths, metrics.Path)

	disabled := &App{config: validTestAppConfig()}
	disabled.modules = wiredRegistry(t)
	disabledRouter, disabledAPI := moduleRouter(t, disabled)
	for _, path := range []string{metrics.Path, pprof.Path, migrationstatus.Path, configinspector.Path, databasemonitor.Path} {
		response := httptest.NewRecorder()
		disabledRouter.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, response.Code, path)
		assert.NotContains(t, disabledAPI.OpenAPI().Paths, path)
	}
}

func TestMountRawRouteSupportsSpecificAndAllMethods(t *testing.T) {
	router := chi.NewRouter()
	mountRawRoute(router, module.RawRoute{
		Method: http.MethodGet,
		Path:   "/specific",
		Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
	})
	mountRawRoute(router, module.RawRoute{
		Path: "/all",
		Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
		}),
	})

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/specific", nil))
	assert.Equal(t, http.StatusNoContent, get.Code)
	postSpecific := httptest.NewRecorder()
	router.ServeHTTP(postSpecific, httptest.NewRequest(http.MethodPost, "/specific", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, postSpecific.Code)
	postAll := httptest.NewRecorder()
	router.ServeHTTP(postAll, httptest.NewRequest(http.MethodPost, "/all", nil))
	assert.Equal(t, http.StatusAccepted, postAll.Code)
}

func TestMountHTTPMiddlewaresRejectsUnwiredRegistry(t *testing.T) {
	app := &App{modules: module.NewRegistry()}
	err := app.mountHTTPMiddlewares(chi.NewRouter())
	assert.ErrorContains(t, err, "resolve HTTP metrics observer")
}

func TestLandingResourcesRequireEnabledAndAdvertised(t *testing.T) {
	settings := API{
		Metrics:             APIMetrics{Enabled: true, Advertise: true},
		MigrationStatus:     APIMigrationStatus{Enabled: true, Advertise: true},
		ConfigInspector:     APIConfigInspector{Enabled: true, Advertise: true},
		DatasourceInspector: APIDatasourceInspector{Enabled: true, Advertise: true},
	}
	resources := landingResources(settings)
	require.Len(t, resources, 4)
	assert.Equal(t, metrics.Path, resources[0].Href)
	assert.Equal(t, migrationstatus.Path, resources[1].Href)
	assert.Equal(t, configinspector.Path, resources[2].Href)
	assert.Equal(t, databasemonitor.ConsolePath, resources[3].Href)

	settings.Metrics.Enabled = false
	settings.MigrationStatus.Advertise = false
	settings.ConfigInspector.Enabled = false
	settings.DatasourceInspector.Advertise = false
	assert.Empty(t, landingResources(settings))
}

// capturingAuditSink collects audit events for assertions without touching the
// process log or the database.
type capturingAuditSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (sink *capturingAuditSink) Write(_ context.Context, events []audit.Event) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, events...)
	return nil
}

func (sink *capturingAuditSink) snapshot() []audit.Event {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]audit.Event(nil), sink.events...)
}

func TestHTTPServerMountsAuditMiddleware(t *testing.T) {
	sink := &capturingAuditSink{}
	app := &App{config: validTestAppConfig(), ctx: t.Context()}
	auditModule := audit.NewModule(appenv.NewRuntime(appenv.NewIdentity(app.config.Name, app.config.Version)),
		audit.Config{Enabled: true, CaptureBodies: true, QueueSize: 1024, BatchSize: 1, FlushInterval: time.Millisecond, MaxBodyBytes: 1024},
		audit.WithSinks(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, auditModule.Stop(stopCtx))
	})

	// Mirror the middleware chain startHTTPServer builds.
	router := chi.NewRouter()
	router.Use(auditModule.HTTPMiddleware)
	router.Post("/orders", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":null}`))
	})

	writeRequest := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"name":"a"}`))
	writeRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, writeRequest)
	require.Equal(t, http.StatusOK, response.Code)

	// A read request is not audited by default.
	readResponse := httptest.NewRecorder()
	router.ServeHTTP(readResponse, httptest.NewRequest(http.MethodGet, "/orders", nil))

	require.Eventually(t, func() bool { return len(sink.snapshot()) == 1 }, time.Second, 10*time.Millisecond)
	events := sink.snapshot()
	assert.Equal(t, "POST /orders", events[0].Operation)
	assert.Equal(t, audit.AccessWrite, events[0].Access)
	assert.Equal(t, `{"name":"a"}`, events[0].Request.Body)
}

func TestHTTPServerDoesNotAuditSkippedRoutes(t *testing.T) {
	sink := &capturingAuditSink{}
	app := &App{config: validTestAppConfig(), ctx: t.Context()}
	auditModule := audit.NewModule(appenv.NewRuntime(appenv.NewIdentity(app.config.Name, app.config.Version)),
		audit.Config{Enabled: true, QueueSize: 1024, BatchSize: 1, FlushInterval: time.Millisecond, MaxBodyBytes: 1024},
		audit.WithSinks(sink))
	require.NoError(t, auditModule.Start(t.Context()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, auditModule.Stop(stopCtx))
	})

	router := chi.NewRouter()
	router.Use(auditModule.HTTPMiddleware)
	router.Post("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/healthz", nil))
	// Give the pipeline a moment; no event should appear for the skipped route.
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, sink.snapshot())
}
