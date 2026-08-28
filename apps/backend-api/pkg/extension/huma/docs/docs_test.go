package docs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultOptions() Options {
	return Options{
		Name:             "test service",
		LandingEnabled:   true,
		DocsEnabled:      true,
		AdvertiseDocs:    true,
		AdvertiseOpenAPI: true,
	}
}

func newDocsRouter(custom ...Options) http.Handler {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Frame-Options", "DENY")
			next.ServeHTTP(response, request)
		})
	})
	config := huma.DefaultConfig("test", "1.0.0")
	config.OpenAPIPath = "/schema"
	options := defaultOptions()
	if len(custom) > 0 {
		options = custom[0]
	}
	Register(router, config, options)
	router.Get("/api-test", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	return router
}

func TestGeneratedRendererPages(t *testing.T) {
	handler := newDocsRouter()
	paths := map[string]string{
		"/docs":              "swagger",
		"/docs/":             "swagger",
		"/docs/scalar":       "scalar",
		"/docs/swagger":      "swagger",
		"/docs/redoc":        "redoc",
		"/docs/rapidoc":      "rapidoc",
		"/docs/stoplight":    "stoplight",
		"/docs/openapi-ui":   "openapi-ui",
		"/docs/openapi-json": "openapi-json",
		"/docs/openapi-yaml": "openapi-yaml",
	}

	for route, renderer := range paths {
		t.Run(route, func(t *testing.T) {
			response := request(t, handler, route)
			body := response.Body.String()
			assert.Contains(t, body, `data-renderer="`+renderer+`"`)
			assert.Contains(t, body, `/docs/config.js`)
			assert.NotContains(t, body, `/grpcui/`)
			assert.NotContains(t, body, "https://")
			assert.Equal(t, "DENY", response.Header().Get("X-Frame-Options"))
			assert.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
		})
	}
}

func TestRendererTabsFollowGitHubStarOrder(t *testing.T) {
	body := request(t, newDocsRouter(), "/docs/").Body.String()
	ordered := []string{"Swagger UI", "ReDoc", "Scalar", "Stoplight", "RapiDoc", "openapi-ui"}
	previous := -1
	for _, label := range ordered {
		current := strings.Index(body, ">"+label+"<")
		require.Greater(t, current, previous, label)
		previous = current
	}
}

