package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedOperatingPlatform(t *testing.T, h coreServiceHarness, platformID, ownerID guid.ID) TenantBootstrapResult {
	t.Helper()
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (?, 'platform', ?, ?, 'active', 1, ?, ?)", platformID, "platform-"+platformID.String(), "Platform "+platformID.String(), repositoryTestTime, repositoryTestTime).Error)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_principals (id, login_name, display_name, status, created_at, updated_at) VALUES (?, ?, 'Owner', 'active', ?, ?)", ownerID, "owner-"+ownerID.String(), repositoryTestTime, repositoryTestTime).Error)
	var result TenantBootstrapResult
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = h.repository.BootstrapTenantOn(t.Context(), tx, TenantBootstrapInput{TenantID: platformID, OwnerID: ownerID, ScopeKind: "platform", DisplayName: "Owner"}, repositoryTestTime)
		return err
	})
	require.NoError(t, err)
	return result
}

func TestOperatingPlatformsHaveIndependentOwnersAndLocalAdministration(t *testing.T) {
	h := newCoreServiceHarness(t)
	first := seedOperatingPlatform(t, h, 1, 11)
	second := seedOperatingPlatform(t, h, 2, 22)
	require.NotEqual(t, first.AdministratorRoleID, second.AdministratorRoleID)
	require.Equal(t, 7, first.MenuCount)
	for _, code := range []string{"role_view", "menu_update", "dept_create", "group_manage_member", "platform_member_create"} {
		allowed, err := h.authorizer.CheckOperatingPlatform(t.Context(), 1, code, 11)
		require.NoError(t, err)
		require.True(t, allowed, code)
		allowed, err = h.authorizer.CheckOperatingPlatform(t.Context(), 2, code, 11)
		require.NoError(t, err)
		require.False(t, allowed, code)
	}
	for _, code := range []string{"platform_administer", "tenant_create", "tenant_administer", "entity_view", "user_delete", "oauth_client_create", "operating_platform_create"} {
		allowed, err := h.authorizer.CheckOperatingPlatform(t.Context(), 1, code, 11)
		require.NoError(t, err)
		require.False(t, allowed, code)
		_, err = h.service.GrantPermission(t.Context(), 1, first.AdministratorRoleID, code)
		require.ErrorIs(t, err, ErrInvalidArgument, code)
	}
	allowed, err := h.authorizer.Check(t.Context(), 1, "role_view", 11)
	require.NoError(t, err)
	require.False(t, allowed, "ordinary tenant admission excludes platform roots")
	_, err = h.service.PutTenantMember(t.Context(), 1, 22, "Owner", "", 0, MemberActive)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = h.service.PutOperatingPlatformMember(t.Context(), 1, 22, "Owner", "", 0, MemberActive)
	require.NoError(t, err)
	allowed, err = h.authorizer.CheckOperatingPlatform(t.Context(), 1, "role_view", 22)
	require.NoError(t, err)
	require.False(t, allowed, "membership alone does not copy another platform's roles")
	_, err = h.repository.GrantPlatformAdministrator(t.Context(), 22)
	require.NoError(t, err)
	allowed, err = h.authorizer.CheckOperatingPlatform(t.Context(), 1, "role_view", 22)
	require.NoError(t, err)
	require.False(t, allowed, "legacy global grants do not become local platform grants")
	granted, err := h.service.EffectivePermissions(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Contains(t, granted, "operating_platform_administer")
	require.NotContains(t, granted, "tenant_create")
	menus, err := h.service.EffectiveMenus(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Len(t, menus, 2)
	require.Equal(t, "Organization", menus[0].Label)
	require.Len(t, menus[0].Children, 3)
	require.Equal(t, "Access", menus[1].Label)
	require.Len(t, menus[1].Children, 2)
	emptyMenus, err := h.service.EffectiveMenus(t.Context(), 1, 22)
	require.NoError(t, err)
	require.Empty(t, emptyMenus, "ungranted members see no empty navigation branches")
	stored, err := h.service.ListMenus(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, stored, 7)
	parents := map[string]guid.ID{}
	for _, menu := range stored {
		if menu.ParentID.Zero() {
			require.Empty(t, menu.Route)
			parents[menu.Label] = menu.ID
		}
	}
	require.Len(t, parents, 2)
	for _, menu := range stored {
		if menu.ParentID.Zero() {
			continue
		}
		parent := "Organization"
		if menu.Route == "/roles" || menu.Route == "/menus" {
			parent = "Access"
		}
		require.Equal(t, parents[parent], menu.ParentID, menu.Route)
	}
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11})
	_, err = h.service.GrantPermission(ctx, 1, first.AdministratorRoleID, "role_view")
	require.NoError(t, err, "grant escalation uses local platform permissions")
}

