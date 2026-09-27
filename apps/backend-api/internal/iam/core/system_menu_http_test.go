package core

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

type systemMenuVerifier struct{ view *authn.AccountViewIdentity }

func (v *systemMenuVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal, AccountView: v.view}, nil
}

func TestSystemMenuHTTPBoundaryUsesEffectiveGlobalGrants(t *testing.T) {
	h := seedSystemMenuHarness(t)
	require.NoError(t, initializeHTTPLocales())
	verifier := &systemMenuVerifier{}
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, httpMembershipChecker{})
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("system-menu-test", "1"))
	registerSystemMenuHTTP(api, h.service, registrar)
	client := humatest.Wrap(t, api)
	response := client.Get("/system/navigation", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, decodeEnvelope[[]MenuItem](t, response).Data)
	require.Equal(t, http.StatusForbidden, client.Get("/system/menus", "Authorization: Bearer test").Code)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants (principal_id,permission_code,created_at) VALUES (11,'system_menu_view',?),(11,'system_menu_create',?),(11,'system_menu_update',?)", repositoryTestTime, repositoryTestTime, repositoryTestTime).Error)
	response = client.Get("/system/menus", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, decodeEnvelope[[]APIMenu](t, response).Data, 11)
	response = client.Post("/system/menus", "Authorization: Bearer test", map[string]any{"label": "Bound", "route": "/system/menus", "permission_code": "system_menu_view"})
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = client.Post("/system/menus", "Authorization: Bearer test", map[string]any{"label": "New branch"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	menu := decodeEnvelope[APIMenu](t, response).Data
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants (principal_id,permission_code,created_at) VALUES (11,'system_menu_bind_permission',?)", repositoryTestTime).Error)
	response = client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"route": "/system/custom", "permission_code": "system_menu_view"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, scope := range []string{"tenant", "platform", "personal"} {
		verifier.view = &authn.AccountViewIdentity{ID: 44, PrincipalID: 22, ScopeType: scope, ScopeID: 1}
		require.Equal(t, http.StatusForbidden, client.Get("/system/navigation", "Authorization: Bearer test").Code, scope)
		require.Equal(t, http.StatusForbidden, client.Get("/system/menus", "Authorization: Bearer test").Code, scope)
	}
	verifier.view = &authn.AccountViewIdentity{ID: 44, PrincipalID: 22, ScopeType: "system", ScopeID: 1, ReadOnly: true}
	require.Equal(t, http.StatusOK, client.Get("/system/navigation", "Authorization: Bearer test").Code)
	require.Equal(t, http.StatusForbidden, client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"label": "Denied"}).Code)
	verifier.view.ReadOnly = false
	require.Equal(t, http.StatusOK, client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"label": "Writable view"}).Code)
	verifier.view.ScopeID = 99
	require.Equal(t, http.StatusForbidden, client.Get("/system/navigation", "Authorization: Bearer test").Code)
}
