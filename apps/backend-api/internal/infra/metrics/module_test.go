package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPMiddlewareExportsRouteMetricsWithoutRawURLs(t *testing.T) {
	module := NewModule()
	assert.Equal(t, "metrics", module.ModuleName())
	router := chi.NewRouter()
	router.Use(module.HTTPMiddleware)
	api := humax.New(router, humax.Config{
		Name: "test", Landing: humax.LandingConfig{Enabled: false},
		Docs: humax.EndpointConfig{Enabled: false}, OpenAPI: humax.EndpointConfig{Enabled: true},
	})
	router.Get("/widgets/{id}", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte("created"))
	})
	require.NoError(t, module.RegisterHTTP(t.Context(), router, api))

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/widgets/123?token=secret", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/missing/456", nil))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
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
	assert.False(t, strings.Contains(text, "123"))
	assert.False(t, strings.Contains(text, "secret"))
	assert.Contains(t, api.OpenAPI().Paths, Path)
}
