package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	healthi18n "7link.com/platform/internal/infra/health/i18n"
	"7link.com/platform/internal/infra/health/service"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/i18n"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	require.NoError(t, i18n.InitEmbedded(i18n.Base))
	require.NoError(t, healthi18n.Register())
	router := chi.NewRouter()
	api := humax.New(router, "test", "1.0.0")
	New(service.New()).Register(api)
	return router
}

func TestCheckHTTP(t *testing.T) {
	handler := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Accept-Language", "zh-CN")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "zh-CN", recorder.Header().Get("Content-Language"))
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Status service.Status `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &fields))
	assert.Equal(t, 0, body.Code)
	assert.Equal(t, "健康检查成功完成。", body.Message)
	assert.Equal(t, service.StatusServing, body.Data.Status)
	assert.ElementsMatch(t, []string{"code", "message", "meta", "data"}, mapKeys(fields))
}

func TestOpenAPIIncludesHealth(t *testing.T) {
	handler := newTestHandler(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var spec struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &spec))
	assert.Equal(t, "3.1.0", spec.OpenAPI)
	assert.Contains(t, spec.Paths, "/healthz")
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
