package respx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryWritesPlatformErrorEnvelope(t *testing.T) {
	rt := newTestRuntime()
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, NewModule(rt).Initialize(t.Context()))
	handler := Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("failure")
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	require.Equal(t, http.StatusInternalServerError, response.Code)
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, http.StatusInternalServerError, body.Code)
	assert.Equal(t, "The server could not complete the request. Retry later; contact an administrator if the problem continues.", body.Message)
}

func TestRecoveryDoesNotOverwriteStartedResponse(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
		panic("failure")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))
	assert.Equal(t, http.StatusAccepted, response.Code)
	assert.Empty(t, response.Body.String())
}
