package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDelegationHTTPUsesExactAppliedAuthorityAndCurrentProject(t *testing.T) {
	m, db, h, in := delegationHTTPHarness(t)
	w := delegationRequest(t, h, "POST", "/tenants/1/delegations", "usage-grant", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	var envelope struct{ Data DelegationView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	id := envelope.Data.ID
	for i, change := range []func(*model.AutomatedDecision){
		func(*model.AutomatedDecision) {},
		func(r *model.AutomatedDecision) { r.State = "DENIED" },
		func(r *model.AutomatedDecision) { r.DecisionID = 0 },
		func(r *model.AutomatedDecision) { r.AuthoritySource += "0" },
		func(r *model.AutomatedDecision) { r.ProjectID = 100 },
		func(r *model.AutomatedDecision) { r.TenantID = 2 },
		func(r *model.AutomatedDecision) { r.ActorID = 4 },
	} {
		r := model.AutomatedDecision{ID: guid.ID(1000 + i), TenantID: 1, ProjectID: in.ProjectID, WorkItemID: 77, ClaimID: guid.ID(2000 + i), TriggerEventID: guid.ID(3000 + i), State: "APPLIED", Reason: "CURRENT_DELEGATION", ActorID: in.GranteePrincipalID, AuthoritySource: "DELEGATION:" + id.String(), DecisionID: guid.ID(4000 + i), CreatedAt: time.Now().UTC()}
		change(&r)
		require.NoError(t, query.BindAutomatedDecisions(db, m.seed.names).WithContext(t.Context()).Create(&r))
	}
	w = delegationRequest(t, h, "GET", "/tenants/1/delegations/"+id.String(), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data.UseCount)
	require.EqualValues(t, 1, *envelope.Data.UseCount)
	require.Len(t, envelope.Data.Uses, 1)
	require.EqualValues(t, 4000, envelope.Data.Uses[0].DecisionID)
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.EntityID != in.ProjectID}, nil
	}
	w = delegationRequest(t, h, "GET", "/tenants/1/delegations/"+id.String(), "", nil)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "CURRENT_DELEGATION")
}

func delegationHTTPHarness(t *testing.T) (*Module, *gorm.DB, http.Handler, DelegationGrantInput) {
	t.Helper()
	m, db, _, _, in := delegationHarness(t)
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 2}, true, nil
	}
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
	require.NoError(t, r.Contribute([]authz.Action{{Resource: "policy", Verb: "read", Scope: authz.ActionScopeTenant}, {Resource: "delegation", Verb: "grant", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	a := &governanceHTTPAuthority{member: true, allow: true}
	m.registrar = authz.NewRegistrar(r, a, a, a)
	m.seed.registry = r
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	m.registerDelegationHTTP(api)
	return m, db, router, in
}
func delegationRequest(t *testing.T, h http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, e := json.Marshal(body)
	require.NoError(t, e)
	r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer valid")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestDelegationHTTPRealIdempotencyCurrentAuthorityAndPaging(t *testing.T) {
	m, db, h, in := delegationHTTPHarness(t)
	w := delegationRequest(t, h, "POST", "/tenants/1/delegations", "one", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	var first struct{ Data DelegationView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.Positive(t, first.Data.ID)
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations", "one", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "true", w.Header().Get("Idempotent-Replayed"))
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	in.ReauthProof = "fresh-proof-after-lost-response"
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations", "one", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "true", w.Header().Get("Idempotent-Replayed"))
	changed := in
	changed.DurationMinutes = 120
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations", "one", changed)
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "176425")
	w = delegationRequest(t, h, "GET", "/tenants/1/delegations?limit=1&offset=0&project_id=99", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"total":1`)
	require.Contains(t, w.Body.String(), `"limit":1`)
	w = delegationRequest(t, h, "GET", "/tenants/1/delegations/"+first.Data.ID.String(), "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations/"+first.Data.ID.String()+"/revocations", "revoke", map[string]string{"reason": "Take control now"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"state":"REVOKED"`)
	require.Zero(t, seedCount(t, db, "binding_probe"))
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations/"+first.Data.ID.String()+"/revocations", "revoke", map[string]string{"reason": "Take control now"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "true", w.Header().Get("Idempotent-Replayed"))
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Reason: "explicit_deny"}, nil
	}
	w = delegationRequest(t, h, "POST", "/tenants/1/delegations", "one", in)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), first.Data.Reason)
	w = delegationRequest(t, h, "GET", "/tenants/1/delegations", "", nil)
	require.Equal(t, 403, w.Code, w.Body.String())
}

