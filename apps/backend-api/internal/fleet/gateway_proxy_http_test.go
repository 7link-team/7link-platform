package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGatewayProxyHTTPRealProviderReplayAndCurrentHumanAuthority(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	store := idempotency.NewModule(m.runtime)
	raw, err := db.DB()
	require.NoError(t, err)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	m.idempotency = store
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	calls := 0
	upstreamStatus, contentType := http.StatusOK, "application/json"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer private-provider-key" {
			t.Error("provider credential not resolved")
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(upstreamStatus)
		_, _ = io.WriteString(w, `{"id":"real-response","status":"completed","usage":{"input_tokens":1,"output_tokens":2}}`)
	}))
	defer upstream.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = upstream.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	m.gatewayBudgets.ReserveOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error { return nil }
	settled := int64(-1)
	m.gatewaySettlement = func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, actual int64) error { settled = actual; return nil }
	router := fleetBusinessRouter(t, m)
	base := "/tenants/7/model-gateway"
	r := fleetHTTPRequest(router, http.MethodPost, base+"/provider-keys", `{"secret":"private-provider-key"}`, map[string]string{"Idempotency-Key": "provider-import"})
	require.Equal(t, 201, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "private-provider-key")
	keys := struct{ ID guid.ID }{}
	require.NoError(t, db.Table(m.names.GatewayKeys.String()).Select("id").Take(&keys).Error)
	in := gatewayWrite()
	in.Enabled = true
	in.Providers[0].BaseURL = upstream.URL + "/v1"
	in.Providers[0].KeyRef = &keys.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SaveGatewayOn(ctx, tx, 7, 0, in); return err }))
	body := `{"project_id":"20","max_input_tokens":8,"max_output_tokens":8,"request":{"input":"hello"}}`
	r = fleetHTTPRequest(router, http.MethodPost, base+"/responses", body, map[string]string{"Idempotency-Key": "actual-request"})
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "real-response")
	require.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	require.Equal(t, int64(12), settled)
	require.Equal(t, 1, calls)
	r = fleetHTTPRequest(router, http.MethodPost, base+"/responses", body, map[string]string{"Idempotency-Key": "actual-request"})
	require.Equal(t, 409, r.Code, r.Body.String())
	require.Equal(t, 1, calls, "uncertain or completed requests must not be replayed")
	for _, failure := range []struct {
		key         string
		status      int
		contentType string
	}{{"provider-auth", 401, "application/json"}, {"wrong-type", 200, "text/html"}} {
		upstreamStatus, contentType = failure.status, failure.contentType
		r = fleetHTTPRequest(router, http.MethodPost, base+"/responses", body, map[string]string{"Idempotency-Key": failure.key})
		require.Equal(t, 503, r.Code, r.Body.String())
		var envelope struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Meta    any    `json:"meta"`
			Data    any    `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.Body.Bytes(), &envelope))
		require.Equal(t, 503, envelope.Code)
		require.NotEmpty(t, envelope.Message)
		require.NotNil(t, envelope.Meta)
		require.Nil(t, envelope.Data)
	}
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	r = fleetHTTPRequest(router, http.MethodPost, base+"/responses", body, map[string]string{"Idempotency-Key": "actual-request"})
	require.Equal(t, 403, r.Code, r.Body.String())
	require.Equal(t, 3, calls)
}

func TestGatewayProxyHTTPRejectsMissingReceiptAndSecretControls(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	router := fleetBusinessRouter(t, m)
	for _, secret := range []string{"has space", "bad\\nvalue"} {
		r := fleetHTTPRequest(router, http.MethodPost, "/tenants/7/model-gateway/provider-keys", `{"secret":"`+secret+`"}`, map[string]string{"Idempotency-Key": "invalid"})
		require.Equal(t, 422, r.Code, r.Body.String())
	}
	r := fleetHTTPRequest(router, http.MethodPost, "/tenants/7/model-gateway/responses", `{"project_id":"20","max_input_tokens":8,"max_output_tokens":8,"request":{"input":"hello"}}`, map[string]string{"Idempotency-Key": "no-store"})
	require.Equal(t, 503, r.Code, r.Body.String())
}
