package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

// platformBlindEvaluator hides CheckOperatingPlatform, which an evaluator may
// legitimately omit.
type platformBlindEvaluator struct{ AuthorizationEvaluator }

func TestOperatingPlatformMemberReadsRequireAnOperatingPlatformRoot(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)

	members, total, err := h.service.ListOperatingPlatformMembers(t.Context(), 1, MemberFilter{Limit: 50})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, members, 1)
	require.Equal(t, guid.ID(11), members[0].PrincipalID)
	member, err := h.service.GetOperatingPlatformMember(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Equal(t, MemberActive, member.Status)

	for name, owner := range map[string]guid.ID{"tenant root": 2, "unknown root": 987654321} {
		_, _, err = h.service.ListOperatingPlatformMembers(t.Context(), owner, MemberFilter{Limit: 50})
		require.ErrorIs(t, err, ErrInvalidArgument, name)
		_, err = h.service.GetOperatingPlatformMember(t.Context(), owner, 22)
		require.ErrorIs(t, err, ErrInvalidArgument, name)
	}
	_, err = h.service.GetTenantMember(t.Context(), 2, 22)
	require.NoError(t, err, "the tenant membership itself exists; only the platform lens refuses it")
}

func TestOperatingPlatformPermissionEvaluationFollowsRootKind(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)

	_, err := h.service.EffectiveOperatingPlatformPermissions(t.Context(), 0, 11)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = h.service.EffectiveOperatingPlatformPermissions(t.Context(), 1, 0)
	require.ErrorIs(t, err, ErrInvalidArgument)
	granted, err := h.service.EffectiveOperatingPlatformPermissions(t.Context(), 1, 11)
	require.NoError(t, err)
	require.Contains(t, granted, "operating_platform_administer")
	require.Contains(t, granted, "platform_member_view")
	require.NotContains(t, granted, "tenant_create")
	none, err := h.service.EffectiveOperatingPlatformPermissions(t.Context(), 1, 33)
	require.NoError(t, err)
	require.Empty(t, none, "a non-member holds nothing on the platform")

	_, err = h.repository.GrantPlatformAdministrator(t.Context(), 11)
	require.NoError(t, err)
	allowed, err := h.service.checkRootPermissions(t.Context(), 99, []string{"system_menu_view", "role_view"}, 11)
	require.NoError(t, err)
	require.True(t, allowed["system_menu_view"], "the system root answers from system authority")
	allowed, err = h.service.checkRootPermissions(t.Context(), 99, []string{"system_menu_view"}, 33)
	require.NoError(t, err)
	require.False(t, allowed["system_menu_view"])

	h.service.checker = platformBlindEvaluator{AuthorizationEvaluator: h.authorizer}
	_, err = h.service.checkRootPermissions(t.Context(), 1, []string{"role_view"}, 11)
	require.ErrorIs(t, err, ErrUnavailable, "a platform root is never evaluated with tenant semantics")
	_, err = h.service.checkRootPermissions(t.Context(), 987654321, []string{"role_view"}, 11)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestRootPermissionOwnershipMatchesRootKind(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id, scope_kind, slug, name, status, version, created_at, updated_at) VALUES (99,'system',?,'System','active',1,?,?)", SystemRootSlug, repositoryTestTime, repositoryTestTime).Error)
	for _, tc := range []struct {
		owner   guid.ID
		code    string
		allowed bool
	}{
		{99, "system_menu_view", true},
		{99, "role_view", false},
		{1, "platform_member_view", true},
		{1, "tenant_create", false},
		{1, "entity_view", false},
		{2, "entity_view", true},
		{2, "platform_member_view", false},
		{2, "system_menu_view", false},
	} {
		found, ok := h.service.registry.Find(tc.code)
		require.True(t, ok, tc.code)
		err := h.service.permissionAllowedForRoot(t.Context(), tc.owner, found)
		if tc.allowed {
			require.NoError(t, err, "%d %s", tc.owner, tc.code)
		} else {
			require.ErrorIs(t, err, ErrInvalidArgument, "%d %s", tc.owner, tc.code)
		}
	}
	found, _ := h.service.registry.Find("role_view")
	require.ErrorIs(t, h.service.permissionAllowedForRoot(t.Context(), 987654321, found), ErrInvalidArgument)
}
