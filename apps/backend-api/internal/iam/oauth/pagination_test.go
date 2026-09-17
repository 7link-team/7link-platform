package oauth

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tenant's OAuth client registry grows without an upper bound, so a page has
// to be bounded by what the caller asked for rather than by what the table
// happens to hold. Each assertion fails on its own if one half of that stops
// working: the limit, the offset, or the unfiltered total.
func TestListClientsPageBoundsTheResult(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	for index := range 5 {
		_, _, err := harness.service.CreateClient(
			t.Context(), 7, fmt.Sprintf("Client %02d", index), nil,
			[]string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
		)
		require.NoError(t, err)
	}

	first, total, err := harness.service.ListClientsPage(t.Context(), 7, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every client, not the page")

	second, total, err := harness.service.ListClientsPage(t.Context(), 7, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedClientIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := harness.service.ListClientsPage(t.Context(), 7, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	past, _, err := harness.service.ListClientsPage(t.Context(), 7, 99, 2)
	require.NoError(t, err)
	assert.Empty(t, past)

	other, total, err := harness.service.ListClientsPage(t.Context(), 8, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = harness.service.ListClientsPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

// normalizePage is the only place the repository ceiling lives, so a caller
// below the transport can neither widen the window past it nor invert it.
func TestOAuthNormalizePageClampsTheWindow(t *testing.T) {
	offset, limit := normalizePage(-5, -3)
	assert.Zero(t, offset)
	assert.Zero(t, limit)
	offset, limit = normalizePage(7, maxPageLimit+1)
	assert.Equal(t, 7, offset)
	assert.Equal(t, maxPageLimit, limit)

	harness := newOAuthHarness(t, "")
	for index := range 3 {
		_, _, err := harness.service.CreateClient(
			t.Context(), 7, fmt.Sprintf("Client %02d", index), nil,
			[]string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
		)
		require.NoError(t, err)
	}
	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to be imposed where the caller is.
	all, total, err := harness.repo.listClientsPage(t.Context(), 7, -1, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, int64(3), total)
}

func TestListClientsPageSurfacesStorageFailures(t *testing.T) {
	harness := newOAuthHarness(t, "")
	require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_clients").Error)
	_, _, err := harness.service.ListClientsPage(t.Context(), 7, 0, 2)
	assert.Error(t, err)

	var unavailable *Repository
	_, _, err = unavailable.listClientsPage(t.Context(), 7, 0, 2)
	assert.Error(t, err)
}

func sharedClientIDs(left, right []Client) []string {
	seen := make(map[string]bool, len(left))
	for _, item := range left {
		seen[item.ID] = true
	}
	var both []string
	for _, item := range right {
		if seen[item.ID] {
			both = append(both, item.ID)
		}
	}
	return both
}
