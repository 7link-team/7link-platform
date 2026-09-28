package core

import (
	"fmt"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdministrativeRootsCannotEnterTenantAdmission(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	m := boundChecker(t, h.runtime, "sl_")
	for index, kind := range []string{"system", "platform"} {
		id := guid.ID(index + 2)
		require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_tenants
			(id, slug, name, scope_kind, status, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'active', 1, ?, ?)`, id, fmt.Sprintf("admin-%d", id), kind, kind, repositoryTestTime, repositoryTestTime).Error)
		require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_tenant_members
			(tenant_id, principal_id, display_name, email, status, created_at, updated_at)
			VALUES (?, 11, 'Member', '', 'active', ?, ?)`, id, repositoryTestTime, repositoryTestTime).Error)
		active, err := m.IsMemberActive(t.Context(), id, 11)
		require.NoError(t, err)
		assert.False(t, active, kind)
		selected, err := m.ResolveTenant(t.Context(), 11, id)
		require.NoError(t, err)
		assert.Zero(t, selected, kind)
		allowed, err := h.authorizer.Check(t.Context(), id, "role_view", 11)
		require.NoError(t, err)
		assert.False(t, allowed, kind)
	}
	selected, err := m.ResolveTenant(t.Context(), 11, 0)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), selected, "administrative memberships must not make the ordinary tenant choice ambiguous")
}

func TestActiveMemberDepartmentIsTenantScopedAndWriterBacked(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedMember(t, 2, 22, MemberActive)
	m := boundChecker(t, h.runtime, "sl_")
	for _, tenant := range []guid.ID{1, 2} {
		require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_departments
			(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
			VALUES (?, ?, NULL, 'Department', 'department', 'active', 0, 1, ?, ?)`,
			tenant, tenant+100, repositoryTestTime, repositoryTestTime).Error)
	}
	id, err := m.ActiveDepartment(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Zero(t, id)
	_, err = h.service.PutTenantMember(t.Context(), 1, 11, "Member", "member@example.test", 101, MemberActive)
	require.NoError(t, err)
	id, err = m.ActiveDepartment(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(101), id)
	id, err = m.ActiveDepartment(t.Context(), 2, 11)
	require.NoError(t, err)
	assert.Zero(t, id)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_departments SET status = 'disabled' WHERE tenant_id = 1 AND id = 101").Error)
	id, err = m.ActiveDepartment(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Zero(t, id)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_departments SET status = 'active' WHERE tenant_id = 1 AND id = 101").Error)
	_, err = h.service.PutTenantMember(t.Context(), 1, 11, "Member", "member@example.test", 101, MemberDisabled)
	require.NoError(t, err)
	id, err = m.ActiveDepartment(t.Context(), 1, 11)
	require.NoError(t, err)
	assert.Zero(t, id)
	_, err = m.ActiveDepartment(t.Context(), 0, 11)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	var absent *MembershipChecker
	_, err = absent.ActiveDepartment(t.Context(), 1, 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_member_departments").Error)
	_, err = m.ActiveDepartment(t.Context(), 1, 11)
	require.Error(t, err)
}

func TestMemberTenantResolutionNeverGuessesOrUsesInactiveMemberships(t *testing.T) {
	h := newCoreServiceHarness(t)
	m := boundChecker(t, h.runtime, "sl_")
	resolve := func(principal, requested, expected guid.ID) {
		t.Helper()
		id, err := m.ResolveTenant(t.Context(), principal, requested)
		require.NoError(t, err)
		assert.Equal(t, expected, id)
	}
	resolve(11, 0, 0)
	h.seedMember(t, 1, 11, MemberActive)
	resolve(11, 0, 1)
	resolve(11, 1, 1)
	resolve(11, 2, 0)
	h.seedMember(t, 2, 22, MemberActive)
	_, err := h.service.PutTenantMember(t.Context(), 2, 11, "Member 11", "", 0, MemberActive)
	require.NoError(t, err)
	resolve(11, 0, 0)
	resolve(11, 2, 2)
	resolve(22, 0, 2)
	resolve(22, 1, 0)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenant_members SET status = 'disabled' WHERE tenant_id = 2 AND principal_id = 11").Error)
	resolve(11, 0, 1)
	resolve(11, 2, 0)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status = 'suspended' WHERE id = 1").Error)
	resolve(11, 0, 0)
	resolve(11, 1, 0)
	for _, input := range [][2]guid.ID{{0, 0}, {-1, 0}, {11, -1}} {
		_, err := m.ResolveTenant(t.Context(), input[0], input[1])
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
	var absent *MembershipChecker
	_, err = absent.ResolveTenant(t.Context(), 11, 0)
	assert.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_tenant_members").Error)
	_, err = m.ResolveTenant(t.Context(), 11, 0)
	require.ErrorContains(t, err, "resolve active member tenant")
}

func TestOperatingPlatformMembershipIsActiveOnlyOnPlatformRoots(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedOperatingPlatform(t, h, 1, 11)
	h.seedMember(t, 2, 22, MemberActive)
	seedHTTPPrincipal(t, h.repositoryHarness, 33)
	_, err := h.service.PutOperatingPlatformMember(t.Context(), 1, 33, "Disabled", "", 0, MemberDisabled)
	require.NoError(t, err)
	checker := NewMembershipChecker(h.runtime)
	require.NoError(t, checker.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	for _, tc := range []struct {
		name            string
		root, principal guid.ID
		active          bool
	}{
		{"active platform member", 1, 11, true},
		{"disabled platform member", 1, 33, false},
		{"tenant member is not a platform member", 2, 22, false},
		{"not a member", 1, 22, false},
		{"unknown root", 987654321, 11, false},
	} {
		active, err := checker.IsOperatingPlatformMemberActive(t.Context(), tc.root, tc.principal)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.active, active, tc.name)
	}
	for name, unwired := range map[string]*MembershipChecker{"nil checker": nil, "no runtime": {}} {
		active, err := unwired.IsOperatingPlatformMemberActive(t.Context(), 1, 11)
		require.ErrorIs(t, err, ErrUnavailable, name)
		require.False(t, active, name)
	}
}
