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
	"gorm.io/gorm"
)

func bootstrapSystemTestAccess(t *testing.T, h coreServiceHarness) {
	t.Helper()
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.repository.withDB(tx).bootstrapSystemAccess(t.Context(), 99, repositoryTestTime, h.service.registry, h.service.writes.audit)
	}))
}

func TestSystemAccessLegacyMigrationPreservesExactAuthorityAndIsIdempotent(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants(principal_id,permission_code,created_at) VALUES (22,'system_menu_view',?),(22,'platform_administer',?)", repositoryTestTime, repositoryTestTime).Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES (11,?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	var count int64
	require.NoError(t, h.db.Table("sl_iam_platform_grants").Count(&count).Error)
	require.Zero(t, count)
	for _, code := range []string{"system_menu_view", "platform_administer"} {
		allowed, err := h.authorizer.CheckPlatform(t.Context(), code, 22)
		require.NoError(t, err)
		require.True(t, allowed, code)
	}
	allowed, err := h.authorizer.CheckPlatform(t.Context(), "system_account_create", 22)
	require.NoError(t, err)
	require.False(t, allowed)
	roles, err := h.service.ListRoles(t.Context(), 99)
	require.NoError(t, err)
	require.Len(t, roles, 2)
	require.EqualValues(t, 2, countBootstrapRows(t, h.db, "sl_", "iam_tenant_members", 99))
	eventCount := len(*h.events)
	bootstrapSystemTestAccess(t, h)
	again, err := h.service.ListRoles(t.Context(), 99)
	require.NoError(t, err)
	require.Equal(t, roles, again)
	require.Len(t, *h.events, eventCount)
	require.Equal(t, guid.ID(0), (*h.events)[eventCount-1].TenantID)
}

