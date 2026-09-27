package core

import (
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

const operatingPlatformAuth = "Authorization: Bearer test"

// newOperatingPlatformHTTPClient serves the operating platform routes through
// the real authorizer and membership checker, so every guard decision below
// comes from stored platform grants rather than a stub.
func newOperatingPlatformHTTPClient(t *testing.T, h coreServiceHarness, verifier *systemAccessVerifier) humatest.TestAPI {
	t.Helper()
	require.NoError(t, initializeHTTPLocales())
	members := NewMembershipChecker(h.runtime)
	require.NoError(t, members.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, members)
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("operating-platform-test", "1"))
	registerOperatingPlatformHTTP(api, h.service, registrar)
	return humatest.Wrap(t, api)
}

func TestOperatingPlatformHTTPOwnerAdministersRolesMenusAndSelfService(t *testing.T) {
	h := newCoreServiceHarness(t)
	platform := seedOperatingPlatform(t, h, 1, 11)
	client := newOperatingPlatformHTTPClient(t, h, &systemAccessVerifier{principal: 11})
	const base = "/operating-platforms/1"

	response := client.Get(base+"/roles", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	roles := decodeEnvelope[[]APIRole](t, response)
	require.Len(t, roles.Data, 1)
	require.Equal(t, platform.AdministratorRoleID, roles.Data[0].ID)
	require.Equal(t, guid.ID(1), roles.Data[0].TenantID)
	response = client.Post(base+"/roles", operatingPlatformAuth, map[string]any{"name": "Auditors", "description": "Read only"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	created := decodeEnvelope[APIRole](t, response).Data
	require.Equal(t, "Auditors", created.Name)
	require.Equal(t, guid.ID(1), created.TenantID, "the role is owned by the platform in the path")
	response = client.Get(base+"/roles?limit=1&offset=1", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	page := decodeEnvelope[[]APIRole](t, response)
	require.Len(t, page.Data, 1)
	require.NotNil(t, page.Meta.Page)
	require.EqualValues(t, 2, page.Meta.Page.Total)

	response = client.Get(base+"/permission-catalog", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	catalog := decodeEnvelope[[]authz.Action](t, response).Data
	codes := map[string]bool{}
	for _, action := range catalog {
		require.True(t, authz.IsOperatingPlatformPermission(action), action.Code)
		codes[action.Code] = true
	}
	require.True(t, codes["role_view"])
	require.True(t, codes["platform_member_create"])
	require.False(t, codes["tenant_create"], "system permissions never leak into a platform catalog")
	require.False(t, codes["entity_view"], "tenant permissions never leak into a platform catalog")
	require.Len(t, catalog, len(h.service.OperatingPlatformPermissionCatalog(t.Context())))

	response = client.Get(base+"/menus", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	menus := decodeEnvelope[[]APIMenu](t, response).Data
	require.Len(t, menus, 7)
	var organization guid.ID
	for _, menu := range menus {
		require.Equal(t, guid.ID(1), menu.TenantID)
		if menu.Label == "Organization" {
			organization = menu.ID
		}
	}
	require.NotZero(t, organization)
	response = client.Post(base+"/menus", operatingPlatformAuth, map[string]any{"label": "Reports", "parent_id": organization.String(), "route": "/platform/reports", "permission_code": "role_view", "sort_order": 90})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	menu := decodeEnvelope[APIMenu](t, response).Data
	require.Equal(t, organization, menu.ParentID)
	require.Equal(t, "role_view", menu.PermissionCode)
	require.Equal(t, MenuActive, menu.Status)
	response = client.Post(base+"/menus", operatingPlatformAuth, map[string]any{"label": "Orphan", "parent_id": "987654321"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	stored, err := h.service.ListMenus(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, stored, 8, "a rejected menu is not persisted")

	response = client.Get(base+"/navigation", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	navigation := decodeEnvelope[[]MenuItem](t, response).Data
	require.Len(t, navigation, 2)
	require.Equal(t, "Organization", navigation[0].Label)
	paths := []string{}
	for _, item := range navigation[0].Children {
		paths = append(paths, item.Path)
	}
	require.Contains(t, paths, "/platform/reports")
	response = client.Get(base+"/permissions", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	granted := decodeEnvelope[[]string](t, response).Data
	require.Contains(t, granted, "operating_platform_administer")
	require.Contains(t, granted, "role_create")
	require.NotContains(t, granted, "tenant_create")
}

func TestOperatingPlatformHTTPMemberLifecycle(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	client := newOperatingPlatformHTTPClient(t, h, &systemAccessVerifier{principal: 11})
	const base = "/operating-platforms/1/members"

	response := client.Get(base, operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	listed := decodeEnvelope[[]APITenantMember](t, response)
	require.Len(t, listed.Data, 1)
	require.Equal(t, guid.ID(11), listed.Data[0].Subject)
	require.Equal(t, MemberActive, listed.Data[0].Status)

	response = client.Post(base, operatingPlatformAuth, map[string]any{"subject": "22", "display_name": "Operator", "email": "operator@example.test"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	member := decodeEnvelope[APITenantMember](t, response).Data
	require.Equal(t, guid.ID(1), member.TenantID)
	require.Equal(t, guid.ID(22), member.Subject)
	require.Equal(t, "Operator", member.DisplayName)
	require.Equal(t, MemberActive, member.Status)
	response = client.Post(base, operatingPlatformAuth, map[string]any{"subject": "22", "display_name": "Duplicate"})
	require.Equal(t, http.StatusConflict, response.Code, "create binds an account once: %s", response.Body.String())
	response = client.Post(base, operatingPlatformAuth, map[string]any{"subject": "0", "display_name": "Zero"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())

	response = client.Get(base+"?search=Operator&status=active", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	filtered := decodeEnvelope[[]APITenantMember](t, response).Data
	require.Len(t, filtered, 1)
	require.Equal(t, guid.ID(22), filtered[0].Subject)

	response = client.Get(base+"/22", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "operator@example.test", decodeEnvelope[APITenantMember](t, response).Data.Email)
	require.Equal(t, http.StatusNotFound, client.Get(base+"/987654321", operatingPlatformAuth).Code)
	require.Equal(t, http.StatusUnprocessableEntity, client.Get(base+"/abc", operatingPlatformAuth).Code)

	response = client.Patch(base+"/22", operatingPlatformAuth, map[string]any{"display_name": "Renamed operator"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	updated := decodeEnvelope[APITenantMember](t, response).Data
	require.Equal(t, "Renamed operator", updated.DisplayName)
	require.Equal(t, MemberActive, updated.Status)
	response = client.Patch(base+"/987654321", operatingPlatformAuth, map[string]any{"display_name": "Ghost"})
	require.Equal(t, http.StatusNotFound, response.Code, "update never creates a membership: %s", response.Body.String())
	_, err := h.service.GetTenantMember(t.Context(), 1, 987654321)
	require.Error(t, err)

	response = client.Delete(base+"/22", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, MemberDisabled, decodeEnvelope[APITenantMember](t, response).Data.Status)
	stored, err := h.service.GetOperatingPlatformMember(t.Context(), 1, 22)
	require.NoError(t, err)
	require.Equal(t, MemberDisabled, stored.Status, "delete disables rather than removes the membership")
	active, err := NewMembershipChecker(h.runtime).IsOperatingPlatformMemberActive(t.Context(), 1, 22)
	require.ErrorIs(t, err, ErrUnavailable, "an unbound checker fails closed")
	require.False(t, active)
}

func TestOperatingPlatformHTTPRejectsOutsidersUngrantedMembersAndForeignViews(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	seedOperatingPlatform(t, h, 2, 33)
	h.seedMember(t, 3, 44, MemberActive)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	_, err := h.service.PutOperatingPlatformMember(t.Context(), 1, 22, "Plain member", "", 0, MemberActive)
	require.NoError(t, err)
	verifier := &systemAccessVerifier{principal: 22}
	client := newOperatingPlatformHTTPClient(t, h, verifier)
	const base = "/operating-platforms/1"

	response := client.Get(base+"/navigation", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, decodeEnvelope[[]MenuItem](t, response).Data, "an ungranted member sees no navigation")
	response = client.Get(base+"/permissions", operatingPlatformAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, decodeEnvelope[[]string](t, response).Data)
	for name, request := range map[string]func() int{
		"list roles": func() int { return client.Get(base+"/roles", operatingPlatformAuth).Code },
		"create role": func() int {
			return client.Post(base+"/roles", operatingPlatformAuth, map[string]any{"name": "Escalated"}).Code
		},
		"catalog":    func() int { return client.Get(base+"/permission-catalog", operatingPlatformAuth).Code },
		"list menus": func() int { return client.Get(base+"/menus", operatingPlatformAuth).Code },
		"create menu": func() int {
			return client.Post(base+"/menus", operatingPlatformAuth, map[string]any{"label": "Injected"}).Code
		},
		"list members": func() int { return client.Get(base+"/members", operatingPlatformAuth).Code },
		"get member":   func() int { return client.Get(base+"/members/11", operatingPlatformAuth).Code },
		"add member": func() int {
			return client.Post(base+"/members", operatingPlatformAuth, map[string]any{"subject": "44", "display_name": "Added"}).Code
		},
		"update owner": func() int {
			return client.Patch(base+"/members/11", operatingPlatformAuth, map[string]any{"display_name": "Hijacked"}).Code
		},
		"disable owner": func() int { return client.Delete(base+"/members/11", operatingPlatformAuth).Code },
	} {
		require.Equal(t, http.StatusForbidden, request(), name)
	}
	roles, err := h.service.ListRoles(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, roles, 1, "denied role creation leaves the platform unchanged")
	menus, err := h.service.ListMenus(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, menus, 7)
	owner, err := h.service.GetOperatingPlatformMember(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, "Owner", owner.DisplayName)
	require.Equal(t, MemberActive, owner.Status)
	_, err = h.service.GetOperatingPlatformMember(t.Context(), 1, 44)
	require.ErrorIs(t, err, ErrMemberNotFound)

	verifier.principal = 11
	require.Equal(t, http.StatusForbidden, client.Get("/operating-platforms/2/roles", operatingPlatformAuth).Code, "owning one platform grants nothing in another")
	require.Equal(t, http.StatusForbidden, client.Get("/operating-platforms/3/roles", operatingPlatformAuth).Code, "a tenant root is not an operating platform")
	require.Equal(t, http.StatusForbidden, client.Get("/operating-platforms/01/roles", operatingPlatformAuth).Code, "non-canonical identifiers are refused")
	verifier.view = &authn.AccountViewIdentity{ID: 50, ActorID: 11, ScopeType: "platform", ScopeID: 2}
	require.Equal(t, http.StatusForbidden, client.Get(base+"/roles", operatingPlatformAuth).Code, "a view of another platform cannot reach this one")
	verifier.view = &authn.AccountViewIdentity{ID: 50, ActorID: 11, ScopeType: "tenant", ScopeID: 1}
	require.Equal(t, http.StatusForbidden, client.Get(base+"/navigation", operatingPlatformAuth).Code)
	verifier.view = &authn.AccountViewIdentity{ID: 50, ActorID: 99, ScopeType: "platform", ScopeID: 1}
	require.Equal(t, http.StatusOK, client.Get(base+"/roles", operatingPlatformAuth).Code, "a view scoped to this platform keeps the viewed member's grants")
}
