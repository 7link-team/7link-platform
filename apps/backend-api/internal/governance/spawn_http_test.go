package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnMemberPagesPreserveCompleteHistoryBeyondOneHundred(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	q := query.BindSpawnGrantMembers(db, m.seed.names)
	at, reason := m.seed.now(), "RETIRED"
	for i := 0; i < 120; i++ {
		require.NoError(t, q.WithContext(ctx).Create(&model.SpawnGrantMember{ID: guid.ID(10000 + i), TenantID: 1, GrantID: grant.ID, AgentID: guid.ID(20000 + i), State: "RELEASED", PerAgentCapMicros: 1, ReservedAt: at, ReleasedAt: &at, ReleaseReason: &reason}))
	}
	seen := map[guid.ID]bool{}
	for _, offset := range []int{0, 50, 100} {
		page, err := m.membersSpawnPageHTTP(ctx, &spawnMembersInput{SpawnRefInput: SpawnRefInput{TenantID: "1", ID: grant.ID.String()}, Limit: 50, Offset: offset})
		require.NoError(t, err)
		for _, member := range page.Body.Data {
			require.False(t, seen[member.AgentID])
			seen[member.AgentID] = true
		}
	}
	require.Len(t, seen, 120)
	_, err = m.membersSpawnPageHTTP(ctx, &spawnMembersInput{SpawnRefInput: SpawnRefInput{TenantID: "1", ID: grant.ID.String()}, Limit: 201})
	require.Error(t, err)
}

func spawnHTTPHarness(t *testing.T) (*Module, *gorm.DB, http.Handler, SpawnGrantInput) {
	t.Helper()
	m, db, _, _, in := spawnGrantHarness(t)
	store := idempotency.NewModule(m.runtime)
	s := module.NewServices()
	require.NoError(t, module.Provide[guid.Source](s, m.seed.ids))
	require.NoError(t, s.Seal())
	require.NoError(t, store.InjectServices(s))
	source := store.MigrationSource()
	source.Prefix = "g_"
	raw, e := db.DB()
	require.NoError(t, e)
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	m.delegations.replay = store
	r, e := authz.NewRegistry()
	require.NoError(t, e)
	require.NoError(t, r.Contribute([]authz.Action{{Resource: "policy", Verb: "read", Scope: authz.ActionScopeTenant}, {Resource: "spawn_grant", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	authority := &governanceHTTPAuthority{member: true, allow: true}
	m.registrar = authz.NewRegistrar(r, authority, authority, authority)
	m.seed.registry = r
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	m.registerSpawnHTTP(api)
	return m, db, router, in
}

func TestSpawnHTTPUsesActualIdempotencyCurrentAuthorityCASAndTenantFences(t *testing.T) {
	m, db, h, in := spawnHTTPHarness(t)
	request := func(method, path, key, etag string, body any) *httptest.ResponseRecorder {
		data, e := json.Marshal(body)
		require.NoError(t, e)
		r := httptest.NewRequest(method, "/tenants/1"+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer valid")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request("POST", "/spawn-grants", "grant-key", "", in)
	require.Equal(t, 201, first.Code, first.Body.String())
	require.Equal(t, `"1"`, first.Header().Get("ETag"))
	var created spawnOutput
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created.Body))
	id := created.Body.Data.ID.String()
	in.ReauthProof = "fresh-proof"
	replayed := request("POST", "/spawn-grants", "grant-key", "", in)
	require.Equal(t, 201, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replayed"))
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.SpawnGrants.String()))
	changed := in
	changed.Reason = "Different business payload must conflict"
	conflict := request("POST", "/spawn-grants", "grant-key", "", changed)
	require.Equal(t, 409, conflict.Code)
	require.Contains(t, conflict.Body.String(), "176425")
	detail := request("GET", "/spawn-grants/"+id, "", "", nil)
	require.Equal(t, 200, detail.Code, detail.Body.String())
	require.Equal(t, `"1"`, detail.Header().Get("ETag"))
	list := request("GET", "/spawn-grants?limit=1&offset=0", "", "", nil)
	require.Equal(t, 200, list.Code, list.Body.String())
	require.Contains(t, list.Body.String(), `"total":1`)
	members := request("GET", "/spawn-grants/"+id+"/members", "", "", nil)
	require.Equal(t, 200, members.Code, members.Body.String())
	tight := SpawnGrantUpdate{MaxSpawnsPerDay: ptrInt(1), Reason: "Tighten available daily capacity"}
	updated := request("PATCH", "/spawn-grants/"+id, "", `"1"`, tight)
	require.Equal(t, 200, updated.Code, updated.Body.String())
	require.Equal(t, `"2"`, updated.Header().Get("ETag"))
	stale := request("PATCH", "/spawn-grants/"+id, "", `"1"`, tight)
	require.Equal(t, 409, stale.Code, stale.Body.String())
	revoked := request("POST", "/spawn-grants/"+id+"/revocations", "revoke-key", `"2"`, map[string]string{"reason": "Revoke obsolete worker capacity"})
	require.Equal(t, 200, revoked.Code, revoked.Body.String())
	require.Contains(t, revoked.Body.String(), `"state":"REVOKED"`)
	again := request("POST", "/spawn-grants/"+id+"/revocations", "revoke-key", `"2"`, map[string]string{"reason": "Revoke obsolete worker capacity"})
	require.Equal(t, 200, again.Code, again.Body.String())
	require.Equal(t, "true", again.Header().Get("Idempotent-Replayed"))
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false}, nil
	}
	denied := request("POST", "/spawn-grants", "grant-key", "", in)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}
