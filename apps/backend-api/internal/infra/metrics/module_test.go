package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/infra/databasemonitor"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPObserverExportsRouteMetricsWithoutRawURLs(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("", "")), Config{})
	assert.Equal(t, "metrics", m.ModuleName())
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{
		Name: "test", Landing: humax.LandingConfig{Enabled: false},
		Docs: humax.EndpointConfig{Enabled: false}, OpenAPI: humax.EndpointConfig{Enabled: true},
	})
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	routes := m.RawHTTPRoutes()
	require.Len(t, routes, 1)
	assert.Equal(t, http.MethodGet, routes[0].Method)
	assert.Equal(t, Path, routes[0].Path)
	m.ObserveHTTP(http.MethodGet, "/widgets/{id}", http.StatusCreated, 2*time.Millisecond, 7)
	m.ObserveHTTP(http.MethodGet, "", http.StatusNotFound, time.Millisecond, 0)

	response := httptest.NewRecorder()
	routes[0].Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))
	result := response.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	text := string(body)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, text, `http_server_requests_total{method="GET",route="/widgets/{id}",status="201"} 1`)
	assert.Contains(t, text, `http_server_requests_total{method="GET",route="unmatched",status="404"} 1`)
	assert.Contains(t, text, "http_server_request_duration_seconds")
	assert.Contains(t, text, "http_server_response_size_bytes")
	assert.Contains(t, text, "go_goroutines")
	assert.Contains(t, text, "process_cpu_seconds_total")
	assert.NotContains(t, text, "/widgets/123")
	assert.NotContains(t, text, "/missing/456")
	assert.NotContains(t, text, "secret")
	assert.Contains(t, api.OpenAPI().Paths, Path)
	assert.Equal(t, []string{"system"}, api.OpenAPI().Paths[Path].Get.Tags)
}

func TestDatasourceMetricsIncludeNamedPoolState(t *testing.T) {
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "primary.sqlite"),
			Writable: true, Readable: true, MaxOpenConns: 7,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })
	assert.NoError(t, router.Probe(t.Context()).RoutingError())
	runtime := appenv.NewRuntime(appenv.NewIdentity("", ""), appenv.WithDatabase(router))
	monitor := databasemonitor.NewModule(runtime, databasemonitor.Config{
		Interval: time.Hour, Timeout: time.Second, StaleAfter: 2 * time.Hour,
	})
	require.NoError(t, router.RegisterQueryObserver(monitor))
	require.NoError(t, monitor.Start(t.Context()))
	require.NoError(t, router.Write().Exec("SELECT 1").Error)
	t.Cleanup(func() { require.NoError(t, monitor.Stop(context.Background())) })

	m := NewModule(runtime, Config{})
	services := module.NewServices()
	require.NoError(t, module.Provide[databasemonitor.Monitor](services, monitor))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))

	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))
	body := response.Body.String()
	labels := `datasource="primary",readable="true",writable="true"`
	assert.Contains(t, body, `database_datasource_up{`+labels+`} 1`)
	assert.Contains(t, body, `database_pool_max_open_connections{`+labels+`} 7`)
	assert.Contains(t, body, `database_pool_open_connections{`+labels+`}`)
	assert.Contains(t, body, `database_pool_wait_count_total{`+labels+`}`)
	assert.Contains(t, body, `database_probe_duration_seconds{`+labels+`}`)
	assert.Contains(t, body, `database_last_probe_timestamp_seconds{`+labels+`}`)
	assert.Contains(t, body, `database_last_success_timestamp_seconds{`+labels+`}`)
	assert.Contains(t, body, `database_consecutive_probe_failures{`+labels+`} 0`)
	assert.Contains(t, body, `database_pool_max_idle_closed_total{`+labels+`}`)
	assert.Contains(t, body, `database_pool_max_idle_time_closed_total{`+labels+`}`)
	assert.Contains(t, body, `database_pool_max_lifetime_closed_total{`+labels+`}`)
	assert.Contains(t, body, `database_sql_executions_total{datasource="primary",operation="SELECT"} 1`)
	assert.Contains(t, body, `database_sql_successes_total{datasource="primary",operation="SELECT"} 1`)
	assert.Contains(t, body, `database_sql_errors_total{datasource="primary",operation="SELECT"} 0`)
	assert.Contains(t, body, `database_sql_duration_seconds_total{datasource="primary",operation="SELECT"}`)
	assert.Contains(t, body, `database_sql_duration_seconds_bucket{datasource="primary",operation="SELECT",le="0.001"}`)
	assert.Contains(t, body, `database_sql_duration_seconds_count{datasource="primary",operation="SELECT"} 1`)
	assert.Contains(t, body, `database_sql_min_duration_seconds{datasource="primary",operation="SELECT"}`)
	assert.Contains(t, body, `database_sql_max_duration_seconds{datasource="primary",operation="SELECT"}`)
	assert.Contains(t, body, `database_sql_running{datasource="primary",operation="SELECT"} 0`)
	assert.Contains(t, body, `database_sql_concurrent_max{datasource="primary",operation="SELECT"} 1`)
	assert.Contains(t, body, `database_sql_rows_total{datasource="primary",operation="SELECT"}`)
	assert.Contains(t, body, `database_sql_max_rows{datasource="primary",operation="SELECT"}`)
}

func TestModuleConfigurationAndCollectorRegistration(t *testing.T) {
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), Config{
		Config: module.Config{Enabled: true},
	})
	assert.True(t, m.Enabled())
	require.NoError(t, m.RegisterCollector(nil))

	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "module_registration_test_total"})
	require.NoError(t, m.RegisterCollector(counter))
	require.NoError(t, m.RegisterCollector(counter), "duplicate collector registration is idempotent")

	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	observer, err := module.Resolve[HTTPObserver](services)
	require.NoError(t, err)
	assert.Same(t, m, observer)

	emptyServices := module.NewServices()
	require.NoError(t, emptyServices.Seal())
	require.NoError(t, m.InjectServices(emptyServices), "the optional database monitor may be absent")

	var nilModule *Module
	require.NoError(t, nilModule.RegisterCollector(counter))
}
