package migrationstatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleReturnsJSONAndRegistersOpenAPI(t *testing.T) {
	reports := []Report{{Module: "users", Status: goosex.Status{State: goosex.StatePending}}}
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	module := NewModule(func(context.Context) ([]Report, error) { return reports, nil })
	assert.Equal(t, "migration-status", module.ModuleName())
	require.NoError(t, module.RegisterHTTP(t.Context(), router, api))

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
}

func TestModuleHidesInspectionErrors(t *testing.T) {
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	module := NewModule(func(context.Context) ([]Report, error) {
		return nil, errors.New("database secret")
	})
	require.NoError(t, module.RegisterHTTP(t.Context(), router, api))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, Path, nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "database secret")
	assert.EqualError(t, NewModule(nil).RegisterHTTP(t.Context(), chi.NewRouter(), nil),
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
