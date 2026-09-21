package organization

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overlappingIDs reports the identifiers two pages have in common. An empty
// result is what proves the offset moved the window rather than re-reading the
// same rows under a different name.
func overlappingIDs(left, right []guid.ID) []guid.ID {
	seen := make(map[guid.ID]bool, len(left))
	for _, id := range left {
		seen[id] = true
	}
	var both []guid.ID
	for _, id := range right {
		if seen[id] {
			both = append(both, id)
		}
	}
	return both
}

func departmentIDs(items []Department) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func tenantIDs(items []Tenant) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func positionIDs(items []Position) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func positionMemberIDs(items []PositionMember) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.PrincipalID
	}
	return ids
}

func groupIDs(items []Group) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

func groupMemberIDs(items []GroupMember) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.PrincipalID
	}
	return ids
}

func invitationIDs(items []Invitation) []guid.ID {
	ids := make([]guid.ID, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	return ids
}

// TestPageWindowArithmetic pins the two helpers every Page method leans on. The
// clamp is what keeps a hostile offset or limit from reaching the database, and
// pageOf is the only bound the listings that cannot page in SQL have at all.
func TestPageWindowArithmetic(t *testing.T) {
	t.Parallel()
	offset, limit := normalizePage(-1, -1)
	assert.Equal(t, []int{0, 0}, []int{offset, limit}, "a negative window collapses to the unbounded one")
	offset, limit = normalizePage(3, maxPageLimit+1)
	assert.Equal(t, []int{3, maxPageLimit}, []int{offset, limit}, "no caller may ask for more than the ceiling")

	items := []int{1, 2, 3, 4, 5}
	assert.Equal(t, []int{1, 2}, pageOf(items, 0, 2), "the limit bounds the window")
	assert.Equal(t, []int{3, 4}, pageOf(items, 2, 2), "the offset moves the window")
	assert.Equal(t, []int{5}, pageOf(items, 4, 2), "a final short page returns what is left")
	assert.Empty(t, pageOf(items, 9, 2), "an offset past the end is an empty page, not a panic")
	assert.Equal(t, items, pageOf(items, 0, 0), "zero still means unbounded at this layer")
	assert.Equal(t, items, pageOf(items, 0, 9), "a limit past the end returns what there is")
}

// A tenant's department tree grows with the organization, so the listing has to
// be bounded by what the caller asked for rather than by what the table holds.
// The page is taken after the tree is ordered, so this also pins that a page
// carries the same order and the same Depth the unpaged listing reports.
func TestDepartmentListPageBoundsTheHierarchy(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	roots := make([]Department, 0, 5)
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"} {
		department, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, Name: name})
		require.NoError(t, err)
		roots = append(roots, department)
	}
	child, err := h.service.Create(t.Context(), 1, CreateDepartment{EntityID: organizationEntityOne, ParentID: roots[0].ID, Name: "Platform"})
	require.NoError(t, err)
	const departments = 6

	full, err := h.service.List(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, full, departments)

	first, total, err := h.service.ListPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(departments), total, "the total counts the whole hierarchy, not the page")
	assert.Equal(t, departmentIDs(full[:2]), departmentIDs(first), "a page keeps the order the unpaged listing has")
	assert.Equal(t, child.ID, first[1].ID, "the child follows its parent, so it is inside the first page")
	assert.Equal(t, 1, first[1].Depth, "paging must not flatten the depth away")

	second, total, err := h.service.ListPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(departments), total)
	assert.Empty(t, overlappingIDs(departmentIDs(first), departmentIDs(second)), "the offset must move the window")

	last, total, err := h.service.ListPage(t.Context(), 1, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 2, "a final page returns what is left")
	assert.Equal(t, int64(departments), total)
	assert.Empty(t, overlappingIDs(departmentIDs(second), departmentIDs(last)))

	beyond, total, err := h.service.ListPage(t.Context(), 1, departments, 2)
	require.NoError(t, err)
	assert.Empty(t, beyond, "an offset past the end is an empty page, not an error")
	assert.Equal(t, int64(departments), total, "the total still reports what a caller could page to")

	_, _, err = h.service.ListPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalid, "the tenant is still required")
}