func TestDelegationHTTPRejectsInvalidBoundaryAndTenant(t *testing.T) {
	_, _, h, in := delegationHTTPHarness(t)
	for _, tc := range []struct {
		method, path, key string
		body              any
		status            int
	}{
		{"POST", "/tenants/1/delegations", "", in, 422},
		{"POST", "/tenants/2/delegations", "other", in, 403},
		{"GET", "/tenants/1/delegations?project_id=0", "", nil, 422},
		{"GET", "/tenants/1/delegations?limit=201", "", nil, 422},
		{"GET", "/tenants/1/delegations?offset=-1", "", nil, 422},
		{"GET", "/tenants/1/delegations?before_id=0", "", nil, 422},
		{"GET", "/tenants/1/delegations?before_id=9999999999999999999", "", nil, 400},
		{"GET", "/tenants/1/delegations?before_id=5&offset=1", "", nil, 400},
		{"GET", "/tenants/1/delegations/999", "", nil, 404},
	} {
		w := delegationRequest(t, h, tc.method, tc.path, tc.key, tc.body)
		require.Equal(t, tc.status, w.Code, w.Body.String())
	}
}

func TestDelegationHTTPKeysetSurvivesConcurrentInsertAndRechecksAuthority(t *testing.T) {
	m, db, h, in := delegationHTTPHarness(t)
	w := delegationRequest(t, h, "POST", "/tenants/1/delegations", "keyset-grant", in)
	require.Equal(t, 201, w.Code, w.Body.String())
	var created struct{ Data DelegationView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	q := query.BindDelegations(db, m.seed.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(created.Data.ID)).Take()
	require.NoError(t, err)
	// Fixture additional historical and foreign rows; only the real grant above
	// creates authority. Listing must never install or alter bindings.
	for _, fixture := range []struct {
		delta           int64
		tenant, project guid.ID
	}{
		{-1, 1, 99}, {-2, 1, 99}, {-3, 2, 99}, {-4, 1, 100},
	} {
		snapshot := *row
		snapshot.ID = row.ID + guid.ID(fixture.delta)
		snapshot.TenantID, snapshot.ProjectID = fixture.tenant, fixture.project
		snapshot.State, snapshot.ActiveKey = "REVOKED", nil
		reason := "MANUAL"
		snapshot.EndReason, snapshot.EndedAt = &reason, &snapshot.UpdatedAt
		require.NoError(t, q.WithContext(t.Context()).Create(&snapshot))
	}
	read := func(path string) []DelegationView {
		t.Helper()
		w := delegationRequest(t, h, "GET", path, "", nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		var page struct{ Data []DelegationView }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		return page.Data
	}
	root := "/tenants/1/delegations?project_id=99&limit=1"
	first := read(root)
	require.Len(t, first, 1)
	require.Equal(t, row.ID, first[0].ID)
	newer := *row
	newer.ID, newer.State, newer.ActiveKey = row.ID+1, "REVOKED", nil
	reason := "MANUAL"
	newer.EndReason, newer.EndedAt = &reason, &newer.UpdatedAt
	require.NoError(t, q.WithContext(t.Context()).Create(&newer))
	second := read(root + "&before_id=" + first[0].ID.String())
	require.Len(t, second, 1)
	require.Equal(t, row.ID-1, second[0].ID)
	third := read(root + "&state=REVOKED&before_id=" + second[0].ID.String())
	require.Len(t, third, 1)
	require.Equal(t, row.ID-2, third[0].ID)
	require.Empty(t, read(root+"&before_id="+third[0].ID.String()))
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: false}, nil
	}
	w = delegationRequest(t, h, "GET", root+"&before_id="+first[0].ID.String(), "", nil)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), row.Reason)
}
