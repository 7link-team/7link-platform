package agents

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func TestWakeHTTPSecretShownOnceReplayAuthorizationAndVersion(t *testing.T) {
	m, db, h, api := agentHTTPHarness(t)
	m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	path := "/tenants/1/agents/7/wake-config"
	call := func(actor int64, key, version, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)).WithContext(agentContext(t, guid.ID(actor)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", version)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	body := `{"wake_mode":"WEBHOOK","webhook_url":"https://receiver.example/wake"}`
	first := call(2, "config-1", "1", body)
	require.Equal(t, 200, first.Code, first.Body.String())
	var out struct {
		Data WakeConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &out))
	secret := out.Data.Secret
	require.NotEmpty(t, secret)
	replay := call(2, "config-1", "1", body)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.NotContains(t, replay.Body.String(), secret)
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	read := callAgent(t, h, agentContext(t, 2), http.MethodGet, path, "", "")
	require.Equal(t, 200, read.Code, read.Body.String())
	require.NotContains(t, read.Body.String(), secret)
	stale := call(2, "config-2", "1", body)
	require.Equal(t, 409, stale.Code, stale.Body.String())
	denied := call(7, "agent-config", "2", body)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	var receipts []string
	require.NoError(t, db.Raw("SELECT response_body FROM k_platform_idempotency_keys").Scan(&receipts).Error)
	for _, receipt := range receipts {
		require.NotContains(t, receipt, secret, "the secret must never enter replay storage")
	}
	documented := api.OpenAPI().Paths["/tenants/{tenant_id}/agents/{agent_principal_id}/wake-config"]
	require.NotNil(t, documented)
	require.NotNil(t, documented.Put)
}
