package fleet

import (
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"testing"
)

func TestGatewayHTTPUsesDedicatedCurrentTenantAuthority(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	bindGatewayHTTPBudget(m)
	m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "gateway_manage"}, nil
	}
	router := fleetBusinessRouter(t, m)
	base := "/tenants/7/model-gateway"
	for _, path := range []string{"/config", "/requests"} {
		r := fleetHTTPRequest(router, http.MethodGet, base+path, "", nil)
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	}
	m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "machine_manage" || permission == "machine_read"}, nil
	}
	for _, path := range []string{"/config", "/requests"} {
		r := fleetHTTPRequest(router, http.MethodGet, base+path, "", nil)
		require.Equal(t, http.StatusForbidden, r.Code, "machine authority must not grant gateway administration")
	}
}

func TestGatewayHTTPAdministrationETagLedgerAndSecretFreeKeys(t *testing.T) {
	m, _, _, _, _ := enrollmentHarness(t)
	bindGatewayHTTPBudget(m)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	router := fleetBusinessRouter(t, m)
	base := "/tenants/7/model-gateway"
	r := fleetHTTPRequest(router, http.MethodGet, base+"/config", "", nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, `"0"`, r.Header().Get("ETag"))
	require.Contains(t, r.Body.String(), `"available":false`)
	b, e := json.Marshal(gatewayWrite())
	require.NoError(t, e)
	r = fleetHTTPRequest(router, http.MethodPut, base+"/config", string(b), map[string]string{"If-Match": `"0"`})
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, `"1"`, r.Header().Get("ETag"))
	r = fleetHTTPRequest(router, http.MethodPut, base+"/config", string(b), map[string]string{"If-Match": `"0"`})
	require.Equal(t, 409, r.Code)
	r = fleetHTTPRequest(router, http.MethodPost, base+"/keys", "", nil)
	require.Equal(t, 201, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "sealed")
	require.NotContains(t, r.Body.String(), "key_hash")
	r = fleetHTTPRequest(router, http.MethodPost, base+"/requests", `{"model":"gpt-test","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`, nil)
	require.Equal(t, 201, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"state":"FAILED"`)
	r = fleetHTTPRequest(router, http.MethodGet, base+"/requests", "", nil)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "gwr_")
	for _, path := range []string{"/config", "/keys", "/requests"} {
		r = fleetHTTPRequest(router, http.MethodOptions, base+path+"?method="+map[string]string{"/config": "PUT", "/keys": "POST", "/requests": "POST"}[path], "", nil)
		require.Equal(t, 200, r.Code, r.Body.String())
	}
	r = fleetHTTPRequest(router, http.MethodPut, base+"/config", string(b), map[string]string{"If-Match": "invalid"})
	require.Equal(t, 422, r.Code)
}

func TestGatewayHTTPIdempotencyUsesPlatformReceiptsAndCurrentAuthority(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	bindGatewayHTTPBudget(m)
	m.config.GatewayEncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	store := idempotency.NewModule(m.runtime)
	raw, e := db.DB()
	require.NoError(t, e)
	source := store.MigrationSource()
	source.Prefix = "dir_"
	require.NoError(t, goosex.Migrate(ctx, raw, source, "sqlite"))
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	m.idempotency = store
	router := fleetBusinessRouter(t, m)
	base := "/tenants/7/model-gateway"
	header := map[string]string{"Idempotency-Key": "one-key"}
	first := fleetHTTPRequest(router, http.MethodPost, base+"/keys", "", header)
	require.Equal(t, 201, first.Code, first.Body.String())
	replay := fleetHTTPRequest(router, http.MethodPost, base+"/keys", "", header)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	var a, b map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a["data"], b["data"])
	var keyCount int64
	require.NoError(t, db.Table(m.names.GatewayKeys.String()).Count(&keyCount).Error)
	require.Equal(t, int64(1), keyCount)
	var created struct {
		Data GatewayKeyView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created))
	body, e := json.Marshal(gatewayWrite())
	require.NoError(t, e)
	r := fleetHTTPRequest(router, http.MethodPut, base+"/config", string(body), map[string]string{"If-Match": "0"})
	require.Equal(t, 200, r.Code, r.Body.String())
	r = fleetHTTPRequest(router, http.MethodPut, base+"/config", string(body), nil)
	require.Equal(t, 428, r.Code, r.Body.String())
	rotationPath := base + "/keys/" + created.Data.ID.String() + "/rotations"
	rotationBody := `{"version":1,"reason":"Rotate the configured gateway credential"}`
	rotated := fleetHTTPRequest(router, http.MethodPost, rotationPath, rotationBody, header)
	require.Equal(t, 201, rotated.Code, rotated.Body.String())
	rotationReplay := fleetHTTPRequest(router, http.MethodPost, rotationPath, rotationBody, header)
	require.Equal(t, 201, rotationReplay.Code, rotationReplay.Body.String())
	require.NoError(t, json.Unmarshal(rotated.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(rotationReplay.Body.Bytes(), &b))
	require.Equal(t, a["data"], b["data"])
	require.NotContains(t, rotated.Body.String(), "sealed")
	first = fleetHTTPRequest(router, http.MethodPost, base+"/requests", `{"model":"gpt-test","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`, header)
	require.Equal(t, 201, first.Code, first.Body.String())
	replay = fleetHTTPRequest(router, http.MethodPost, base+"/requests", `{"model":"gpt-test","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`, header)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &a))
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &b))
	require.Equal(t, a["data"], b["data"])
	r = fleetHTTPRequest(router, http.MethodPost, base+"/requests", `{"model":"other","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`, header)
	require.Equal(t, 422, r.Code, r.Body.String())
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, nil
	}
	r = fleetHTTPRequest(router, http.MethodPost, base+"/requests", `{"model":"gpt-test","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`, header)
	require.Equal(t, 403, r.Code, "replay must recheck current IAM")
	r = fleetHTTPRequest(router, http.MethodPost, rotationPath, rotationBody, header)
	require.Equal(t, 403, r.Code, "rotation replay rechecks current Human authority")
}

