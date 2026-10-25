package fleet

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type agentSpawnHTTPAuthority struct{ fleetHTTPAuthority }

func (agentSpawnHTTPAuthority) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{TenantID: 7, PrincipalID: 11, SubjectType: authn.SubjectTypeServiceAccount}, nil
}

func TestAgentSpawnHTTPReplayAndCurrentSourceAuthority(t *testing.T) {
	m, db, ctx, _, in := agentSpawnHarness(t)
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
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	authority := agentSpawnHTTPAuthority{}
	m.registrar = authz.NewRegistrar(registry, authority, authority, authority)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	m.registerAgentSpawnHTTP(api)
	body, err := json.Marshal(in)
	require.NoError(t, err)
	headers := map[string]string{"Idempotency-Key": "spawn-one"}
	first := fleetHTTPRequest(router, "POST", "/tenants/7/agent-spawns", string(body), headers)
	require.Equal(t, 201, first.Code, first.Body.String())
	replay := fleetHTTPRequest(router, "POST", "/tenants/7/agent-spawns", string(body), headers)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotency-Replayed"))
	var result struct {
		Data RegisteredAgent `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &result))
	in.Reason = "Different request under the same key"
	changed, err := json.Marshal(in)
	require.NoError(t, err)
	conflict := fleetHTTPRequest(router, "POST", "/tenants/7/agent-spawns", string(changed), headers)
	require.Equal(t, 409, conflict.Code, conflict.Body.String())
	m.spawnPolicy.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, AgentSpawnInput, bool) (AgentSpawnGrant, error) {
		return AgentSpawnGrant{}, ErrMachineForbidden
	}
	denied := fleetHTTPRequest(router, "POST", "/tenants/7/agent-spawns", string(body), headers)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	retireBody := `{"version":1,"reason":"Capacity is no longer needed"}`
	retirePath := "/tenants/7/agent-spawns/" + result.Data.ID.String() + "/retirements"
	retire := fleetHTTPRequest(router, "POST", retirePath, retireBody, map[string]string{"Idempotency-Key": "retire-one"})
	require.Equal(t, 200, retire.Code, retire.Body.String())
	retireReplay := fleetHTTPRequest(router, "POST", retirePath, retireBody, map[string]string{"Idempotency-Key": "retire-one"})
	require.Equal(t, 200, retireReplay.Code, retireReplay.Body.String())
	require.Equal(t, "true", retireReplay.Header().Get("Idempotency-Replayed"))
	m.spawnPolicy.RetireOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, bool) error {
		return ErrMachineForbidden
	}
	retireDenied := fleetHTTPRequest(router, "POST", retirePath, retireBody, map[string]string{"Idempotency-Key": "retire-one"})
	require.Equal(t, 403, retireDenied.Code, retireDenied.Body.String())
}
