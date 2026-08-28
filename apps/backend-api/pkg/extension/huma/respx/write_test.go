package respx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteError(t *testing.T) {
	initI18n(t)

	t.Run("localized envelope with retry-after", func(t *testing.T) {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(i18n.WithLocale(r.Context(), "zh-CN"))

		WriteError(rr, r, http.StatusTooManyRequests, messageKey("too_many_requests"), 3*time.Second)

		assert.Equal(t, http.StatusTooManyRequests, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		assert.Equal(t, "3", rr.Header().Get("Retry-After"))

		var env struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Meta    json.RawMessage `json:"meta"`
			Data    json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
		assert.Equal(t, http.StatusTooManyRequests, env.Code)
		assert.Equal(t, "已达到请求频率限制，请等待一段时间后重试。", env.Message)
		assert.JSONEq(t, "null", string(env.Data))
		assert.NotEmpty(t, env.Meta)
	})

	t.Run("no retry-after header when zero", func(t *testing.T) {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		WriteError(rr, r, http.StatusForbidden, messageKey("forbidden"), 0)

		assert.Empty(t, rr.Header().Get("Retry-After"))
		var env struct {
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
		assert.Equal(t, "You do not have permission to perform this operation. Request access or use an authorized account.", env.Message, "unset locale falls back to base")
	})
}

func TestWriteOK(t *testing.T) {
	initI18n(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(i18n.WithLocale(request.Context(), "zh-CN"))

	WriteOK(recorder, request, map[string]string{"docs": "/docs"})

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	var envelope Envelope[map[string]string]
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	assert.Zero(t, envelope.Code)
	assert.Equal(t, "成功", envelope.Message)
	assert.Equal(t, "/docs", envelope.Data["docs"])
	assert.False(t, envelope.Meta.RequestAt.IsZero())
}
