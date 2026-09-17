package provisioning

import (
	"fmt"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each of these listings grows with the tenant, so a page has to be bounded by
// what the caller asked for rather than by what the table happens to hold. Every
// assertion fails on its own if one half of that stops working: the limit, the
// offset, or the unfiltered total.
func TestListDirectoriesPageBoundsTheResult(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenantID := testID("tenant-a")
	// The environment already seeds one directory, so four more make five.
	for index := range 4 {
		_, err := env.service.CreateDirectory(t.Context(), tenantID, fmt.Sprintf("Directory %02d", index), nil)
		require.NoError(t, err)
	}

	first, total, err := env.service.ListDirectoriesPage(t.Context(), tenantID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every directory, not the page")

	second, total, err := env.service.ListDirectoriesPage(t.Context(), tenantID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedDirectoryIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := env.service.ListDirectoriesPage(t.Context(), tenantID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	past, _, err := env.service.ListDirectoriesPage(t.Context(), tenantID, 99, 2)
	require.NoError(t, err)
	assert.Empty(t, past)

	other, total, err := env.service.ListDirectoriesPage(t.Context(), testID("tenant-b"), 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = env.service.ListDirectoriesPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidDirectory)
}

func TestListTargetsPageBoundsTheResult(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenantID := testID("tenant-a")
	for index := range 5 {
		_, err := env.service.CreateTarget(t.Context(), tenantID,
			fmt.Sprintf("Target %02d", index), fmt.Sprintf("https://scim.example.test/v%d", index), "secret", nil)
		require.NoError(t, err)
	}

	first, total, err := env.service.ListTargetsPage(t.Context(), tenantID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every target, not the page")

	second, total, err := env.service.ListTargetsPage(t.Context(), tenantID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedTargetIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := env.service.ListTargetsPage(t.Context(), tenantID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1)

	other, total, err := env.service.ListTargetsPage(t.Context(), testID("tenant-b"), 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = env.service.ListTargetsPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidTarget)
}

func TestListCredentialsPageBoundsTheResult(t *testing.T) {
	env := newProvisioningEnvironment(t)
	tenantID, directoryID := testID("tenant-a"), env.auth.DirectoryID
	// The environment already seeds one credential, so four more make five.
	for index := range 4 {
		_, err := env.service.CreateCredential(t.Context(), tenantID, directoryID, fmt.Sprintf("Credential %02d", index), nil)
		require.NoError(t, err)
	}

	first, total, err := env.service.ListCredentialsPage(t.Context(), tenantID, directoryID, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every credential, not the page")

	second, total, err := env.service.ListCredentialsPage(t.Context(), tenantID, directoryID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedCredentialIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := env.service.ListCredentialsPage(t.Context(), tenantID, directoryID, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1)

	// An unknown directory is not an empty credential set: reporting it as one
	// would let a caller conclude the directory exists and holds no credentials.
	_, _, err = env.service.ListCredentialsPage(t.Context(), tenantID, testID("missing"), 0, 2)
	assert.ErrorIs(t, err, ErrDirectoryMissing)
	_, _, err = env.service.ListCredentialsPage(t.Context(), testID("tenant-b"), directoryID, 0, 2)
	assert.ErrorIs(t, err, ErrDirectoryMissing)
}

// normalizePage is the only place the repository ceiling lives, so a caller
// below the transport can neither widen the window past it nor invert it.
func TestProvisioningNormalizePageClampsTheWindow(t *testing.T) {
	offset, limit := normalizePage(-5, -3)
	assert.Zero(t, offset)
	assert.Zero(t, limit)
	offset, limit = normalizePage(7, maxPageLimit+1)
	assert.Equal(t, 7, offset)
	assert.Equal(t, maxPageLimit, limit)

	env := newProvisioningEnvironment(t)
	tenantID := testID("tenant-a")
	for index := range 2 {
		_, err := env.service.CreateDirectory(t.Context(), tenantID, fmt.Sprintf("Directory %02d", index), nil)
		require.NoError(t, err)
	}
	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to be imposed where the caller is.
	all, total, err := env.service.repo.listDirectoriesPage(t.Context(), tenantID, -1, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, int64(3), total)
}

func TestProvisioningPagedListingsSurfaceStorageFailures(t *testing.T) {
	t.Run("directories", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_directories").Error)
		_, _, err := repo.listDirectoriesPage(t.Context(), 1, 0, 2)
		assert.Error(t, err)
	})
	t.Run("targets", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_targets").Error)
		_, _, err := repo.listTargetsPage(t.Context(), 1, 0, 2)
		assert.Error(t, err)
	})
	t.Run("credentials", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_credentials").Error)
		_, _, err := repo.listCredentialsPage(t.Context(), 1, 0, 2)
		assert.Error(t, err)
	})
}

func sharedDirectoryIDs(left, right []Directory) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedIDs(ids, other)
}

func sharedTargetIDs(left, right []Target) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, parseGUID(item.ID))
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, parseGUID(item.ID))
	}
	return sharedIDs(ids, other)
}

func sharedCredentialIDs(left, right []Credential) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedIDs(ids, other)
}

func sharedIDs(left, right []guid.ID) []guid.ID {
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
