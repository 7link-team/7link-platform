package humax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestOperationsGroupedByFinalPathPrefix(t *testing.T) {
	api := New(chi.NewRouter(), DefaultConfig("test", "dev"))
	for _, path := range []string{"/tenants/{tenant_id}/files", "/principals/{principal_id}/files", "/files/{file_id}", "/oauth/token", "/.well-known/openid-configuration"} {
		huma.Register(api, huma.Operation{Method: http.MethodGet, Path: path, OperationID: path, Tags: []string{"wrong-suffix-group"}}, func(context.Context, *struct{}) (*struct{}, error) {
			return &struct{}{}, nil
		})
		assert.Equal(t, []string{strings.Split(path, "/")[1]}, api.OpenAPI().Paths[path].Get.Tags)
	}
}

func TestNewConfiguresAPIAndSharedMiddleware(t *testing.T) {
	router := chi.NewRouter()
	api := New(router, DefaultConfig("orders", "2.3.4"))

	assert.Equal(t, "orders API", api.OpenAPI().Info.Title)
	assert.Equal(t, "2.3.4", api.OpenAPI().Info.Version)
	assert.Equal(t, "3.1.0", api.OpenAPI().OpenAPI)

	var locale string
	router.Get("/probe", func(response http.ResponseWriter, request *http.Request) {
		locale = i18n.LocaleFromContext(request.Context())
		response.WriteHeader(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/probe?lang=zh", nil))

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "zh-CN", locale)
	assert.Equal(t, "zh-CN", response.Header().Get("Content-Language"))

	spec := httptest.NewRecorder()
	router.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, OpenAPIPath+".json", nil))
	assert.Equal(t, http.StatusOK, spec.Code)
}

func TestNewTrimsServiceName(t *testing.T) {
	api := New(chi.NewRouter(), DefaultConfig("  orders  ", "dev"))
	assert.Equal(t, "orders API", api.OpenAPI().Info.Title)
}

func TestNewControlsLandingDocsAndOpenAPIRoutes(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*Config)
		rootStatus int
		docsStatus int
		specStatus int
	}{
		{
			name: "landing disabled",
			configure: func(config *Config) {
				config.Landing.Enabled = false
			},
			rootStatus: http.StatusNotFound,
			docsStatus: http.StatusOK,
			specStatus: http.StatusOK,
		},
		{
			name: "docs disabled",
			configure: func(config *Config) {
				config.Docs.Enabled = false
			},
			rootStatus: http.StatusOK,
			docsStatus: http.StatusNotFound,
			specStatus: http.StatusOK,
		},
		{
			name: "openapi disabled",
			configure: func(config *Config) {
				config.OpenAPI.Enabled = false
			},
			rootStatus: http.StatusOK,
			docsStatus: http.StatusOK,
			specStatus: http.StatusNotFound,
		},
		{
			name: "openapi and docs disabled",
			configure: func(config *Config) {
				config.Docs.Enabled = false
				config.OpenAPI.Enabled = false
			},
			rootStatus: http.StatusOK,
			docsStatus: http.StatusNotFound,
			specStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := chi.NewRouter()
			config := DefaultConfig("orders", "dev")
			test.configure(&config)
			New(router, config)

			assert.Equal(t, test.rootStatus, routeStatus(router, "/"))
			assert.Equal(t, test.docsStatus, routeStatus(router, "/docs"))
			assert.Equal(t, test.specStatus, routeStatus(router, OpenAPIPath+".json"))
		})
	}
}

func routeStatus(handler http.Handler, target string) int {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response.Code
}

func TestValidationSchemaInstalledBySharedConfiguration(t *testing.T) {
	api := New(chi.NewRouter(), DefaultConfig("test", "dev"))
	huma.Register(api, huma.Operation{Method: http.MethodPost, Path: "/validation", OperationID: "validation"},
		func(context.Context, *struct {
			Body struct {
				Name string `json:"name" minLength:"3"`
			}
		}) (*struct{}, error) {
			return &struct{}{}, nil
		})
	response := api.OpenAPI().Paths["/validation"].Post.Responses["422"]
	if assert.NotNil(t, response) {
		schema := response.Content["application/json"].Schema
		assert.Len(t, schema.AnyOf, 2)
		assert.Contains(t, schema.AnyOf[1].Ref, "ValidationError")
	}
}
