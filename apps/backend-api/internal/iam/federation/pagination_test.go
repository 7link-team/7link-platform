package federation

import (
	"fmt"
	"testing"

	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tenant's provider registry grows without an upper bound, so a page has to be
// bounded by what the caller asked for rather than by what the table happens to
// hold. Each assertion fails on its own if one half of that stops working: the
// limit, the offset, or the unfiltered total.
func TestListProvidersPageBoundsTheResult(t *testing.T) {
	env := newFederationEnvironment(t, "sl_")
	tenantA, tenantB := federationID("tenant-a"), federationID("tenant-b")
	for index := range 5 {
		input := env.providerInput(fmt.Sprintf("Provider %02d", index), fmt.Sprintf("https://issuer%d.example/", index))
		env.createProvider(t, tenantA, input)
	}

	first, total, err := env.service.ListProvidersPage(t.Context(), tenantA, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every provider, not the page")

	second, total, err := env.service.ListProvidersPage(t.Context(), tenantA, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedProviderIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := env.service.ListProvidersPage(t.Context(), tenantA, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1, "a final short page returns what is left, not an error")

	past, _, err := env.service.ListProvidersPage(t.Context(), tenantA, 99, 2)
	require.NoError(t, err)
	assert.Empty(t, past)

	other, total, err := env.service.ListProvidersPage(t.Context(), tenantB, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = env.service.ListProvidersPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidProvider)
}

func TestListSAMLServiceProvidersPageBoundsTheResult(t *testing.T) {
	env := newFederationEnvironment(t)
	tenantA, tenantB := testID("tenant-a"), testID("tenant-b")
	for index := range 5 {
		seedSAMLServiceProvider(t, env, tenantA, index)
	}

	first, total, err := env.service.ListSAMLServiceProvidersPage(t.Context(), tenantA, 0, 2)
	require.NoError(t, err)
	assert.Len(t, first, 2, "the limit must bound the page")
	assert.Equal(t, int64(5), total, "the total counts every service provider, not the page")

	second, total, err := env.service.ListSAMLServiceProvidersPage(t.Context(), tenantA, 2, 2)
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, int64(5), total)
	assert.Empty(t, sharedSAMLSPIDs(first, second), "the offset must move the window, not repeat it")

	last, _, err := env.service.ListSAMLServiceProvidersPage(t.Context(), tenantA, 4, 2)
	require.NoError(t, err)
	assert.Len(t, last, 1)

	other, total, err := env.service.ListSAMLServiceProvidersPage(t.Context(), tenantB, 0, 2)
	require.NoError(t, err)
	assert.Empty(t, other, "the page stays inside the tenant")
	assert.Zero(t, total)

	_, _, err = env.service.ListSAMLServiceProvidersPage(t.Context(), 0, 0, 2)
	assert.ErrorIs(t, err, ErrInvalidSAMLSP)
}

// normalizePage is the only place the repository ceiling lives, so a caller
// below the transport can neither widen the window past it nor invert it.
func TestFederationNormalizePageClampsTheWindow(t *testing.T) {
	offset, limit := normalizePage(-5, -3)
	assert.Zero(t, offset)
	assert.Zero(t, limit)
	offset, limit = normalizePage(7, maxPageLimit+1)
	assert.Equal(t, 7, offset)
	assert.Equal(t, maxPageLimit, limit)

	env := newFederationEnvironment(t)
	tenantA := testID("tenant-a")
	for index := range 3 {
		seedSAMLServiceProvider(t, env, tenantA, index)
	}
	// Zero means unbounded at this layer, which is why the HTTP input declares a
	// minimum of one: the bound has to be imposed where the caller is.
	all, total, err := env.service.repo.listSAMLServiceProvidersPage(t.Context(), tenantA, -1, 0)
	require.NoError(t, err)
	assert.Len(t, all, 3)
	assert.Equal(t, int64(3), total)
}

func TestFederationPagedListingsSurfaceStorageFailures(t *testing.T) {
	t.Run("providers", func(t *testing.T) {
		env := newFederationEnvironment(t)
		require.NoError(t, env.db.Exec("DROP TABLE iam_identity_providers").Error)
		_, _, err := env.service.repo.listProvidersPage(t.Context(), testID("tenant-a"), 0, 2)
		assert.Error(t, err)
	})
	t.Run("saml service providers", func(t *testing.T) {
		env := newFederationEnvironment(t)
		require.NoError(t, env.db.Exec("DROP TABLE iam_saml_service_providers").Error)
		_, _, err := env.service.repo.listSAMLServiceProvidersPage(t.Context(), testID("tenant-a"), 0, 2)
		assert.Error(t, err)
	})
}

// seedSAMLServiceProvider writes a registry row directly: the listing under test
// reads rows without reparsing the metadata, so a real metadata server per row
// would only slow the test down.
func seedSAMLServiceProvider(t *testing.T, env *federationEnvironment, tenantID guid.ID, index int) {
	t.Helper()
	now := federationTestNow
	require.NoError(t, env.db.Table(env.prefix+model.SAMLServiceProvider{}.TableName()).Create(&model.SAMLServiceProvider{
		ID:          testID(fmt.Sprintf("saml-sp-%d", index)),
		TenantID:    tenantID,
		Name:        fmt.Sprintf("Portal %02d", index),
		EntityID:    fmt.Sprintf("https://sp%d.example/metadata", index),
		MetadataXML: "<EntityDescriptor/>",
		Status:      samlSPStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error)
}

func sharedProviderIDs(left, right []Provider) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedFederationIDs(ids, other)
}

func sharedSAMLSPIDs(left, right []SAMLServiceProvider) []guid.ID {
	ids := make([]guid.ID, 0, len(left))
	for _, item := range left {
		ids = append(ids, item.ID)
	}
	other := make([]guid.ID, 0, len(right))
	for _, item := range right {
		other = append(other, item.ID)
	}
	return sharedFederationIDs(ids, other)
}

func sharedFederationIDs(left, right []guid.ID) []guid.ID {
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