// The platform tenant listing is the one collection nothing else narrows: it
// grows with the deployment rather than with one tenant's data.
func TestTenantListPageBoundsThePlatformListing(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	for id := guid.ID(1); id <= 5; id++ {
		h.seedTenant(t, id, TenantActive)
	}
	h.seedTenant(t, 6, TenantDeleted)
	service, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(1000))

	first, total, err := service.ListPage(t.Context(), false, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every live tenant, not the page")

	second, total, err := service.ListPage(t.Context(), false, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, overlappingIDs(tenantIDs(first), tenantIDs(second)), "the offset must move the window")

	last, _, err := service.ListPage(t.Context(), false, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	unpaged, err := service.List(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, tenantIDs(unpaged)[:2], tenantIDs(first), "a page keeps the order the unpaged listing has")

	deleted, total, err := service.ListPage(t.Context(), true, 0, 10)
	require.NoError(t, err)
	assert.Len(t, deleted, 6, "include_deleted widens the page")
	assert.Equal(t, int64(6), total, "and the total follows the same filter the page does")
}

func newPositionServiceForPaging(t *testing.T, h *organizationHarness) *PositionService {
	t.Helper()
	service := newBoundPositionService(
		t, h.runtime, h.appender,
		&organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{},
		newOrganizationTestIDs(300),
	)
	service.now = func() time.Time { return organizationTestNow }
	return service
}

// Positions are bounded by the tenant, but a position's holders grow with the
// tenant's headcount, which is the listing that gets long first.
func TestPositionListPageAndMembersPageBoundTheirListings(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	principals := []guid.ID{10, 11, 12, 13, 14}
	for index, principal := range principals {
		h.seedMember(t, 1, principal, fmt.Sprintf("Member %d", index))
	}
	service := newPositionServiceForPaging(t, h)
	positions := make([]Position, 0, 5)
	for index := 1; index <= 5; index++ {
		position, err := service.Create(t.Context(), 1, CreatePosition{
			Code: fmt.Sprintf("code%d", index), Name: fmt.Sprintf("Position %d", index), SortOrder: index,
		})
		require.NoError(t, err)
		positions = append(positions, position)
	}
	for _, principal := range principals {
		_, err := service.PutMember(t.Context(), 1, positions[0].ID, principal, PositionMemberWindow{})
		require.NoError(t, err)
	}

	first, total, err := service.ListPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the position page")
	assert.Equal(t, int64(len(positions)), total, "the total counts every tenant position")
	second, total, err := service.ListPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(positions)), total)
	assert.Empty(t, overlappingIDs(positionIDs(first), positionIDs(second)), "the offset must move the window")
	unpaged, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, positionIDs(unpaged)[:2], positionIDs(first), "a page keeps the unpaged order")

	firstMembers, total, err := service.ListMembersPage(t.Context(), 1, positions[0].ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, firstMembers, 2, "the limit must bound the membership page")
	assert.Equal(t, int64(len(principals)), total, "the total counts every holder, not the page")
	secondMembers, total, err := service.ListMembersPage(t.Context(), 1, positions[0].ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, secondMembers, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.Empty(t, overlappingIDs(positionMemberIDs(firstMembers), positionMemberIDs(secondMembers)),
		"the offset must move the membership window")
	lastMembers, _, err := service.ListMembersPage(t.Context(), 1, positions[0].ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, lastMembers, 1, "a final short page returns what is left, not an error")

	empty, total, err := service.ListMembersPage(t.Context(), 1, positions[1].ID, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Zero(t, total, "a position nobody holds reports a total of zero rather than the tenant's")

	// An unknown position is not an empty membership: reporting it as one would
	// let a caller conclude the position exists and nobody holds it.
	_, _, err = service.ListMembersPage(t.Context(), 1, positions[0].ID+9999, 0, 2)
	assert.ErrorIs(t, err, ErrPositionNotFound)
	_, _, err = service.ListMembersPage(t.Context(), 1, 0, 0, 2)
	assert.ErrorIs(t, err, ErrPositionInvalid)
	_, _, err = service.ListPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrPositionInvalid)
}

func newGroupServiceForPaging(t *testing.T, h *organizationHarness) *GroupService {
	t.Helper()
	service := newBoundGroupService(
		t, h.runtime, h.appender,
		&organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{},
		newOrganizationTestIDs(400),
	)
	service.now = func() time.Time { return organizationTestNow }
	return service
}

