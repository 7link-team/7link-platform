package humax

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/pkg/i18n"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

func TestNewConfiguresAPIAndSharedMiddleware(t *testing.T) {
	router := chi.NewRouter()
	api := New(router, "orders", "2.3.4")

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
	api := New(chi.NewRouter(), "  orders  ", "dev")
	assert.Equal(t, "orders API", api.OpenAPI().Info.Title)
}
