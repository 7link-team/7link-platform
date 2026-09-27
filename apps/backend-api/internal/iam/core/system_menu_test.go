package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
)

func seedSystemMenuHarness(t *testing.T) coreServiceHarness {
	t.Helper()
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	_, err := h.repository.createMenus(t.Context(), 99, SystemDefaultMenus(), repositoryTestTime)
	require.NoError(t, err)
	return h
}

func TestSystemMenuStoredTreeUsesGlobalPermissionFloorsAndRealOwner(t *testing.T) {
	for _, menu := range SystemDefaultMenus() {
		require.Equal(t, menu.Label, menu.Translations["en-US"])
		for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
			require.NotEmpty(t, menu.Translations[locale])
			require.NotContains(t, menu.Translations[locale], "menu_system_")
		}
	}
	h := seedSystemMenuHarness(t)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_platform_grants (principal_id,permission_code,created_at) VALUES (11,'system_menu_view',?)", repositoryTestTime).Error)
	s, owner, err := h.service.systemMenuService(t.Context())
	require.NoError(t, err)
	require.Equal(t, guid.ID(99), owner)
	menus, err := s.ListMenus(t.Context(), owner)
	require.NoError(t, err)
	require.Len(t, menus, 11)
	tree, err := s.EffectiveMenus(t.Context(), owner, 11)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Equal(t, "Access", tree[0].Label)
	require.Len(t, tree[0].Children, 1)
	require.Equal(t, "/system/menus", tree[0].Children[0].Path)
	var accounts Menu
	for _, menu := range menus {
		if menu.Route == "/system/accounts" {
			accounts = menu
		}
	}
	accounts.PermissionCode = "system_menu_view"
	accounts.Label = "Edited accounts"
	_, err = s.UpdateMenu(t.Context(), accounts)
	require.NoError(t, err)
	tree, err = s.EffectiveMenus(t.Context(), owner, 11)
	require.NoError(t, err)
	require.Len(t, tree, 1, "weakened page gate must still omit accounts")
	_, err = h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	tree, err = s.EffectiveMenus(t.Context(), owner, 11)
	require.NoError(t, err)
	require.Len(t, tree, 3)
	require.Equal(t, "Edited accounts", tree[0].Children[0].Label)
	require.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_roles", owner))
	require.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_tenant_members", owner))
}

func TestSystemMenuMutationsRejectForeignOwnershipAndAttributeOperator(t *testing.T) {
	h := seedSystemMenuHarness(t)
	s, owner, err := h.service.systemMenuService(t.Context())
	require.NoError(t, err)
	foreign, err := h.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "Tenant", Status: MenuActive})
	require.NoError(t, err)
	_, err = s.GetMenu(t.Context(), owner, foreign.ID)
	require.Error(t, err)
	for _, menu := range []Menu{
		{TenantID: owner, Label: "Bad code", Route: "/system/menus", PermissionCode: "menu_view", Status: MenuActive},
		{TenantID: owner, Label: "Bad route", Route: "/menus", PermissionCode: "system_menu_view", Status: MenuActive},
		{TenantID: owner, Label: "Unprotected route", Route: "/system/custom", Status: MenuActive},
		{TenantID: owner, ParentID: foreign.ID, Label: "Foreign parent", Status: MenuActive},
	} {
		_, err = s.CreateMenu(t.Context(), menu)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11, AccountView: &authn.AccountViewIdentity{ID: 44, PrincipalID: 22, ScopeType: "system", ScopeID: 1}})
	before, err := h.repository.revisions.Current(t.Context(), h.db, owner)
	require.NoError(t, err)
	created, err := s.CreateMenu(ctx, Menu{TenantID: owner, Label: "Custom", Route: "/system/custom", PermissionCode: "system_menu_view", Status: MenuActive})
	require.NoError(t, err)
	event := (*h.events)[len(*h.events)-1]
	require.Equal(t, guid.ID(0), event.TenantID)
	require.Equal(t, guid.ID(22), event.PrincipalID)
	require.Equal(t, "11", event.Detail["viewed_principal_id"])
	require.Equal(t, "99", event.Detail["scope_id"])
	after, err := h.repository.revisions.Current(t.Context(), h.db, owner)
	require.NoError(t, err)
	require.Equal(t, before+1, after)
	_, err = s.MoveMenu(ctx, owner, created.ID, nil, "before", foreign.ID)
	require.Error(t, err)
}
