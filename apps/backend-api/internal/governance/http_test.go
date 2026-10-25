package governance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type governanceHTTPAuthority struct{ member, allow bool }

func (a *governanceHTTPAuthority) Authenticate(_ context.Context, authorization, _ string) (*authn.Claims, error) {
	if authorization == "Bearer invalid" {
		return nil, errors.New("invalid credential")
	}
	return &authn.Claims{PrincipalID: 2, TenantID: 1, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (a *governanceHTTPAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return a.allow, nil
}
func (a *governanceHTTPAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}
func (a *governanceHTTPAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return a.member, nil
}
func (a *governanceHTTPAuthority) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{AllowAll: a.allow}, nil
}

func governanceHTTPHarness(t *testing.T) (*Module, *gorm.DB, http.Handler, *governanceHTTPAuthority) {
	t.Helper()
	m, db, _, _ := settingsHarness(t)
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "agent", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "audit", Verb: "read", Scope: authz.ActionScopeTenant}, {Resource: "policy", Verb: "read", Scope: authz.ActionScopeTenant}, {Resource: "policy", Verb: "manage", Scope: authz.ActionScopeTenant}, {Resource: "delegation", Verb: "grant", Scope: authz.ActionScopeTenant, DataScoped: true}, {Resource: "spawn_grant", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	authority := &governanceHTTPAuthority{member: true, allow: true}
	m.registrar = authz.NewRegistrar(registry, authority, authority, authority)
	m.seed.registry = registry
	require.NoError(t, m.policies.seal(registry))
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	return m, db, router, authority
}
func governanceRequest(t *testing.T, handler http.Handler, method, path string, body any, version string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid")
	if version != "" {
		req.Header.Set("If-Match", version)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestGovernanceInboxHTTPBindsCurrentMemberAndReturnsAllCapabilityKinds(t *testing.T) {
	m, _, handler, authority := governanceHTTPHarness(t)
	require.NoError(t, m.inbox.Register(inboxFixtureSource("WORK_ITEM_REVIEW", []InboxItem{inboxFixture(7, "WORK_ITEM_REVIEW", "HIGH")})))
	w := governanceRequest(t, handler, http.MethodGet, "/tenants/1/inbox-items", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"title":"Pending source decision"`)
	require.NotContains(t, w.Body.String(), `"recipient_id"`)
	for _, kind := range InboxKinds() {
		require.Contains(t, w.Body.String(), kind)
	}
	authority.member = false
	w = governanceRequest(t, handler, http.MethodGet, "/tenants/1/inbox-items", nil, "")
	require.Equal(t, 403, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "Pending source decision")
	authority.member = true
	for _, path := range []string{"/tenants/1/inbox-items?kind=unknown", "/tenants/1/inbox-items?cursor=invalid", "/tenants/1/inbox-items?project_id=bad", "/tenants/1/inbox-items?limit=101", "/tenants/1/inbox-items?sort=arbitrary"} {
		w = governanceRequest(t, handler, http.MethodGet, path, nil, "")
		require.True(t, w.Code == 400 || w.Code == 422, w.Body.String())
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 2})
	for _, in := range []*inboxListInput{{TenantID: "bad", Limit: 1}, {TenantID: "2", Limit: 1}, {TenantID: "1", ProjectID: "bad", Limit: 1}, {TenantID: "1", Sort: "bad", Limit: 1}} {
		_, err := m.listInboxHTTP(ctx, in)
		require.Error(t, err)
	}
	_, err := m.listInboxHTTP(t.Context(), &inboxListInput{TenantID: "1", Limit: 1})
	require.Error(t, err)
	_, err = (*Module)(nil).listInboxHTTP(ctx, nil)
	require.Error(t, err)
	require.ErrorIs(t, (*Module)(nil).RegisterHTTP(ctx, nil), ErrUnavailable)
	m.inbox.sources["WORK_ITEM_REVIEW"] = InboxSource{Kind: "WORK_ITEM_REVIEW", Read: func(context.Context, InboxRequest) ([]InboxItem, error) {
		return nil, errors.New("PRIVATE SQL failure")
	}}
	w = governanceRequest(t, handler, http.MethodGet, "/tenants/1/inbox-items", nil, "")
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "PRIVATE SQL")
}

func TestGovernanceCatalogHTTPIsPermissionGuardedAndDoesNotExposeEvaluators(t *testing.T) {
	m, _, handler, authority := governanceHTTPHarness(t)
	w := governanceRequest(t, handler, http.MethodGet, "/tenants/1/governance-catalog", nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"verification_override":"UNAVAILABLE"`)
	require.Contains(t, w.Body.String(), `"settings":"AVAILABLE"`)
	authority.allow = false
	w = governanceRequest(t, handler, http.MethodGet, "/tenants/1/governance-catalog", nil, "")
	require.Equal(t, 403, w.Code)
	_, err := (*Module)(nil).getGovernanceHTTP(t.Context(), nil)
	require.Error(t, err)
	m.policies.sealed = false
	_, err = m.getGovernanceHTTP(t.Context(), nil)
	require.Error(t, err)
	m.policies.sealed = true
	m.policies.rules = []PolicyRule{{ID: "actual", Codes: []string{"policy_read"}}}
	out, err := m.getGovernanceHTTP(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []GovernancePolicyView{{ID: "actual", Codes: []string{"policy_read"}}}, out.Body.Data.Policies)
}