func TestOperatingPlatformBootstrapRejectsWrongKindAndRollsBackAtomically(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedTenantAndPrincipal(t, h.db, "sl_", 1, 11)
	_, err := h.repository.BootstrapTenantOn(t.Context(), h.db, TenantBootstrapInput{TenantID: 1, OwnerID: 11, ScopeKind: "platform"}, repositoryTestTime)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Zero(t, countBootstrapRows(t, h.db, "sl_", "iam_tenant_members", 1))
	require.NoError(t, h.db.Exec("CREATE TRIGGER fail_platform_menus BEFORE INSERT ON sl_iam_menus BEGIN SELECT RAISE(ABORT, 'menu insert failure'); END").Error)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (2, 'platform', 'platform-two', 'Platform Two', 'active', 1, ?, ?)", repositoryTestTime, repositoryTestTime).Error; err != nil {
			return err
		}
		_, err := h.repository.BootstrapTenantOn(t.Context(), tx, TenantBootstrapInput{TenantID: 2, OwnerID: 11, ScopeKind: "platform"}, repositoryTestTime)
		return err
	})
	require.ErrorContains(t, err, "menu insert failure")
	var count int64
	require.NoError(t, h.db.Table("sl_iam_tenants").Where("id = 2").Count(&count).Error)
	require.Zero(t, count)
	for _, table := range []string{"iam_tenant_members", "iam_roles", "iam_role_permissions", "iam_role_members", "iam_menus"} {
		require.Zero(t, countBootstrapRows(t, h.db, "sl_", table, 2), table)
	}
}

func TestOperatingPlatformLastAdministratorIsProtectedAcrossMutations(t *testing.T) {
	h := newCoreServiceHarness(t)
	result := seedOperatingPlatform(t, h, 1, 11)
	seedOperatingPlatform(t, h, 2, 22)
	_, err := h.service.SetOperatingPlatformMemberStatus(t.Context(), 1, 11, MemberDisabled)
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = h.service.PutOperatingPlatformMember(t.Context(), 1, 11, "Owner", "", 0, MemberDisabled)
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = h.service.RemoveMember(t.Context(), 1, result.AdministratorRoleID, 11)
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = h.service.RevokePermission(t.Context(), 1, result.AdministratorRoleID, "operating_platform_administer")
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	require.ErrorIs(t, h.service.DeleteRole(t.Context(), 1, result.AdministratorRoleID), ErrLastTenantAdministrator)
	_, _, err = h.service.SetPermissionCondition(t.Context(), 1, result.AdministratorRoleID, "operating_platform_administer", []byte(`{"version":1,"eq":[{"context":"client.id"},{"value":"console"}]}`))
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	_, err = h.service.PutOperatingPlatformMember(t.Context(), 1, 22, "Second owner", "", 0, MemberActive)
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, result.AdministratorRoleID, 22, nil)
	require.NoError(t, err)
	_, err = h.service.SetOperatingPlatformMemberStatus(t.Context(), 1, 11, MemberDisabled)
	require.NoError(t, err)
	_, err = h.service.SetOperatingPlatformMemberStatus(t.Context(), 1, 22, MemberDisabled)
	require.ErrorIs(t, err, ErrLastTenantAdministrator)
	member, err := h.service.GetOperatingPlatformMember(t.Context(), 1, 22)
	require.NoError(t, err)
	require.Equal(t, MemberActive, member.Status)
}

func TestOperatingPlatformMemberCreationCannotReplaceAnExistingMember(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	seedOperatingPlatform(t, h, 2, 22)
	member, err := h.service.CreateOperatingPlatformMember(t.Context(), 1, 22, "Original", "original@example.test", 0, MemberActive)
	require.NoError(t, err)
	require.Equal(t, "Original", member.DisplayName)
	eventCount := len(*h.events)
	_, err = h.service.CreateOperatingPlatformMember(t.Context(), 1, 22, "Overwrite", "changed@example.test", 0, MemberDisabled)
	require.ErrorIs(t, err, ErrMemberConflict)
	stored, err := h.service.GetOperatingPlatformMember(t.Context(), 1, 22)
	require.NoError(t, err)
	require.Equal(t, "Original", stored.DisplayName)
	require.Equal(t, "original@example.test", stored.Email)
	require.Equal(t, MemberActive, stored.Status)
	require.Len(t, *h.events, eventCount)
}