// A group's membership is the listing most exposed to tenant growth, and it has
// two shapes: a static group's stored rows, which SQL can window, and a dynamic
// group's rule verdict, which only Go can decide. Both have to come back
// bounded, and both have to report the unpaged total.
func TestGroupListPageAndMembersPageBoundTheirListings(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	principals := []guid.ID{10, 11, 12, 13, 14}
	for index, principal := range principals {
		h.seedMember(t, 1, principal, fmt.Sprintf("Member %d", index))
	}
	service := newGroupServiceForPaging(t, h)
	groups := make([]Group, 0, 5)
	for index := 1; index <= 5; index++ {
		group, err := service.Create(t.Context(), 1, CreateGroup{
			Name: fmt.Sprintf("Group %d", index), SortOrder: index,
		})
		require.NoError(t, err)
		groups = append(groups, group)
	}
	for _, principal := range principals {
		_, err := service.PutMember(t.Context(), 1, groups[0].ID, principal, GroupMemberWindow{})
		require.NoError(t, err)
	}

	first, total, err := service.ListPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the group page")
	assert.Equal(t, int64(len(groups)), total, "the total counts every tenant group")
	second, total, err := service.ListPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(groups)), total)
	assert.Empty(t, overlappingIDs(groupIDs(first), groupIDs(second)), "the offset must move the window")
	unpaged, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, groupIDs(unpaged)[:2], groupIDs(first), "a page keeps the unpaged order")

	firstMembers, total, err := service.ListMembersPage(t.Context(), 1, groups[0].ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, firstMembers, 2, "the limit must bound the membership page")
	assert.Equal(t, int64(len(principals)), total, "the total counts every member, not the page")
	secondMembers, total, err := service.ListMembersPage(t.Context(), 1, groups[0].ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, secondMembers, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.Empty(t, overlappingIDs(groupMemberIDs(firstMembers), groupMemberIDs(secondMembers)),
		"the offset must move the membership window")
	lastMembers, _, err := service.ListMembersPage(t.Context(), 1, groups[0].ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, lastMembers, 1, "a final short page returns what is left, not an error")

	_, _, err = service.ListMembersPage(t.Context(), 1, groups[0].ID+9999, 0, 2)
	assert.ErrorIs(t, err, ErrGroupNotFound, "an unknown group is not an empty membership")
	_, _, err = service.ListMembersPage(t.Context(), 1, 0, 0, 2)
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, _, err = service.ListPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrGroupInvalid)
}