func TestGatewayHTTPFailuresDoNotLeakStorageOrKeys(t *testing.T) {
	for _, fault := range []string{"writer", "config_query", "request_query", "authority", "missing_store"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _, _, _ := enrollmentHarness(t)
			bindGatewayHTTPBudget(m)
			router := fleetBusinessRouter(t, m)
			base := "/tenants/7/model-gateway"
			var paths = []struct{ method, path, body string }{{http.MethodGet, "/config", ""}, {http.MethodPut, "/config", `{"enabled":false,"default_model":"","providers":[],"models":[],"budgets":[]}`}, {http.MethodGet, "/requests", ""}, {http.MethodPost, "/keys", ""}, {http.MethodPost, "/requests", `{"model":"id","project_id":"20","max_input_tokens":100,"max_output_tokens":100}`}}
			switch fault {
			case "writer":
				m.runtime = nil
			case "config_query":
				require.NoError(t, db.Exec("DROP TABLE "+m.names.GatewayConfigs.String()).Error)
				paths = paths[:2]
			case "request_query":
				require.NoError(t, db.Exec("DROP TABLE "+m.names.GatewayRequests.String()).Error)
				paths = paths[2:3]
			case "authority":
				m.principals.ActiveHumanByID = nil
			case "missing_store":
				paths = paths[3:]
			}
			for _, p := range paths {
				r := fleetHTTPRequest(router, p.method, base+p.path, p.body, map[string]string{"If-Match": "0", "Idempotency-Key": "test"})
				require.Equal(t, 503, r.Code, r.Body.String())
				require.NotContains(t, r.Body.String(), "sqlite")
				require.NotContains(t, r.Body.String(), "sealed")
			}
		})
	}
}

// HTTP administration tests isolate the fleet transport contract; budget
// transaction and cap invariants are exercised by budget's real-provider tests.
func bindGatewayHTTPBudget(m *Module) {
	m.gatewayBudgets = GatewayBudgetConsumer{
		ReserveOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error { return nil },
		SettleOn:  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil },
	}
}
