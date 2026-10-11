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
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
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
			_, err = h.repository.AddMember(t.Context(), 99, legacy.ID, 33, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, client.Post(base+"/members", "Authorization: Bearer test", map[string]any{"subject": "33", "display_name": "Legacy"}).Code)
			v.principal = 11
			v.view = &authn.AccountViewIdentity{ID: 55, ActorID: 11, ScopeType: "system", ScopeID: 1, ReadOnly: true}
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

func TestSystemScopeHTTPRejectsWrongKindAndMissingTargetsWithoutWriting(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	for id, kind := range map[guid.ID]string{2: "tenant", 3: "platform"} {
		require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(?,?,?,?,'active',1,?,?)", id, kind, "owner-"+id.String(), "Owner "+id.String(), repositoryTestTime, repositoryTestTime).Error)
	}
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	require.NoError(t, initializeHTTPLocales())
	r := authz.NewRegistrar(h.service.registry, &systemAccessVerifier{principal: 11}, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("scope-kind-test", "1"))
	registerSystemScopeHTTP(api, h.service, r)
	client := humatest.Wrap(t, api)
	const auth = "Authorization: Bearer test"
	tenantRole, err := h.repository.CreateRole(t.Context(), 2, "Tenant reader", "")
	require.NoError(t, err)
	platformRole, err := h.repository.CreateRole(t.Context(), 3, "Platform reader", "")
	require.NoError(t, err)

	response := client.Post("/system/tenants/2/members", auth, map[string]any{"subject": "22", "display_name": "Managed", "email": "managed@example.test"})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	response = client.Get("/system/tenants/2/roles", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	roles := decodeEnvelope[[]APIRole](t, response).Data
	require.Len(t, roles, 1)
	require.Equal(t, tenantRole.ID, roles[0].ID, "roles are listed from the addressed owner only")
	require.Equal(t, http.StatusOK, client.Put("/system/tenants/2/roles/"+tenantRole.ID.String()+"/members/22", auth).Code)
	response = client.Get("/system/tenants/2/members/22", auth)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	member := decodeEnvelope[APITenantMember](t, response).Data
	require.Equal(t, "managed@example.test", member.Email)
	require.Equal(t, []guid.ID{tenantRole.ID}, member.RoleIDs)
	response = client.Patch("/system/tenants/2/members/22", auth, map[string]any{"email": "moved@example.test", "department_id": "", "status": "disabled"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	patched := decodeEnvelope[APITenantMember](t, response).Data
	require.Equal(t, "moved@example.test", patched.Email)
	require.Equal(t, MemberDisabled, patched.Status)
	require.Equal(t, "Managed", patched.DisplayName, "omitted fields are preserved")

	// Every route resolves the owner's real kind: a platform id under the
	// tenant segment, a tenant id under the platform segment, and an unknown id
	// are all indistinguishable 404s.
	for base, missing := range map[string]string{
		"/system/tenants/3":             "platform root under tenant segment",
		"/system/operating-platforms/2": "tenant root under platform segment",
		"/system/tenants/987654321":     "unknown owner",
	} {
		for name, request := range map[string]func() int{
			"list members": func() int { return client.Get(base+"/members", auth).Code },
			"get member":   func() int { return client.Get(base+"/members/22", auth).Code },
			"list roles":   func() int { return client.Get(base+"/roles", auth).Code },
			"member roles": func() int { return client.Get(base+"/members/22/roles", auth).Code },
			"create": func() int {
				return client.Post(base+"/members", auth, map[string]any{"subject": "22", "display_name": "Misrouted"}).Code
			},
			"update": func() int {
				return client.Patch(base+"/members/22", auth, map[string]any{"display_name": "Misrouted"}).Code
			},
			"disable": func() int { return client.Delete(base+"/members/22", auth).Code },
			"add role": func() int {
				return client.Put(base+"/roles/"+platformRole.ID.String()+"/members/22", auth).Code
			},
		} {
			require.Equal(t, http.StatusNotFound, request(), "%s: %s", missing, name)
		}
	}
	_, err = h.service.GetTenantMember(t.Context(), 3, 22)
	require.ErrorIs(t, err, ErrMemberNotFound, "a misrouted create never binds the platform")
	stored, err := h.service.GetTenantMember(t.Context(), 2, 22)
	require.NoError(t, err)
	require.Equal(t, "Managed", stored.DisplayName, "a misrouted update never touches the tenant membership")
	require.Equal(t, MemberDisabled, stored.Status)

	require.Equal(t, http.StatusNotFound, client.Patch("/system/tenants/2/members/33", auth, map[string]any{"display_name": "Ghost"}).Code)
	require.Equal(t, http.StatusNotFound, client.Delete("/system/tenants/2/members/33", auth).Code)
	require.Equal(t, http.StatusNotFound, client.Delete("/system/tenants/2/roles/987654321/members/22", auth).Code)
	require.Equal(t, http.StatusNotFound, client.Delete("/system/tenants/2/roles/"+platformRole.ID.String()+"/members/22", auth).Code)
	assigned, err := h.service.ListTenantMemberRoles(t.Context(), 2, 22)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{tenantRole.ID}, assigned, "failed removals keep the existing assignment")
}
