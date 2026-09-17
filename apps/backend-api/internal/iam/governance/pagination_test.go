package governance

import (
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tenant's role catalog grows with the tenant, so a page has to be bounded
// by what the caller asked for rather than by what the table happens to hold.
// Each assertion fails on its own if one half of that stops working: the limit,
// the offset, or the unfiltered total.
func TestRequestableRolesPageBoundsTheResult(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	h.seedTenant(t, 1)
	h.seedTenant(t, 2)
	for index := range 5 {
		h.seedRole(t, 1, guid.ID(20+index), fmt.Sprintf("Role %02d", index))
	}

	first, total, err := h.service.RequestableRolesPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every role, not the page")

	second, total, err := h.service.RequestableRolesPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedRoleIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := h.service.RequestableRolesPage(t.Context(), 1, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	past, _, err := h.service.RequestableRolesPage(t.Context(), 1, 99, 2)
	require.NoError(t, err)
	assert.Empty(t, past)

	other, total, err := h.service.RequestableRolesPage(t.Context(), 2, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = h.service.RequestableRolesPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalid)
	var unavailable *Service
	_, _, err = unavailable.RequestableRolesPage(t.Context(), 1, 0, 2)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = (&Service{repo: &Repository{}}).RequestableRolesPage(t.Context(), 1, 0, 2)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

func TestListAccessRequestsPageBoundsTheResult(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	h.seedTenant(t, 1)
	h.seedTenant(t, 2)
	h.seedMember(t, 1, 10, "Requester")
	h.seedMember(t, 1, 11, "Second requester")
	// Five distinct roles keep every request independent of the others.
	for index := range 5 {
		h.seedRole(t, 1, guid.ID(20+index), fmt.Sprintf("Role %02d", index))
		requester := guid.ID(10)
		if index == 4 {
			requester = 11
		}
		_, err := h.service.Create(t.Context(), 1, requester, CreateAccessRequest{
			RoleID: guid.ID(20 + index), Reason: "temporary operations access",
			AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
		})
		require.NoError(t, err)
	}

	first, total, err := h.service.ListPage(t.Context(), 1, 0, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every request, not the page")

	second, total, err := h.service.ListPage(t.Context(), 1, 0, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedRequestIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := h.service.ListPage(t.Context(), 1, 0, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1)

	// The requester filter narrows the total too, not just the page: a caller
	// paging their own requests must not be told how many the tenant holds.
	mine, total, err := h.service.ListPage(t.Context(), 1, 11, 0, 2)
	require.NoError(t, err)
	assert.Len(t, mine, 1)
	assert.Equal(t, int64(1), total)

	other, total, err := h.service.ListPage(t.Context(), 2, 0, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = h.service.ListPage(t.Context(), 0, 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalid)
	var unavailable *Service
	_, _, err = unavailable.ListPage(t.Context(), 1, 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = (&Service{repo: &Repository{}}).ListPage(t.Context(), 1, 0, 0, 2)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

func TestListReviewsPageBoundsTheResult(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	seedReviewableGrants(t, h)
	h.seedTenant(t, 2)
	for index := range 5 {
		_, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
			Name: fmt.Sprintf("Review %02d", index), DueAt: governanceTestNow.Add(24 * time.Hour),
		})
		require.NoError(t, err)
	}

	first, total, err := h.service.ListReviewsPage(t.Context(), 1, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every campaign, not the page")
	assert.Positive(t, first[0].Total, "a page still carries the per-review item counts")

	second, total, err := h.service.ListReviewsPage(t.Context(), 1, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedReviewIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := h.service.ListReviewsPage(t.Context(), 1, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1)

	other, total, err := h.service.ListReviewsPage(t.Context(), 2, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = h.service.ListReviewsPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidReview)
	var unavailable *Service
	_, _, err = unavailable.ListReviewsPage(t.Context(), 1, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidReview)
	_, _, err = (&Service{repo: &Repository{}}).ListReviewsPage(t.Context(), 1, 0, 2)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

// normalizePage is the only place the ceiling lives, so a caller below the
// transport can neither widen the window past it nor invert it.
func TestGovernanceNormalizePageClampsTheWindow(t *testing.T) {
	offset, limit := normalizePage(-5, -3)
	assert.Zero(t, offset)
	assert.Zero(t, limit)
	offset, limit = normalizePage(7, maxPageLimit+1)
	assert.Equal(t, 7, offset)
	assert.Equal(t, maxPageLimit, limit)

	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	for index := range 3 {
		h.seedRole(t, 1, guid.ID(20+index), fmt.Sprintf("Role %02d", index))
	}
	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to be imposed where the caller is.
	all, total, err := h.service.RequestableRolesPage(t.Context(), 1, -1, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, int64(3), total)
}

func TestGovernancePagedListingsSurfaceStorageFailures(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	for _, table := range []string{"iam_roles", "iam_access_requests", "iam_access_reviews"} {
		require.NoError(t, h.db.Exec("DROP TABLE "+h.table(table)).Error)
	}
	_, _, err := h.service.RequestableRolesPage(t.Context(), 1, 0, 2)
	assert.Error(t, err)
	_, _, err = h.service.ListPage(t.Context(), 1, 0, 0, 2)
	assert.Error(t, err)
	_, _, err = h.service.ListReviewsPage(t.Context(), 1, 0, 2)
	assert.Error(t, err)
}

func sharedRoleIDs(left, right []RequestableRole) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedGovernanceIDs(ids, other)
}

func sharedRequestIDs(left, right []AccessRequest) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedGovernanceIDs(ids, other)
}

func sharedReviewIDs(left, right []AccessReview) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedGovernanceIDs(ids, other)
}

func sharedGovernanceIDs(left, right []guid.ID) []guid.ID {
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
