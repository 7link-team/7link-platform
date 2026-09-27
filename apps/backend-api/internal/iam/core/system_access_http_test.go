package core

import (
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const systemAccessAuth = "Authorization: Bearer test"

// newSystemAccessHTTPClient serves the system role and member routes for a
// full system administrator (11) over the real authorizer.
func newSystemAccessHTTPClient(t *testing.T) (coreServiceHarness, humatest.TestAPI, *systemAccessVerifier) {
	t.Helper()
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	seedHTTPPrincipal(t, h.repositoryHarness, 33)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	require.NoError(t, initializeHTTPLocales())
	verifier := &systemAccessVerifier{principal: 11}
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("system-access-http-test", "1"))
	registerSystemAccessHTTP(api, h.service, registrar)
	return h, humatest.Wrap(t, api), verifier
}

func systemBuiltinRole(t *testing.T, client humatest.TestAPI) APISystemRole {
	t.Helper()
	response := client.Get("/system/roles", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, role := range decodeEnvelope[[]APISystemRole](t, response).Data {
		if role.Builtin {
			return role
		}
	}
	t.Fatal("system superadministrator role is missing")
	return APISystemRole{}
}

func TestSystemRoleHTTPLifecycleAndPermissionGrants(t *testing.T) {
	h, client, _ := newSystemAccessHTTPClient(t)
	full := systemBuiltinRole(t, client)
	response := client.Get("/system/roles/"+full.ID.String(), systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[APISystemRole](t, response).Data.Builtin)
	require.Equal(t, http.StatusNotFound, client.Get("/system/roles/987654321", systemAccessAuth).Code)

	response = client.Post("/system/roles", systemAccessAuth, map[string]any{"name": "__System_forged"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, "reserved names are refused case-insensitively: %s", response.Body.String())
	roles, err := h.service.ListRoles(t.Context(), 99)
	require.NoError(t, err)
	require.Len(t, roles, 1, "a refused role is not persisted")

	response = client.Post("/system/roles", systemAccessAuth, map[string]any{"name": "Operators", "description": "Day shift"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	role := decodeEnvelope[APISystemRole](t, response).Data
	require.False(t, role.Builtin)
	require.Equal(t, guid.ID(99), role.TenantID)
	response = client.Patch("/system/roles/"+role.ID.String(), systemAccessAuth, map[string]any{"description": "Night shift"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "Night shift", decodeEnvelope[APISystemRole](t, response).Data.Description)
	require.Equal(t, "Operators", decodeEnvelope[APISystemRole](t, response).Data.Name)

	response = client.Get("/system/role-permission-catalog", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	catalog := map[string]bool{}
	for _, action := range decodeEnvelope[[]authz.Action](t, response).Data {
		require.Equal(t, "platform", action.Scope, action.Code)
		catalog[action.Code] = true
	}
	require.True(t, catalog["system_menu_view"])
	require.False(t, catalog["platform_administer"], "full authority is never assignable through a role")

	response = client.Get("/system/roles/"+full.ID.String()+"/permission-grants", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, decodeEnvelope[[]APIRolePermissionGrant](t, response).Data, len(h.service.PlatformPermissionCatalog(t.Context())), "the builtin role reports the whole platform catalog")
	require.Equal(t, http.StatusNotFound, client.Get("/system/roles/987654321/permission-grants", systemAccessAuth).Code)

	grant := func(method, code string) (int, MutationResult) {
		path := "/system/roles/" + role.ID.String() + "/permissions/" + code
		response := client.Put(path, systemAccessAuth)
		if method == http.MethodDelete {
			response = client.Delete(path, systemAccessAuth)
		}
		if response.Code != http.StatusOK {
			return response.Code, MutationResult{}
		}
		return response.Code, decodeEnvelope[MutationResult](t, response).Data
	}
	code, result := grant(http.MethodPut, "system_menu_view")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, MutationResult{Changed: true, SyncStatus: "applied"}, result)
	code, result = grant(http.MethodPut, "system_menu_view")
	require.Equal(t, http.StatusOK, code)
	require.False(t, result.Changed, "granting twice is idempotent")
	code, _ = grant(http.MethodPut, "platform_administer")
	require.Equal(t, http.StatusForbidden, code)
	code, _ = grant(http.MethodDelete, "platform_administer")
	require.Equal(t, http.StatusForbidden, code, "only imported legacy roles may shed full authority")
	response = client.Get("/system/roles/"+role.ID.String()+"/permission-grants", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	grants := decodeEnvelope[[]APIRolePermissionGrant](t, response).Data
	require.Len(t, grants, 1)
	require.Equal(t, "system_menu_view", grants[0].PermissionCode)
	code, result = grant(http.MethodDelete, "system_menu_view")
	require.Equal(t, http.StatusOK, code)
	require.True(t, result.Changed)
	permissions, err := h.repository.ListPermissions(t.Context(), 99, role.ID)
	require.NoError(t, err)
	require.Empty(t, permissions)
	require.Equal(t, http.StatusNotFound, client.Put("/system/roles/987654321/permissions/system_menu_view", systemAccessAuth).Code)

	response = client.Delete("/system/roles/"+role.ID.String(), systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[MutationResult](t, response).Data.Changed)
	require.Equal(t, http.StatusNotFound, client.Get("/system/roles/"+role.ID.String(), systemAccessAuth).Code)
	require.Equal(t, http.StatusNotFound, client.Delete("/system/roles/"+role.ID.String(), systemAccessAuth).Code)
	require.Equal(t, http.StatusNotFound, client.Patch("/system/roles/987654321", systemAccessAuth, map[string]any{"description": "Ghost"}).Code)
}

func TestSystemRoleHTTPMembershipAndSuperadministratorAssignment(t *testing.T) {
	h, client, verifier := newSystemAccessHTTPClient(t)
	full := systemBuiltinRole(t, client)
	response := client.Post("/system/roles", systemAccessAuth, map[string]any{"name": "Readers"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	role := decodeEnvelope[APISystemRole](t, response).Data
	response = client.Post("/system/members", systemAccessAuth, map[string]any{"principal_id": "22", "display_name": "Reader"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	response = client.Put("/system/roles/"+role.ID.String()+"/members/22", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[MutationResult](t, response).Data.Changed)
	response = client.Get("/system/roles/"+role.ID.String()+"/members", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []guid.ID{22}, decodeEnvelope[[]guid.ID](t, response).Data)
	response = client.Get("/system/roles/"+full.ID.String()+"/members", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []guid.ID{11}, decodeEnvelope[[]guid.ID](t, response).Data, "builtin membership is the full administrator table")
	require.Equal(t, http.StatusNotFound, client.Get("/system/roles/987654321/members", systemAccessAuth).Code)
	require.Equal(t, http.StatusNotFound, client.Put("/system/roles/987654321/members/22", systemAccessAuth).Code)

	response = client.Delete("/system/roles/"+role.ID.String()+"/members/22", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[MutationResult](t, response).Data.Changed)
	members, _, err := h.repository.ListRoleMembersPage(t.Context(), 99, role.ID, 0, 50)
	require.NoError(t, err)
	require.Empty(t, members)

	response = client.Put("/system/roles/"+full.ID.String()+"/members/33", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[MutationResult](t, response).Data.Changed)
	isFull, err := h.repository.isFullSystemAdministrator(t.Context(), 33)
	require.NoError(t, err)
	require.True(t, isFull, "assigning the builtin role grants full authority")
	response = client.Get("/system/members/33", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	promoted := decodeEnvelope[APISystemMember](t, response).Data
	require.True(t, promoted.Protected)
	require.Equal(t, []guid.ID{full.ID}, promoted.RoleIDs)
	require.Equal(t, http.StatusForbidden, client.Patch("/system/members/33", systemAccessAuth, map[string]any{"status": "disabled"}).Code, "a full administrator cannot be disabled")
	stillActive, err := h.service.GetTenantMember(t.Context(), 99, 33)
	require.NoError(t, err)
	require.Equal(t, MemberActive, stillActive.Status)

	response = client.Delete("/system/roles/"+full.ID.String()+"/members/33", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, decodeEnvelope[MutationResult](t, response).Data.Changed)
	isFull, err = h.repository.isFullSystemAdministrator(t.Context(), 33)
	require.NoError(t, err)
	require.False(t, isFull)
	response = client.Delete("/system/roles/"+full.ID.String()+"/members/11", systemAccessAuth)
	require.Equal(t, http.StatusConflict, response.Code, "the last full administrator is kept: %s", response.Body.String())
	isFull, err = h.repository.isFullSystemAdministrator(t.Context(), 11)
	require.NoError(t, err)
	require.True(t, isFull)

	seedSystemPermissions(t, h, 22, "system_role_manage_member")
	verifier.principal = 22
	require.Equal(t, http.StatusForbidden, client.Delete("/system/roles/"+full.ID.String()+"/members/11", systemAccessAuth).Code, "only a full administrator manages full authority")
	isFull, err = h.repository.isFullSystemAdministrator(t.Context(), 11)
	require.NoError(t, err)
	require.True(t, isFull)
}

func TestSystemMemberHTTPListCreateUpdateAndLookup(t *testing.T) {
	h, client, _ := newSystemAccessHTTPClient(t)
	response := client.Post("/system/members", systemAccessAuth, map[string]any{"principal_id": "22", "display_name": "Reader", "email": "reader@example.test"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	created := decodeEnvelope[APISystemMember](t, response).Data
	require.Equal(t, guid.ID(22), created.Subject)
	require.Equal(t, guid.ID(99), created.TenantID)
	require.False(t, created.Protected)
	require.Empty(t, created.RoleIDs)
	response = client.Post("/system/members", systemAccessAuth, map[string]any{"principal_id": "22", "display_name": "Again"})
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	stored, err := h.service.GetTenantMember(t.Context(), 99, 22)
	require.NoError(t, err)
	require.Equal(t, "Reader", stored.DisplayName, "a duplicate create does not overwrite the membership")

	response = client.Get("/system/members?search=reader&status=active", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	listed := decodeEnvelope[[]APISystemMember](t, response)
	require.Len(t, listed.Data, 1)
	require.Equal(t, guid.ID(22), listed.Data[0].Subject)
	require.EqualValues(t, 1, listed.Meta.Page.Total)
	response = client.Get("/system/members?status=disabled", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, decodeEnvelope[[]APISystemMember](t, response).Data)

	response = client.Patch("/system/members/22", systemAccessAuth, map[string]any{"display_name": "Senior reader", "email": "senior@example.test", "department_id": ""})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	updated := decodeEnvelope[APISystemMember](t, response).Data
	require.Equal(t, "Senior reader", updated.DisplayName)
	require.Equal(t, "senior@example.test", updated.Email)
	require.Zero(t, updated.DepartmentID)
	require.Equal(t, MemberActive, updated.Status)
	response = client.Patch("/system/members/22", systemAccessAuth, map[string]any{"email": "not an address"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	stored, err = h.service.GetTenantMember(t.Context(), 99, 22)
	require.NoError(t, err)
	require.Equal(t, "senior@example.test", stored.Email, "a rejected update leaves the membership unchanged")

	require.Equal(t, http.StatusNotFound, client.Get("/system/members/987654321", systemAccessAuth).Code)
	require.Equal(t, http.StatusNotFound, client.Get("/system/members/987654321/roles", systemAccessAuth).Code)
	require.Equal(t, http.StatusNotFound, client.Patch("/system/members/33", systemAccessAuth, map[string]any{"display_name": "Not a member"}).Code)
	_, err = h.service.GetTenantMember(t.Context(), 99, 33)
	require.ErrorIs(t, err, ErrMemberNotFound, "update never creates a system membership")
	response = client.Get("/system/members/11/roles", systemAccessAuth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Len(t, decodeEnvelope[[]guid.ID](t, response).Data, 1, "the full administrator reports the builtin role")
}
