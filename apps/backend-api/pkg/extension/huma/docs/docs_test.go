package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDocsRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Frame-Options", "DENY")
			next.ServeHTTP(response, request)
		})
	})
	config := huma.DefaultConfig("test", "1.0.0")
	config.OpenAPIPath = "/schema"
	Register(router, config, "test service")
	router.Get("/api-test", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	return router
}

func TestGeneratedRendererPages(t *testing.T) {
	handler := newDocsRouter()
	paths := map[string]string{
		"/docs":            "scalar",
		"/docs/":           "scalar",
		"/docs/scalar":     "scalar",
		"/docs/swagger":    "swagger",
		"/docs/redoc":      "redoc",
		"/docs/rapidoc":    "rapidoc",
		"/docs/stoplight":  "stoplight",
		"/docs/openapi-ui": "openapi-ui",
	}

	for route, renderer := range paths {
		t.Run(route, func(t *testing.T) {
			response := request(t, handler, route)
			body := response.Body.String()
			assert.Contains(t, body, `data-renderer="`+renderer+`"`)
			assert.Contains(t, body, `/docs/config.js`)
			assert.Contains(t, body, `/grpcui/`)
			assert.NotContains(t, body, "https://")
			assert.Equal(t, "DENY", response.Header().Get("X-Frame-Options"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
		})
	}
}

func TestRuntimeConfigUsesApplicationValues(t *testing.T) {
	response := request(t, newDocsRouter(), "/docs/config.js")
	actual := strings.TrimSuffix(strings.TrimPrefix(response.Body.String(), "window.__DOCS_CONFIG__ = "), ";\n")
	assert.JSONEq(t, `{
		"name": "test service",
		"specJSON": "/schema.json",
		"specYAML": "/schema.yaml"
	}`, actual)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestEmbeddedAssetsAreServedLocally(t *testing.T) {
	handler := newDocsRouter()
	assets := []string{
		"/docs/assets/app.css",
		"/docs/assets/app.js",
		"/docs/assets/scalar.js",
		"/docs/assets/swagger-ui.css",
		"/docs/assets/swagger-ui-bundle.js",
		"/docs/assets/redoc.js",
		"/docs/assets/rapidoc.js",
		"/docs/assets/stoplight.css",
		"/docs/assets/stoplight.js",
		"/docs/assets/mermaid.min.js",
		"/docs/assets/openapi-ui.js",
	}

	for _, route := range assets {
		t.Run(route, func(t *testing.T) {
			response := request(t, handler, route)
			assert.Greater(t, response.Body.Len(), 16)
			assert.Equal(t, "public, max-age=3600, must-revalidate", response.Header().Get("Cache-Control"))
		})
	}
}

func TestStoplightUsesEmbeddedMermaid(t *testing.T) {
	body := request(t, newDocsRouter(), "/docs/assets/stoplight.js").Body.String()
	assert.Contains(t, body, "/docs/assets/mermaid.min.js")
	assert.NotContains(t, body, "https://unpkg.com/mermaid")
}

func TestRootRedirectAndOrdinaryRouteProtection(t *testing.T) {
	handler := newDocsRouter()
	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusTemporaryRedirect, root.Code)
	assert.Equal(t, "/docs/", root.Header().Get("Location"))

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api-test", nil))
	assert.Equal(t, http.StatusNoContent, apiResponse.Code)
	assert.Equal(t, "DENY", apiResponse.Header().Get("X-Frame-Options"))
}

func request(t *testing.T, handler http.Handler, route string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route, nil))
	require.Equal(t, http.StatusOK, recorder.Code, strings.TrimSpace(recorder.Body.String()))
	return recorder
}