func TestSystemAccessLegacyMigrationRollsBackOnPermissionFailure(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants(principal_id,permission_code,created_at) VALUES(22,'system_menu_view',?)", repositoryTestTime).Error)
	require.NoError(t, h.db.Exec("CREATE TRIGGER reject_system_permission BEFORE INSERT ON sl_iam_role_permissions BEGIN SELECT RAISE(ABORT,'system permission failure'); END").Error)
	err := h.db.Transaction(func(tx *gorm.DB) error {
		return h.repository.withDB(tx).bootstrapSystemAccess(t.Context(), 99, repositoryTestTime, h.service.registry, h.service.writes.audit)
	})
	require.ErrorContains(t, err, "system permission failure")
	var count int64
	require.NoError(t, h.db.Table("sl_iam_platform_grants").Where("principal_id = 22").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_roles", 99))
	require.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_tenant_members", 99))
	require.Empty(t, *h.events)
	require.NoError(t, h.db.Exec("DROP TRIGGER reject_system_permission").Error)
	bootstrapSystemTestAccess(t, h)
	allowed, err := h.authorizer.CheckPlatform(t.Context(), "system_menu_view", 22)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestSystemMenuUpgradePreservesCustomNodesAndRunsOnce(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	_, err := h.repository.createMenus(t.Context(), 99, legacySystemDefaultMenus(), repositoryTestTime)
	require.NoError(t, err)
	legacyMenus, err := h.repository.ListMenus(t.Context(), 99, false)
	require.NoError(t, err)
	var branch, leaf Menu
	var auditParent guid.ID
	for _, menu := range legacyMenus {
		if menu.Label == "Organization" {
			branch = menu
		}
		if menu.Route == "/system/accounts" {
			leaf = menu
		}
		if menu.Label == "Audit" {
			auditParent = menu.ID
		}
	}
	branch.Icon, branch.SortOrder, branch.Translations = "custom-branch", 73, map[string]string{"en-US": "Custom account branch"}
	branch, err = h.repository.UpdateMenu(t.Context(), branch)
	require.NoError(t, err)
	leaf.Icon, leaf.SortOrder, leaf.ParentID, leaf.Translations = "custom-leaf", 91, auditParent, map[string]string{"en-US": "Custom accounts"}
	leaf, err = h.repository.UpdateMenu(t.Context(), leaf)
	require.NoError(t, err)
	custom, err := h.repository.CreateMenu(t.Context(), Menu{TenantID: 99, Label: "Custom operations", Route: "/system/custom", PermissionCode: "system_menu_view", Status: MenuActive})
	require.NoError(t, err)
	bootstrapSystemTestAccess(t, h)
	menus, err := h.service.ListMenus(t.Context(), 99)
	require.NoError(t, err)
	require.Len(t, menus, 19)
	persisted, err := h.service.GetMenu(t.Context(), 99, custom.ID)
	require.NoError(t, err)
	require.Equal(t, custom, persisted)
	assertCustomizedDefaults := func() {
		t.Helper()
		gotBranch, err := h.service.GetMenu(t.Context(), 99, branch.ID)
		require.NoError(t, err)
		require.Equal(t, "Account management", gotBranch.Label)
		require.Equal(t, branch.Icon, gotBranch.Icon)
		require.Equal(t, branch.SortOrder, gotBranch.SortOrder)
		require.Equal(t, branch.Translations, gotBranch.Translations)
		gotLeaf, err := h.service.GetMenu(t.Context(), 99, leaf.ID)
		require.NoError(t, err)
		require.Equal(t, "User accounts", gotLeaf.Label)
		require.Equal(t, leaf.Icon, gotLeaf.Icon)
		require.Equal(t, leaf.SortOrder, gotLeaf.SortOrder)
		require.Equal(t, leaf.ParentID, gotLeaf.ParentID)
		require.Equal(t, leaf.Translations, gotLeaf.Translations)
	}
	assertCustomizedDefaults()
	for _, menu := range menus {
		if menu.Route == "/system/operating-platforms" {
			parent, err := h.service.GetMenu(t.Context(), 99, menu.ParentID)
			require.NoError(t, err)
			require.Equal(t, "Operations management", parent.Label)
		}
	}
	var removed guid.ID
	for _, menu := range menus {
		require.NotEqual(t, "/system/administrators", menu.Route)
		if menu.Route == "/system/departments" {
			removed = menu.ID
		}
	}
	require.NoError(t, h.repository.DeleteMenu(t.Context(), 99, removed))
	bootstrapSystemTestAccess(t, h)
	assertCustomizedDefaults()
	_, err = h.service.GetMenu(t.Context(), 99, removed)
	require.Error(t, err)
}

type systemAccessVerifier struct {
	principal guid.ID
	view      *authn.AccountViewIdentity
}

func TestSystemMenuUpgradeCorrectsHistoricalAccountTranslations(t *testing.T) {
	for _, alreadyUpgraded := range []bool{false, true} {
		name := "legacy"
		if alreadyUpgraded {
			name = "already-upgraded"
		}
		t.Run(name, func(t *testing.T) {
			h := newCoreServiceHarness(t)
			seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
			defaults := legacySystemDefaultMenus()
			if alreadyUpgraded {
				defaults = SystemDefaultMenus()
			}
			historical := map[string]string{"en-US": "Global accounts", "zh-CN": "全局账号", "ms-MY": "Akaun global"}
			for i := range defaults {
				if defaults[i].Route == "/system/accounts" {
					defaults[i].Translations = historical
				}
			}
			_, err := h.repository.createMenus(t.Context(), 99, defaults, repositoryTestTime)
			require.NoError(t, err)
			bootstrapSystemTestAccess(t, h)
			menus, err := h.repository.ListMenus(t.Context(), 99, false)
			require.NoError(t, err)
			var account Menu
			var removed guid.ID
			for _, menu := range menus {
				if menu.Route == "/system/accounts" {
					account = menu
				}
				if menu.Route == "/system/departments" {
					removed = menu.ID
				}
			}
			require.Equal(t, "User accounts", account.Label)
			require.Equal(t, "User accounts", account.Translations["en-US"])
			require.Equal(t, "用户账号", account.Translations["zh-CN"])
			require.Equal(t, "Akaun pengguna", account.Translations["ms-MY"])
			require.NotZero(t, removed)
			require.NoError(t, h.repository.DeleteMenu(t.Context(), 99, removed))
			account.Translations = map[string]string{"en-US": "Custom users", "zh-CN": "自定义账号", "ms-MY": "Akaun tersuai"}
			account, err = h.repository.UpdateMenu(t.Context(), account)
			require.NoError(t, err)
			for i := 0; i < 2; i++ {
				bootstrapSystemTestAccess(t, h)
				stored, err := h.service.GetMenu(t.Context(), 99, account.ID)
				require.NoError(t, err)
				require.Equal(t, account, stored)
				_, err = h.service.GetMenu(t.Context(), 99, removed)
				require.Error(t, err)
			}
		})
	}
}

func TestSystemMemberHTTPIncludesMigratedDirectRoleIDs(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants(principal_id,permission_code,created_at) VALUES(22,'system_menu_view',?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	require.NoError(t, initializeHTTPLocales())
	r := authz.NewRegistrar(h.service.registry, &systemAccessVerifier{principal: 11}, h.authorizer, httpMembershipChecker{})
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("member-role-projection", "1"))
	registerSystemAccessHTTP(api, h.service, r)
	client := humatest.Wrap(t, api)
	response := client.Get("/system/roles", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var migrated, full guid.ID
	for _, role := range decodeEnvelope[[]APISystemRole](t, response).Data {
		if role.Name == SystemLegacyRolePrefix+"22" {
			migrated = role.ID
		}
		if role.Builtin {
			full = role.ID
		}
	}
	require.NotZero(t, migrated)
	require.NotZero(t, full)
	_, err := h.repository.GrantPermission(t.Context(), 99, migrated, "platform_administer")
	require.NoError(t, err)
	response = client.Patch("/system/roles/"+migrated.String(), "Authorization: Bearer test", map[string]any{"name": SystemLegacyRolePrefix + "22", "description": "Updated imported role"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "Updated imported role", decodeEnvelope[APISystemRole](t, response).Data.Description)
	require.Equal(t, SystemLegacyRolePrefix+"22", decodeEnvelope[APISystemRole](t, response).Data.Name)
	response = client.Patch("/system/roles/"+migrated.String(), "Authorization: Bearer test", map[string]any{"name": "Renamed imported authority"})
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = client.Post("/system/roles", "Authorization: Bearer test", map[string]any{"name": "Ordinary role"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	ordinary := decodeEnvelope[APISystemRole](t, response).Data
	response = client.Patch("/system/roles/"+ordinary.ID.String(), "Authorization: Bearer test", map[string]any{"name": SystemLegacyRolePrefix + "ordinary"})
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	response = client.Patch("/system/roles/"+full.String(), "Authorization: Bearer test", map[string]any{"name": SystemSuperadministratorRoleName, "description": "Protected edit"})
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = client.Get("/system/members", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, member := range decodeEnvelope[[]APISystemMember](t, response).Data {
		if member.Subject == 22 {
			require.Equal(t, []guid.ID{migrated}, member.RoleIDs)
		}
	}
	response = client.Get("/system/members/22", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []guid.ID{migrated}, decodeEnvelope[APISystemMember](t, response).Data.RoleIDs)
	response = client.Get("/system/members/22/roles", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []guid.ID{migrated}, decodeEnvelope[[]guid.ID](t, response).Data)
}

func TestSystemMenuUpgradePreservesCustomizedLegacyAccessWithoutReseeding(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants(id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES(99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	_, err := h.repository.createMenus(t.Context(), 99, legacySystemDefaultMenus(), repositoryTestTime)
	require.NoError(t, err)
	menus, err := h.repository.ListMenus(t.Context(), 99, false)
	require.NoError(t, err)
	var customized Menu
	var parent guid.ID
	for _, menu := range menus {
		if menu.Route == "/system/administrators" {
			customized = menu
		}
		if menu.Label == "Audit" {
			parent = menu.ID
		}
	}
	customized.Icon, customized.SortOrder, customized.ParentID, customized.Translations = "custom-shield", 77, parent, map[string]string{"en-US": "Custom grants"}
	customized, err = h.repository.UpdateMenu(t.Context(), customized)
	require.NoError(t, err)
	bootstrapSystemTestAccess(t, h)
	stored, err := h.service.GetMenu(t.Context(), 99, customized.ID)
	require.NoError(t, err)
	require.Equal(t, customized, stored)
	menus, err = h.repository.ListMenus(t.Context(), 99, false)
	require.NoError(t, err)
	var removed guid.ID
	for _, menu := range menus {
		if menu.Route == "/system/departments" {
			removed = menu.ID
		}
	}
	require.NotZero(t, removed)
	require.NoError(t, h.repository.DeleteMenu(t.Context(), 99, removed))
	for i := 0; i < 2; i++ {
		bootstrapSystemTestAccess(t, h)
		_, err = h.service.GetMenu(t.Context(), 99, removed)
		require.Error(t, err)
		stored, err = h.service.GetMenu(t.Context(), 99, customized.ID)
		require.NoError(t, err)
		require.Equal(t, customized, stored)
	}
}

func (v *systemAccessVerifier) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: v.principal, SubjectType: authn.SubjectTypePrincipal, AccountView: v.view}, nil
}

func TestSystemAccessHTTPDirectRolesAndProtectedSuperadministrator(t *testing.T) {
	h := seedSystemMenuHarness(t)
	seedHTTPPrincipal(t, h.repositoryHarness, 22)
	seedHTTPPrincipal(t, h.repositoryHarness, 33)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_administrators(principal_id,created_at) VALUES(11,?)", repositoryTestTime).Error)
	bootstrapSystemTestAccess(t, h)
	require.NoError(t, initializeHTTPLocales())
	verifier := &systemAccessVerifier{principal: 11}
	registrar := authz.NewRegistrar(h.service.registry, verifier, h.authorizer, httpMembershipChecker{})
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("system-access-test", "1"))
	registerSystemAccessHTTP(api, h.service, registrar)
	client := humatest.Wrap(t, api)
	response := client.Get("/system/roles", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	roles := decodeEnvelope[[]APISystemRole](t, response).Data
	require.Len(t, roles, 1)
	full := roles[0]
	require.True(t, full.Builtin)
	require.Equal(t, http.StatusForbidden, client.Patch("/system/roles/"+full.ID.String(), "Authorization: Bearer test", map[string]any{"name": "Weakened"}).Code)
	require.Equal(t, http.StatusForbidden, client.Delete("/system/roles/"+full.ID.String(), "Authorization: Bearer test").Code)
	require.Equal(t, http.StatusForbidden, client.Put("/system/roles/"+full.ID.String()+"/permissions/system_menu_view", "Authorization: Bearer test").Code)
	response = client.Post("/system/roles", "Authorization: Bearer test", map[string]any{"name": "Menu readers"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	role := decodeEnvelope[APISystemRole](t, response).Data
	response = client.Post("/system/members", "Authorization: Bearer test", map[string]any{"principal_id": "22", "display_name": "Reader"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = client.Put("/system/roles/"+role.ID.String()+"/permissions/system_menu_view", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = client.Put("/system/roles/"+role.ID.String()+"/members/22", "Authorization: Bearer test")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	allowed, err := h.authorizer.CheckPlatform(t.Context(), "system_menu_view", 22)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = h.authorizer.Check(t.Context(), 1, "user_view", 22)
	require.NoError(t, err)
	require.False(t, allowed, "system roles must not become tenant authority")
	response = client.Patch("/system/members/22", "Authorization: Bearer test", map[string]any{"status": "disabled"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	allowed, err = h.authorizer.CheckPlatform(t.Context(), "system_menu_view", 22)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, http.StatusOK, client.Patch("/system/members/22", "Authorization: Bearer test", map[string]any{"status": "active"}).Code)
	require.Equal(t, http.StatusForbidden, client.Patch("/system/members/11", "Authorization: Bearer test", map[string]any{"status": "disabled"}).Code)
	foreign, err := h.service.CreateRole(t.Context(), 1, "Tenant role", "")
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, client.Get("/system/roles/"+foreign.ID.String(), "Authorization: Bearer test").Code)
	seedSystemPermissions(t, h, 22, "system_role_view", "system_role_create", "system_role_grant_permission", "system_role_manage_member")
	verifier.principal = 22
	require.Equal(t, http.StatusForbidden, client.Put("/system/roles/"+role.ID.String()+"/permissions/system_account_create", "Authorization: Bearer test").Code)
	require.Equal(t, http.StatusForbidden, client.Put("/system/roles/"+full.ID.String()+"/members/33", "Authorization: Bearer test").Code)
	verifier.view = &authn.AccountViewIdentity{ID: 44, PrincipalID: 11, ScopeType: "system", ScopeID: 1, ReadOnly: true}
	require.Equal(t, http.StatusForbidden, client.Post("/system/roles", "Authorization: Bearer test", map[string]any{"name": "Read only"}).Code)
	verifier.view.ReadOnly = false
	response = client.Post("/system/roles", "Authorization: Bearer test", map[string]any{"name": "Viewed role"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	event := (*h.events)[len(*h.events)-1]
	require.Equal(t, guid.ID(0), event.TenantID)
	require.Equal(t, guid.ID(11), event.PrincipalID)
	require.Equal(t, "22", event.Detail["viewed_principal_id"])
}
