package migrationstatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleReturnsJSONAndRegistersOpenAPI(t *testing.T) {
	reports := []Report{{Module: "users", Status: goosex.Status{State: goosex.StatePending}}}
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), Config{})
	services := module.NewServices()
	require.NoError(t, module.Provide[Inspector](services, func(context.Context) ([]Report, error) { return reports, nil }))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	assert.Equal(t, "migration-status", m.ModuleName())
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, route := range m.RawHTTPRoutes() {
		router.Method(route.Method, route.Path, route.Handler)
	}

	for _, accept := range []string{"application/json", "text/html"} {
		request := httptest.NewRequest(http.MethodGet, Path, nil)
		request.Header.Set("Accept", accept)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
		assert.Contains(t, response.Body.String(), `"state":"pending"`)
	}
	assert.Contains(t, api.OpenAPI().Paths, Path)
	assert.Equal(t, []string{"Diagnostics"}, api.OpenAPI().Paths[Path].Get.Tags)
}

func TestModuleHidesInspectionErrors(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	m := NewModule(rt, Config{})
	services := module.NewServices()
	require.NoError(t, module.Provide[Inspector](services, func(context.Context) ([]Report, error) {
		return nil, errors.New("database secret")
	}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, route := range m.RawHTTPRoutes() {
		router.Method(route.Method, route.Path, route.Handler)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "database secret")
	assert.EqualError(t, NewModule(rt, Config{}).RegisterHTTP(t.Context(), nil),
		"register migration status: inspector is unavailable")
}

func TestOverallStateUsesMostSevereState(t *testing.T) {
	reports := []Report{
		{Status: goosex.Status{State: goosex.StateClean}},
		{Status: goosex.Status{State: goosex.StateAhead}},
		{Status: goosex.Status{State: goosex.StateDiverged}},
	}
	assert.Equal(t, goosex.StateDiverged, overallState(reports))
	assert.Equal(t, goosex.StateClean, overallState(nil))
}
