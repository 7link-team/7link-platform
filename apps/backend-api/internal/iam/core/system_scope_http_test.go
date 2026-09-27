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

func TestSystemScopeHTTPGlobalAdministratorManagesForeignMemberships(t *testing.T) {
	for _, kind := range []string{"tenant", "platform"} {
		t.Run(kind, func(t *testing.T) {
			h := seedSystemMenuHarness(t)
			seedHTTPPrincipal(t, h.repositoryHarness, 22)
			seedHTTPPrincipal(t, h.repositoryHarness, 33)
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(2,?,'scope-owner','Scope owner','active',1,?,?)", kind, repositoryTestTime, repositoryTestTime).Error)
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
			bootstrapSystemTestAccess(t, h)
			require.NoError(t, initializeHTTPLocales())
			v := &systemAccessVerifier{principal: 11}
			r := authz.NewRegistrar(h.service.registry, v, h.authorizer, httpMembershipChecker{})
			api := humax.New(chi.NewRouter(), humax.DefaultConfig("scope-test", "1"))
			registerSystemScopeHTTP(api, h.service, r)
			registerSystemAccessHTTP(api, h.service, r)
			client := humatest.Wrap(t, api)
			base := "/system/tenants/2"
			permission := "user_view"
			if kind == "platform" {
				base = "/system/operating-platforms/2"
				permission = "platform_member_view"
			}
			role, err := h.repository.CreateRole(t.Context(), 2, "Scoped reader", "")
			require.NoError(t, err)
			_, err = h.repository.GrantPermission(t.Context(), 2, role.ID, permission)
			require.NoError(t, err)
			foreign, err := h.repository.CreateRole(t.Context(), 1, "Foreign", "")
			require.NoError(t, err)
			response := client.Post(base+"/members", "Authorization: Bearer test", map[string]any{"subject": "22", "display_name": "Managed account"})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, guid.ID(2), decodeEnvelope[APITenantMember](t, response).Data.TenantID)
			response = client.Put(base+"/roles/"+role.ID.String()+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			response = client.Get(base+"/members/22/roles?limit=1", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []string{role.ID.String()}, decodeEnvelope[[]string](t, response).Data)
			response = client.Get(base+"/members", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			members := decodeEnvelope[[]APITenantMember](t, response).Data
			require.Len(t, members, 1)
			require.Equal(t, guid.ID(22), members[0].Subject)
			require.Equal(t, []guid.ID{role.ID}, members[0].RoleIDs)
			require.Equal(t, http.StatusNotFound, client.Put(base+"/roles/"+foreign.ID.String()+"/members/22", "Authorization: Bearer test").Code)
			require.Equal(t, http.StatusNotFound, client.Get(base+"/members/33", "Authorization: Bearer test").Code)
			require.Equal(t, http.StatusNotFound, client.Post(base+"/members", "Authorization: Bearer test", map[string]any{"subject": "999", "display_name": "Missing"}).Code)
			require.Equal(t, http.StatusNotFound, client.Get("/system/tenants/99/members", "Authorization: Bearer test").Code)
			response = client.Get(base+"/members?limit=1&offset=1", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Empty(t, decodeEnvelope[[]APITenantMember](t, response).Data)
			response = client.Patch(base+"/members/22", "Authorization: Bearer test", map[string]any{"display_name": "Renamed"})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "Renamed", decodeEnvelope[APITenantMember](t, response).Data.DisplayName)
			response = client.Delete(base+"/roles/"+role.ID.String()+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			response = client.Delete(base+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			audited := 0
			for _, event := range *h.events {
				if event.TargetID == 22 && event.Detail["scope_kind"] == kind {
					audited++
					require.Zero(t, event.TenantID)
					require.Equal(t, "2", event.Detail["scope_id"])
				}
			}
			require.Positive(t, audited)
			seedSystemPermissions(t, h, 33, "system_account_view")
			v.principal = 33
			response = client.Get("/system/account-owners?category="+kind+"&limit=1", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			owners := decodeEnvelope[[]APISystemAccountOwner](t, response).Data
			require.Len(t, owners, 1)
			if kind == "platform" {
				require.Equal(t, guid.ID(2), owners[0].ID)
			}
			for _, owner := range owners {
				require.NotEqual(t, guid.ID(99), owner.ID)
			}
			require.Equal(t, http.StatusUnprocessableEntity, client.Get("/system/account-owners?category=system", "Authorization: Bearer test").Code)
			response = client.Get("/system/account-owners?category="+kind+"&limit=1&offset=1", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			if kind == "platform" {
				require.Empty(t, decodeEnvelope[[]APISystemAccountOwner](t, response).Data)
			}
			require.Equal(t, http.StatusOK, client.Get(base+"/members", "Authorization: Bearer test").Code)
			require.Equal(t, http.StatusForbidden, client.Patch(base+"/members/22", "Authorization: Bearer test", map[string]any{"status": "active"}).Code)
			legacy, err := h.repository.CreateRole(t.Context(), 99, SystemLegacyRolePrefix+"33", "")
			require.NoError(t, err)
			_, err = h.repository.GrantPermission(t.Context(), 99, legacy.ID, "platform_administer")
			require.NoError(t, err)
			_, err = h.repository.AddMember(t.Context(), 99, legacy.ID, 33)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, client.Post(base+"/members", "Authorization: Bearer test", map[string]any{"subject": "33", "display_name": "Legacy"}).Code)
			v.principal = 11
			v.view = &authn.AccountViewIdentity{ID: 55, PrincipalID: 11, ScopeType: "system", ScopeID: 1, ReadOnly: true}
			require.Equal(t, http.StatusForbidden, client.Patch(base+"/members/22", "Authorization: Bearer test", map[string]any{"status": "active"}).Code)
			v.view = nil
			response = client.Patch(base+"/members/22", "Authorization: Bearer test", map[string]any{"status": "active"})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			admin, err := h.repository.CreateRole(t.Context(), 2, "Scoped administrator", "")
			require.NoError(t, err)
			adminPermission := "tenant_administer"
			if kind == "platform" {
				adminPermission = "operating_platform_administer"
			}
			_, err = h.repository.GrantPermission(t.Context(), 2, admin.ID, adminPermission)
			require.NoError(t, err)
			response = client.Put(base+"/roles/"+admin.ID.String()+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			response = client.Delete(base+"/roles/"+admin.ID.String()+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			response = client.Delete(base+"/members/22", "Authorization: Bearer test")
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			response = client.Patch(base+"/members/22", "Authorization: Bearer test", map[string]any{"status": "disabled"})
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status='suspended' WHERE id=2").Error)
			v.principal = 33
			response = client.Get("/system/account-owners?category="+kind, "Authorization: Bearer test")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			owners = decodeEnvelope[[]APISystemAccountOwner](t, response).Data
			foundSuspended := false
			for _, owner := range owners {
				if owner.ID == 2 {
					foundSuspended = true
					require.Equal(t, "suspended", owner.Status)
					require.Equal(t, "Scope owner", owner.Name)
				}
			}
			require.True(t, foundSuspended)
			v.principal = 11
			require.Equal(t, http.StatusOK, client.Get(base+"/members", "Authorization: Bearer test").Code)
			params := api.OpenAPI().Paths["/system/members"].Get.Parameters
			found := map[string]bool{}
			for _, p := range params {
				found[p.Name] = true
			}
			require.True(t, found["offset"])
			require.True(t, found["limit"])
		})
	}
}