func TestSpecViewerPagesOfferPrettyViewAndDownload(t *testing.T) {
	handler := newDocsRouter()
	for _, route := range []string{"/docs/openapi-json", "/docs/openapi-yaml"} {
		body := request(t, handler, route).Body.String()
		assert.Contains(t, body, `/docs/assets/app.js`)
		assert.Contains(t, body, `aria-current="page"`)
	}
	app := request(t, handler, "/docs/assets/app.js").Body.String()
	assert.Contains(t, app, `Expand all`)
	assert.Contains(t, app, `Collapse all`)
	assert.Contains(t, app, `Download`)
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

func TestRootLandingAndOrdinaryRouteProtection(t *testing.T) {
	handler := newDocsRouter()
	root := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	handler.ServeHTTP(root, request)
	assert.Equal(t, http.StatusOK, root.Code)
	assert.Equal(t, "text/html; charset=utf-8", root.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", root.Header().Get("Cache-Control"))
	assert.Contains(t, root.Body.String(), "test service API")
	assert.Contains(t, root.Body.String(), `href="/docs"`)
	assert.Contains(t, root.Body.String(), `href="/schema.json"`)
	assert.NotContains(t, root.Body.String(), `href="/livez"`)
	assert.NotContains(t, root.Body.String(), `href="/healthz"`)
	assert.NotContains(t, root.Body.String(), `href="/readyz"`)
	assert.NotContains(t, root.Body.String(), "https://")

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api-test", nil))
	assert.Equal(t, http.StatusNoContent, apiResponse.Code)
	assert.Equal(t, "DENY", apiResponse.Header().Get("X-Frame-Options"))
}

func TestRootReturnsEnvelopeForAPIClients(t *testing.T) {
	handler := newDocsRouter()
	for _, accept := range []string{"", "*/*", "application/json"} {
		t.Run(accept, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", accept)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assert.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
			var envelope struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Meta    struct {
					RequestAt string  `json:"request_at"`
					ElapsedMS float64 `json:"elapsed_ms"`
				} `json:"meta"`
				Data landingData `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			assert.Zero(t, envelope.Code)
			assert.Equal(t, "Success", envelope.Message)
			assert.NotEmpty(t, envelope.Meta.RequestAt)
			assert.GreaterOrEqual(t, envelope.Meta.ElapsedMS, 0.0)
			assert.Equal(t, landingData{
				Name:   "test service",
				Status: "available",
				Resources: []landingResource{
					{ID: "docs", Title: "API reference", Description: "Interactive documentation for registered endpoints", Href: "/docs"},
					{ID: "openapi", Title: "OpenAPI specification", Description: "Machine-readable API contract", Href: "/schema.json"},
				},
			}, envelope.Data)
		})
	}
}

func TestRegistrationAndAdvertisementAreIndependent(t *testing.T) {
	options := defaultOptions()
	options.AdvertiseDocs = false
	options.AdvertiseOpenAPI = false
	options.AdvertiseHealth = true
	handler := newDocsRouter(options)

	root := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	handler.ServeHTTP(root, request)
	require.Equal(t, http.StatusOK, root.Code)
	assert.NotContains(t, root.Body.String(), `href="/docs"`)
	assert.NotContains(t, root.Body.String(), `href="/schema.json"`)
	assert.Contains(t, root.Body.String(), `href="/livez"`)
	assert.Contains(t, root.Body.String(), `href="/healthz"`)
	assert.Contains(t, root.Body.String(), `href="/readyz"`)

	assert.Equal(t, http.StatusOK, requestStatus(handler, "/docs"))
	assert.Equal(t, http.StatusOK, requestStatus(handler, "/docs/config.js"))
}

func TestDisabledLandingAndDocsAreNotMounted(t *testing.T) {
	options := defaultOptions()
	options.LandingEnabled = false
	options.DocsEnabled = false
	handler := newDocsRouter(options)

	assert.Equal(t, http.StatusNotFound, requestStatus(handler, "/"))
	assert.Equal(t, http.StatusNotFound, requestStatus(handler, "/docs"))
	assert.Equal(t, http.StatusNotFound, requestStatus(handler, "/docs/config.js"))
	assert.Equal(t, http.StatusNoContent, requestStatus(handler, "/api-test"))
}

func TestLandingWithoutAdvertisedResourcesOmitsNavigation(t *testing.T) {
	options := defaultOptions()
	options.DocsEnabled = false
	options.AdvertiseOpenAPI = false
	handler := newDocsRouter(options)

	root := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	handler.ServeHTTP(root, request)
	require.Equal(t, http.StatusOK, root.Code)
	assert.NotContains(t, root.Body.String(), "<nav")
	assert.Contains(t, root.Body.String(), "API endpoint available")
}

func TestRootContentNegotiation(t *testing.T) {
	tests := map[string]bool{
		"text/html": true,
		"text/html,application/xhtml+xml,*/*;q=0.8":        true,
		"application/json,text/html;q=0.5":                 false,
		"application/problem+json":                         false,
		"text/html;q=0":                                    false,
		"application/json;q=0.5,text/html;q=0.8":           true,
		"application/json;q=0.8,application/xhtml+xml;q=1": true,
	}
	for accept, expected := range tests {
		t.Run(accept, func(t *testing.T) {
			assert.Equal(t, expected, prefersHTML(accept))
		})
	}
}

func TestLandingEscapesAndTrimsServiceName(t *testing.T) {
	escaped := string(indexHTML(landingData{Name: `<script>alert("x")</script>`}))
	assert.NotContains(t, escaped, "<script>")
	assert.Contains(t, escaped, "&lt;script&gt;")

	trimmed := string(indexHTML(landingData{Name: "  test service  "}))
	assert.Contains(t, trimmed, "<title>test service API</title>")

	empty := string(indexHTML(landingData{Name: "  "}))
	assert.Contains(t, empty, "<title> API</title>")
}

func request(t *testing.T, handler http.Handler, route string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route, nil))
	require.Equal(t, http.StatusOK, recorder.Code, strings.TrimSpace(recorder.Body.String()))
	return recorder
}

func requestStatus(handler http.Handler, route string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, route, nil))
	return recorder.Code
}
