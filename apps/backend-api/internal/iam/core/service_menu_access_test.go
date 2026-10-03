package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveMenusPreserveUnauthorizedParentsOnlyAsDirectories(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "View users", "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "user_view")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, role.ID, 11, nil)
	require.NoError(t, err)
	parent, err := h.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "Roles", Route: "/roles", PermissionCode: "role_view", Status: MenuActive})
	require.NoError(t, err)
	_, err = h.service.CreateMenu(t.Context(), Menu{TenantID: 1, ParentID: parent.ID, Label: "Users", Route: "/users", PermissionCode: "user_view", Status: MenuActive})
	require.NoError(t, err)
	tree, err := h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Len(t, tree, 1)
	require.Empty(t, tree[0].Path)
	require.Len(t, tree[0].Children, 1)
	require.Equal(t, "/users", tree[0].Children[0].Path)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "role_view")
	require.NoError(t, err)
	tree, err = h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, "/roles", tree[0].Path)
}

func TestEffectiveMenusCannotWeakenBuiltInPagePermissions(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	role, err := h.service.CreateRole(t.Context(), 1, "View menus", "")
	require.NoError(t, err)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "menu_view")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, role.ID, 11, nil)
	require.NoError(t, err)
	for _, route := range []string{"/users", "/users?custom=true", "/%75sers", "/other/../users", "/entities/123/roles"} {
		_, err = h.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: route, Route: route, PermissionCode: "menu_view", Status: MenuActive})
		require.NoError(t, err)
	}
	_, err = h.service.CreateMenu(t.Context(), Menu{TenantID: 1, Label: "Unprotected users", Route: "/users?unprotected=true", Status: MenuActive})
	require.NoError(t, err)
	tree, err := h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Empty(t, tree)
	_, err = h.service.GrantPermission(t.Context(), 1, role.ID, "user_view")
	require.NoError(t, err)
	tree, err = h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Len(t, tree, 5)
}
