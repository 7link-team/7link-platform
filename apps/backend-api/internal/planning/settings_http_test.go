package planning

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanningSettingsHTTPPatchETagAndCurrentAuthority(t *testing.T) {
	h := setup(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, h.m.RegisterHTTP(t.Context(), api))
	call := func(method, version, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/tenants/1/planning-settings", strings.NewReader(body)).WithContext(claims(t, 3))
		r.Header.Set("Content-Type", "application/json")
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	w := call("GET", "", "", 200)
	require.Equal(t, "\"1\"", w.Header().Get("ETag"))
	require.Contains(t, w.Body.String(), "Asia/Shanghai")
	w = call("PATCH", "\"1\"", `{"timezone":"Europe/Berlin","agent_day_report":false,"grace_day_minutes":0}`, 200)
	require.Equal(t, "\"2\"", w.Header().Get("ETag"))
	require.Contains(t, w.Body.String(), `"agent_day_report":false`)
	call("PATCH", "\"1\"", `{"timezone":"UTC"}`, 409)
	for _, version := range []string{"1", "W/\"2\"", "\"02\""} {
		call("PATCH", version, `{"timezone":"UTC"}`, 422)
	}
	call("PATCH", "\"2\"", `{"day_report_at":"25:00"}`, 422)
	w = call("GET", "", "", 200)
	require.Contains(t, w.Body.String(), "Europe/Berlin")
	require.Equal(t, "\"2\"", w.Header().Get("ETag"))
}
