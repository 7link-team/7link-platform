package core

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
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
	seedSystemPermissions(t, h, 11, "system_menu_view", "system_menu_create", "system_menu_update")
	response = client.Get("/system/menus", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, decodeEnvelope[[]APIMenu](t, response).Data, 18)
	response = client.Post("/system/menus", "Authorization: Bearer test", map[string]any{"label": "Bound", "route": "/system/menus", "permission_code": "system_menu_view"})
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = client.Post("/system/menus", "Authorization: Bearer test", map[string]any{"label": "New branch"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	menu := decodeEnvelope[APIMenu](t, response).Data
	seedSystemPermissions(t, h, 11, "system_menu_bind_permission")
	response = client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"route": "/system/custom", "permission_code": "system_menu_view"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, scope := range []string{"tenant", "platform", "personal"} {
		verifier.view = &authn.AccountViewIdentity{ID: 44, ActorID: 22, ScopeType: scope, ScopeID: 1}
		require.Equal(t, http.StatusForbidden, client.Get("/system/navigation", "Authorization: Bearer test").Code, scope)
		require.Equal(t, http.StatusForbidden, client.Get("/system/menus", "Authorization: Bearer test").Code, scope)
	}
	verifier.view = &authn.AccountViewIdentity{ID: 44, ActorID: 22, ScopeType: "system", ScopeID: 1, ReadOnly: true}
	require.Equal(t, http.StatusOK, client.Get("/system/navigation", "Authorization: Bearer test").Code)
	require.Equal(t, http.StatusForbidden, client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"label": "Denied"}).Code)
	verifier.view.ReadOnly = false
	require.Equal(t, http.StatusOK, client.Patch("/system/menus/"+menu.ID.String(), "Authorization: Bearer test", map[string]any{"label": "Writable view"}).Code)
	verifier.view.ScopeID = 99
	require.Equal(t, http.StatusForbidden, client.Get("/system/navigation", "Authorization: Bearer test").Code)
}

func newSystemMenuHTTPClient(t *testing.T, h coreServiceHarness) humatest.TestAPI {
	t.Helper()
	require.NoError(t, initializeHTTPLocales())
	registrar := authz.NewRegistrar(h.service.registry, &systemMenuVerifier{}, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("system-menu-crud-test", "1"))
	registerSystemMenuHTTP(api, h.service, registrar)
	return humatest.Wrap(t, api)
}

