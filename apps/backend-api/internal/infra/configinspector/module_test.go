package configinspector

import (
	"net/http"
	"net/http/httptest"
	"testing"

	configx "7link.com/platform/pkg/config"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleRedactsConfigurationAndRegistersOpenAPI(t *testing.T) {
	type sample struct {
		Name   string `mapstructure:"name"`
		Secret string `mapstructure:"secret" secret:"true"`
	}
	m := NewModule(sample{Name: "api", Secret: "private"}, Config{})
	require.NoError(t, m.Initialize(t.Context()))
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	assert.Equal(t, "config-inspector", m.ModuleName())
	require.NoError(t, m.RegisterHTTP(t.Context(), router, api))

	request := httptest.NewRequest(http.MethodGet, Path, nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), configx.RedactedValue)
	assert.NotContains(t, response.Body.String(), "private")
	assert.Contains(t, api.OpenAPI().Paths, Path)
	assert.Equal(t, []string{"Diagnostics"}, api.OpenAPI().Paths[Path].Get.Tags)
}

func TestNewModuleRejectsUnsupportedConfiguration(t *testing.T) {
	m := NewModule(nil, Config{})
	err := m.Initialize(t.Context())
	assert.ErrorContains(t, err, "create config inspector")
}
