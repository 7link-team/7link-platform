package notification

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func notificationHTTP(t *testing.T, f *fixture) humatest.TestAPI {
	t.Helper()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := authn.WithClaims(r.Context(), &authn.Claims{Subject: "11", PrincipalID: 11, SubjectType: "HUMAN"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	api := humax.New(router, humax.DefaultConfig("test", "1"))
	require.NoError(t, f.m.RegisterHTTP(t.Context(), api))
	return humatest.Wrap(t, api)
}

func TestHTTPStorageOutageReturnsUnavailable(t *testing.T) {
	f := newFixture(t)
	id := f.notify(t, sample())[0]
	api := notificationHTTP(t, f)
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("notification:test-outage", func(db *gorm.DB) { db.AddError(errors.New("storage outage")) }))
	for _, path := range []string{"/principals/11/notifications", "/principals/11/notification-summary", "/principals/11/tenants/7/notification-preferences/*"} {
		response := api.Get(path)
		require.Equal(t, 503, response.Code, response.Body.String())
	}
	response := api.Patch("/notifications/"+id.String(), map[string]any{"read": true})
	require.Equal(t, 503, response.Code, response.Body.String())
	response = api.Post("/principals/11/notification-reads", map[string]any{"tenant_id": "7", "up_to_id": id.String()})
	require.Equal(t, 503, response.Code, response.Body.String())
	p := PreferenceView{InApp: true, Email: "OFF", Timezone: "UTC"}
	response = api.Put("/principals/11/tenants/7/notification-preferences/*", "If-Match: 0", p)
	require.Equal(t, 503, response.Code, response.Body.String())
}
func TestInboxHTTPContract(t *testing.T) {
	f := newFixture(t)
	id := f.notify(t, sample())[0]
	api := notificationHTTP(t, f)
	response := api.Get("/principals/11/notifications")
	require.Equal(t, 200, response.Code, response.Body.String())
	var page respx.Envelope[[]NotificationView]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	require.Equal(t, id, page.Data[0].ID)
	response = api.Get("/principals/11/notification-summary")
	require.Equal(t, 200, response.Code, response.Body.String())
	var summary respx.Envelope[NotificationSummary]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	require.Equal(t, 1, summary.Data.Unread)
	response = api.Patch("/notifications/"+id.String(), map[string]any{"read": true})
	require.Equal(t, 200, response.Code, response.Body.String())
	response = api.Post("/principals/11/notification-reads", map[string]any{"tenant_id": "7", "up_to_id": id.String()})
	require.Equal(t, 200, response.Code, response.Body.String())
	response = api.Get("/principals/11/notifications?tenant_id=invalid")
	require.Equal(t, 400, response.Code, response.Body.String())
	response = api.Get("/principals/11/notification-summary?tenant_id=invalid")
	require.Equal(t, 400, response.Code, response.Body.String())
	response = api.Patch("/notifications/999", map[string]any{"read": true})
	require.Equal(t, 404, response.Code, response.Body.String())
}

func TestPreferenceHTTPPreconditions(t *testing.T) {
	f := newFixture(t)
	api := notificationHTTP(t, f)
	path := "/principals/11/tenants/7/notification-preferences/*"
	response := api.Get(path)
	require.Equal(t, 200, response.Code, response.Body.String())
	var p respx.Envelope[PreferenceView]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &p))
	response = api.Put(path, p.Data)
	require.Equal(t, 428, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: 0", p.Data)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: 0", p.Data)
	require.Equal(t, 409, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: invalid", p.Data)
	require.Equal(t, 400, response.Code, response.Body.String())
	f.members.active = false
	response = api.Get(path)
	require.Equal(t, 403, response.Code, response.Body.String())
}

func TestPlatformPreferenceHTTPPermissionAndVersion(t *testing.T) {
	f := newFixture(t)
	api := notificationHTTP(t, f)
	path := "/principals/11/platform-notification-preferences/*"
	response := api.Get(path)
	require.Equal(t, 200, response.Code, response.Body.String())
	var p respx.Envelope[PreferenceView]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &p))
	require.Zero(t, p.Data.TenantID)
	p.Data.Email = "DAILY_DIGEST"
	response = api.Put(path, p.Data)
	require.Equal(t, 428, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: 0", p.Data)
	require.Equal(t, 200, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: 0", p.Data)
	require.Equal(t, 409, response.Code, response.Body.String())
	response = api.Get("/principals/11/tenants/0/notification-preferences/*")
	require.Equal(t, 422, response.Code, response.Body.String())
	f.allow = false
	response = api.Get(path)
	require.Equal(t, 403, response.Code, response.Body.String())
	response = api.Put(path, "If-Match: 1", p.Data)
	require.Equal(t, 403, response.Code, response.Body.String())
}

func TestHTTPUnavailableAndInputValidation(t *testing.T) {
	f := newFixture(t)
	api := notificationHTTP(t, f)
	f.m.runtime = nil
	for _, path := range []string{"/principals/11/notifications", "/principals/11/notification-summary", "/principals/11/tenants/7/notification-preferences/*"} {
		response := api.Get(path)
		require.Equal(t, 503, response.Code, response.Body.String())
	}
	response := api.Patch("/notifications/1", map[string]any{"read": true})
	require.Equal(t, 503, response.Code)
	response = api.Post("/principals/11/notification-reads", map[string]any{"tenant_id": "7", "up_to_id": "1"})
	require.Equal(t, 503, response.Code)
	require.Error(t, (*Module)(nil).RegisterHTTP(t.Context(), api))
}