// The dynamic half of the same guarantee. The rule matches every seeded member,
// so a page that came back unbounded here would be the whole tenant.
func TestDynamicGroupMembersPageBoundsTheEvaluatedMembership(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	principals := []guid.ID{10, 11, 12, 13, 14}
	for index, principal := range principals {
		h.seedMember(t, 1, principal, fmt.Sprintf("Member %d", index))
	}
	service := newGroupServiceForPaging(t, h)
	rule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`)
	group, err := service.Create(t.Context(), 1, CreateGroup{Name: "Everyone", Type: GroupTypeDynamic, Rule: rule})
	require.NoError(t, err)

	matched, err := service.ListMembers(t.Context(), 1, group.ID)
	require.NoError(t, err)
	require.Len(t, matched, len(principals), "the rule matches every seeded member")

	first, total, err := service.ListMembersPage(t.Context(), 1, group.ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the evaluated page")
	assert.Equal(t, int64(len(principals)), total, "the total is the matched count, not the page")
	assert.Equal(t, groupMemberIDs(matched)[:2], groupMemberIDs(first),
		"a page agrees with the unpaged evaluation about who is in the group")

	second, total, err := service.ListMembersPage(t.Context(), 1, group.ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.Empty(t, overlappingIDs(groupMemberIDs(first), groupMemberIDs(second)), "the offset must move the window")

	last, _, err := service.ListMembersPage(t.Context(), 1, group.ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left")

	beyond, total, err := service.ListMembersPage(t.Context(), 1, group.ID, len(principals), 2)
	require.NoError(t, err)
	assert.Empty(t, beyond, "an offset past the end is an empty page, not an error")
	assert.Equal(t, int64(len(principals)), total)
}

// Invitations accumulate: every issue, resend and revoke leaves a row behind,
// so this listing grows even when the tenant's headcount does not.
func TestInvitationListPageBoundsTheListing(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedTenant(t, 1, TenantActive)
	var principalCalls atomic.Int64
	service := newInvitationService(t, h, &invitationCredentialFixture{}, &principalCalls)
	issued := make([]Invitation, 0, 5)
	for index := 1; index <= 5; index++ {
		invitation, _, err := service.Create(t.Context(), 1, CreateInvitation{
			Email: fmt.Sprintf("invitee%d@example.com", index),
		})
		require.NoError(t, err)
		issued = append(issued, invitation)
	}

	first, total, err := service.ListPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(issued)), total, "the total counts every invitation, not the page")

	second, total, err := service.ListPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(issued)), total)
	assert.Empty(t, overlappingIDs(invitationIDs(first), invitationIDs(second)), "the offset must move the window")

	last, _, err := service.ListPage(t.Context(), 1, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	unpaged, err := service.List(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, invitationIDs(unpaged)[:2], invitationIDs(first), "a page keeps the unpaged order")

	other, total, err := service.ListPage(t.Context(), 2, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "another tenant's invitations stay invisible")
	assert.Zero(t, total, "and so does their count")

	_, _, err = service.ListPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvitationInvalid)
}

// TestPagedListingsSurfaceStorageFailuresInsteadOfAnEmptyPage pins that every
// bounded listing reports a broken read. A page that swallowed the failure
// would answer with an empty window and a zero total, which reads exactly like
// a tenant that genuinely owns nothing, and a caller paging through it would
// conclude the rows had been deleted.
func TestPagedListingsSurfaceStorageFailuresInsteadOfAnEmptyPage(t *testing.T) {
	for _, test := range []struct {
		name  string
		table string
		call  func(*testing.T, *organizationHarness) error
	}{
		{name: "tenants", table: "iam_tenants", call: func(t *testing.T, h *organizationHarness) error {
			service, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(2800))
			_, _, err := service.ListPage(t.Context(), false, 0, 10)
			return err
		}},
		{name: "positions", table: "iam_positions", call: func(t *testing.T, h *organizationHarness) error {
			_, _, err := newPositionServiceForPaging(t, h).ListPage(t.Context(), 1, 0, 10)
			return err
		}},
		{name: "groups", table: "iam_groups", call: func(t *testing.T, h *organizationHarness) error {
			_, _, err := newGroupServiceForPaging(t, h).ListPage(t.Context(), 1, 0, 10)
			return err
		}},
		{name: "invitations", table: "iam_invitations", call: func(t *testing.T, h *organizationHarness) error {
			var principalCalls atomic.Int64
			service := newInvitationService(t, h, &invitationCredentialFixture{}, &principalCalls)
			_, _, err := service.ListPage(t.Context(), 1, 0, 10)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newOrganizationHarness(t, "sl_")
			require.NoError(t, h.db.Exec("DROP TABLE sl_"+test.table).Error)
			assert.Error(t, test.call(t, h))
		})
	}

	t.Run("position members", func(t *testing.T) {
		h := newOrganizationHarness(t, "sl_")
		service := newPositionServiceForPaging(t, h)
		created, err := service.Create(t.Context(), 1, CreatePosition{Code: "engineer", Name: "Engineer"})
		require.NoError(t, err)
		_, _, err = service.ListMembersPage(t.Context(), 1, 987654, 0, 10)
		assert.ErrorIs(t, err, ErrPositionNotFound, "an unknown position is missing, not a position nobody holds")
		require.NoError(t, h.db.Exec("DROP TABLE sl_iam_position_members").Error)
		_, _, err = service.ListMembersPage(t.Context(), 1, created.ID, 0, 10)
		assert.Error(t, err)
	})

	t.Run("group members", func(t *testing.T) {
		h := newOrganizationHarness(t, "sl_")
		service := newGroupServiceForPaging(t, h)
		created, err := service.Create(t.Context(), 1, CreateGroup{Name: "Operators"})
		require.NoError(t, err)
		_, _, err = service.ListMembersPage(t.Context(), 1, 987654, 0, 10)
		assert.ErrorIs(t, err, ErrGroupNotFound, "an unknown group is missing, not a group nobody is in")
		require.NoError(t, h.db.Exec("DROP TABLE sl_iam_group_members").Error)
		_, _, err = service.ListMembersPage(t.Context(), 1, created.ID, 0, 10)
		assert.Error(t, err)
	})

	t.Run("dynamic group members", func(t *testing.T) {
		h := newOrganizationHarness(t, "sl_")
		service := newGroupServiceForPaging(t, h)
		rule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`)
		created, err := service.Create(t.Context(), 1, CreateGroup{Name: "Everyone", Type: GroupTypeDynamic, Rule: rule})
		require.NoError(t, err)
		require.NoError(t, h.db.Exec("DROP TABLE sl_iam_tenant_members").Error)
		_, _, err = service.ListMembersPage(t.Context(), 1, created.ID, 0, 10)
		assert.Error(t, err, "an unevaluatable rule cannot be reported as an empty membership")
	})
}

// TestUnwiredRepositoryRefusesPagedListings pins that the paged reads resolve
// their physical table names before they touch the writer. A page that skipped
// that guard would dereference a nil writer and take the process down instead
// of handing the caller an error it can report.
func TestUnwiredRepositoryRefusesPagedListings(t *testing.T) {
	t.Parallel()
	unavailable := &Repository{}
	_, _, err := unavailable.listTenantsPage(t.Context(), false, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.listPositionsPage(t.Context(), 1, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.listPositionMembersPage(t.Context(), 1, 2, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.listGroupsPage(t.Context(), 1, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.listGroupMembersPage(t.Context(), 1, 2, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.listInvitationsPage(t.Context(), 1, 0, 10)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}