func TestSystemMenuHTTPCatalogMirrorsSystemDefaults(t *testing.T) {
	h := seedSystemMenuHarness(t)
	client := newSystemMenuHTTPClient(t, h)
	require.Equal(t, http.StatusForbidden, client.Get("/system/menu-catalog", "Authorization: Bearer test").Code, "catalog needs system_menu_view")
	seedSystemPermissions(t, h, 11, "system_menu_view")
	response := client.Get("/system/menu-catalog", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	catalog := decodeEnvelope[[]MenuDeclaration](t, response).Data
	require.Equal(t, h.service.SystemMenuCatalog(t.Context()), catalog)
	labels := make([]string, 0, len(catalog))
	children := map[string][]string{}
	for _, branch := range catalog {
		labels = append(labels, branch.Label)
		require.Equal(t, branch.Label, branch.Key)
		require.Empty(t, branch.Path, "branches are not routable")
		require.Empty(t, branch.PermissionCode, "branches carry no permission floor")
		require.Len(t, branch.Translations, 3)
		for _, leaf := range branch.Children {
			require.Equal(t, leaf.Path, leaf.Key)
			require.NotEmpty(t, leaf.PermissionCode, leaf.Path)
			children[branch.Label] = append(children[branch.Label], leaf.Path)
		}
	}
	require.Equal(t, []string{"Account management", "Permission management", "Basic management", "Organization management", "Audit"}, labels)
	require.Equal(t, []string{"/system/accounts/system", "/system/accounts/platform", "/system/accounts/tenant", "/system/accounts", "/system/closure-requests"}, children["Account management"])
	require.Equal(t, []string{"/system/menus", "/system/files"}, children["Basic management"])
	require.Equal(t, []string{"/system/audit/events", "/system/audit-governance"}, children["Audit"])
}

func TestSystemMenuHTTPReadUpdateMoveAndDeleteStayInsideSystemRoot(t *testing.T) {
	h := seedSystemMenuHarness(t)
	client := newSystemMenuHTTPClient(t, h)
	const auth = "Authorization: Bearer test"
	seedSystemPermissions(t, h, 11, "system_menu_view", "system_menu_update", "system_menu_create")
	menus, err := h.repository.ListMenus(t.Context(), 99, false)
	require.NoError(t, err)
	byRoute, byLabel := map[string]Menu{}, map[string]Menu{}
	for _, menu := range menus {
		if menu.Route != "" {
			byRoute[menu.Route] = menu
		} else {
			byLabel[menu.Label] = menu
		}
	}
	files := byRoute["/system/files"]
	response := client.Get("/system/menus/"+files.ID.String(), auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	got := decodeEnvelope[APIMenu](t, response).Data
	require.Equal(t, files.ID, got.ID)
	require.Equal(t, guid.ID(99), got.TenantID)
	require.Equal(t, "system_file_view", got.PermissionCode)
	require.Equal(t, http.StatusNotFound, client.Get("/system/menus/987654321", auth).Code)
	tenantMenu, err := h.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "Tenant menu", Status: MenuActive})
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, client.Get("/system/menus/"+tenantMenu.ID.String(), auth).Code, "a tenant menu is not addressable through the system root")
	require.Equal(t, http.StatusNotFound, client.Patch("/system/menus/"+tenantMenu.ID.String(), auth, map[string]any{"label": "Hijacked"}).Code)
	storedTenant, err := h.service.GetMenu(t.Context(), 1, tenantMenu.ID)
	require.NoError(t, err)
	require.Equal(t, "Tenant menu", storedTenant.Label)

	response = client.Patch("/system/menus/"+files.ID.String(), auth, map[string]any{
		"label": "Files", "icon": "archive", "sort_order": 5, "status": "disabled",
		"translations": map[string]string{"en-US": "Files"}, "parent_id": byLabel["Audit"].ID.String(),
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	updated := decodeEnvelope[APIMenu](t, response).Data
	require.Equal(t, "Files", updated.Label)
	require.Equal(t, "archive", updated.Icon)
	require.Equal(t, 5, updated.SortOrder)
	require.Equal(t, MenuDisabled, updated.Status)
	require.Equal(t, byLabel["Audit"].ID, updated.ParentID)
	require.Equal(t, map[string]string{"en-US": "Files"}, updated.Translations)
	require.Equal(t, "/system/files", updated.Route, "omitted fields are preserved")
	require.Equal(t, "system_file_view", updated.PermissionCode)

	response = client.Patch("/system/menus/"+files.ID.String(), auth, map[string]any{"permission_code": "system_menu_view"})
	require.Equal(t, http.StatusForbidden, response.Code, "rebinding a permission needs system_menu_bind_permission")
	response = client.Patch("/system/menus/"+files.ID.String(), auth, map[string]any{"placement": map[string]any{"kind": "first"}, "label": "Mixed"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	stored, err := h.service.GetMenu(t.Context(), 99, files.ID)
	require.NoError(t, err)
	require.Equal(t, "Files", stored.Label, "rejected edits leave the menu unchanged")
	require.Equal(t, "system_file_view", stored.PermissionCode)

	menusNode := byRoute["/system/menus"]
	response = client.Patch("/system/menus/"+files.ID.String(), auth, map[string]any{
		"parent_id": byLabel["Basic management"].ID.String(), "placement": map[string]any{"kind": "before", "relative_to": menusNode.ID.String()},
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	moved := decodeEnvelope[APIMenu](t, response).Data
	require.Equal(t, byLabel["Basic management"].ID, moved.ParentID)
	movedMenus, err := h.repository.ListMenus(t.Context(), 99, false)
	require.NoError(t, err)
	order := map[guid.ID]int{}
	for _, menu := range movedMenus {
		order[menu.ID] = menu.SortOrder
	}
	require.Less(t, order[files.ID], order[menusNode.ID], "before placement orders the subtree ahead of its sibling")
	response = client.Patch("/system/menus/"+files.ID.String(), auth, map[string]any{"placement": map[string]any{"kind": "before"}})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, "before without a sibling is invalid")

	require.Equal(t, http.StatusForbidden, client.Delete("/system/menus/"+files.ID.String(), auth).Code, "delete needs system_menu_delete")
	_, err = h.service.GetMenu(t.Context(), 99, files.ID)
	require.NoError(t, err)
	seedSystemPermissions(t, h, 11, "system_menu_delete")
	require.Equal(t, http.StatusNotFound, client.Delete("/system/menus/"+tenantMenu.ID.String(), auth).Code)
	_, err = h.service.GetMenu(t.Context(), 1, tenantMenu.ID)
	require.NoError(t, err, "a tenant menu survives a system delete attempt")
	response = client.Delete("/system/menus/"+files.ID.String(), auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, MutationResult{Changed: true, SyncStatus: "not_applicable"}, decodeEnvelope[MutationResult](t, response).Data)
	require.Equal(t, http.StatusNotFound, client.Get("/system/menus/"+files.ID.String(), auth).Code)
	event := (*h.events)[len(*h.events)-1]
	require.Equal(t, guid.ID(0), event.TenantID, "system menu audit is not attributed to a tenant")
	require.Equal(t, "system", event.Detail["scope_kind"])
	require.Equal(t, "99", event.Detail["scope_id"])
}
