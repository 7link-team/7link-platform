package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T, services ...*service.Service) http.Handler {
	t.Helper()
	require.NoError(t, i18n.NewModule(i18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule().Initialize(t.Context()))
	require.NoError(t, healthi18n.Register())
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("test", "1.0.0"))
	svc := service.New()
	if len(services) > 0 {
		svc = services[0]
	}
	New(svc).Register(api)
	return router
}

func TestCheckHTTP(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, i18n.Base, recorder.Header().Get("Content-Language"))
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Status  service.Status         `json:"status"`
			Modules []service.ModuleStatus `json:"modules"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &fields))
	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "Health check completed.", body.Message)
	assert.Equal(t, service.StatusServing, body.Data.Status)
	assert.Empty(t, body.Data.Modules)
	assert.ElementsMatch(t, []string{"code", "message", "meta", "data"}, mapKeys(fields))
}

func TestCheckHTTPLocalePrecedence(t *testing.T) {
	handler := newTestHandler(t)
	tests := []struct {
		name       string
		target     string
		xLang      string
		acceptLang string
		message    string
	}{
		{name: "base when headers are absent", target: "/healthz", message: "Health check completed."},
		{name: "accept-language", target: "/healthz", acceptLang: "zh-CN", message: "健康检查已完成。"},
		{name: "x-lang wins", target: "/healthz", xLang: "zh-CN", acceptLang: "en-US", message: "健康检查已完成。"},
		{name: "query wins", target: "/healthz?lang=en-US", xLang: "zh-CN", acceptLang: "zh-CN", message: "Health check completed."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header.Set("X-Lang", test.xLang)
			request.Header.Set("Accept-Language", test.acceptLang)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			var body struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, test.message, body.Message)
		})
	}
}

func TestReadinessHTTP(t *testing.T) {
	unavailable := errors.New("database unavailable")
	serviceValue := service.New(func() []service.Checker {
		return []service.Checker{
			{Name: "http"},
			{Name: "database", Check: func(context.Context) error { return unavailable }},
		}
	})
	handler := newTestHandler(t, serviceValue)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	var body struct {
		Code    int               `json:"code"`
		Message string            `json:"message"`
		Data    service.CheckData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, http.StatusServiceUnavailable, body.Code)
	assert.Equal(t, "Service is not ready.", body.Message)
	assert.Equal(t, service.StatusNotServing, body.Data.Status)
	assert.Equal(t, []service.ModuleStatus{
		{Name: "http", Status: service.StatusUnknown},
		{Name: "database", Status: service.StatusNotServing},
	}, body.Data.Modules)
}

func TestReadinessHTTPWhenServing(t *testing.T) {
	handler := newTestHandler(t, service.New(func() []service.Checker {
		return []service.Checker{{Name: "http"}}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Message string            `json:"message"`
		Data    service.CheckData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, "Readiness check completed successfully.", body.Message)
	assert.Equal(t, service.StatusServing, body.Data.Status)
}

func TestLivenessIgnoresDependencyFailures(t *testing.T) {
	handler := newTestHandler(t, service.New(func() []service.Checker {
		return []service.Checker{{
			Name:  "database",
			Check: func(context.Context) error { return errors.New("unavailable") },
		}}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))

	require.Equal(t, http.StatusOK, response.Code)
	var body struct {
		Code int               `json:"code"`
		Data service.CheckData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Zero(t, body.Code)
	assert.Equal(t, service.StatusServing, body.Data.Status)
	assert.Empty(t, body.Data.Modules)
}

func TestOpenAPIIncludesHealth(t *testing.T) {
	handler := newTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var spec struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]struct {
			Get struct {
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &spec))
	assert.Equal(t, "3.1.0", spec.OpenAPI)
	assert.Contains(t, spec.Paths, "/livez")
	assert.Contains(t, spec.Paths, "/healthz")
	assert.Contains(t, spec.Paths, "/readyz")
	assert.Contains(t, spec.Paths["/readyz"].Get.Responses, "200")
	assert.Contains(t, spec.Paths["/readyz"].Get.Responses, "503")
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
