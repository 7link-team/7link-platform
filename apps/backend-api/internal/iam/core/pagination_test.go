package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A role's membership grows with the tenant, so the listing has to be bounded
// by what the caller asked for rather than by what the table happens to hold.
// Each assertion here fails on its own if one half of that stops working: the
// limit, the offset, the unfiltered total, or the role check that has to run
// before any of them.
func TestListRoleMembersPageBoundsTheResult(t *testing.T) {
	harness := newCoreServiceHarness(t)
	repository := harness.repository
	principals := []guid.ID{11, 22, 33, 44, 55}
	// seedTenantAndPrincipal inserts the tenant as well, so only the first call
	// may carry it; the rest would collide on the tenant's primary key.
	seedTenantAndPrincipal(t, harness.db, "sl_", 1, principals[0])
	for _, principal := range principals[1:] {
		seedHTTPPrincipal(t, harness.repositoryHarness, principal)
	}
	for _, principal := range principals {
		_, err := repository.PutMember(t.Context(), TenantMember{
			TenantID: 1, PrincipalID: principal, DisplayName: "Member", Email: principal.String() + "@example.test", Status: MemberActive,
		})
		require.NoError(t, err)
	}
	role, err := repository.CreateRole(t.Context(), 1, "Operators", "")
	require.NoError(t, err)
	for _, principal := range principals {
		_, err = repository.AddMember(t.Context(), 1, role.ID, principal)
		require.NoError(t, err)
	}

	first, total, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(len(principals)), total, "the total counts every holder, not the page")

	second, total, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(len(principals)), total)
	assert.NotEqual(t, first, second, "the offset must move the window")
	assert.Empty(t, intersect(first, second), "pages must not overlap")

	last, _, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to come from somewhere, and the transport is
	// where a caller can be held to it.
	all, _, err := repository.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 0)
	require.NoError(t, err)
	assert.Len(t, all, len(principals))

	// An unknown role is not an empty membership. Reporting it as one would let a
	// caller conclude a role exists and has nobody in it.
	_, _, err = repository.ListRoleMembersPage(t.Context(), 1, role.ID+9999, 0, 2)
	assert.Error(t, err)

	_, _, err = harness.service.ListRoleMembersPage(t.Context(), 0, role.ID, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	page, total, err := harness.service.ListRoleMembersPage(t.Context(), 1, role.ID, 0, 3)
	require.NoError(t, err)
	assert.Len(t, page, 3)
	assert.Equal(t, int64(len(principals)), total)
}

func intersect(left, right []guid.ID) []guid.ID {
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
