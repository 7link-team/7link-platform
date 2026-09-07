package core

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestPlatformAdministratorStoreGrantsThroughTheCallersTransaction walks the
// capability the composition boundary holds, and proves the grant is visible to
// the same transaction that will decide whether to commit it.
func TestPlatformAdministratorStoreGrantsThroughTheCallersTransaction(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadmin_")
	const tenantID, principalID guid.ID = 5100, 5101
	seedTenantAndPrincipal(t, harness.db, "bootadmin_", tenantID, principalID)
	store := (&Module{repository: harness.repository}).platformAdministratorStore()

	administrators, err := store.List(t.Context(), harness.db)
	require.NoError(t, err)
	assert.Empty(t, administrators, "a freshly migrated platform has no administrator")

	granted, err := store.GrantFull(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.True(t, granted)

	administrators, err = store.List(t.Context(), harness.db)
	require.NoError(t, err)
	require.Len(t, administrators, 1)
	assert.Equal(t, principalID, administrators[0].PrincipalID)
	assert.True(t, administrators[0].FullAdministrator)
}

// TestPlatformAdministratorGrantIsCreateOnly is what makes a rerun of the
// bootstrap report "already bootstrapped" instead of silently rewriting the
// row an operator may have narrowed on purpose.
func TestPlatformAdministratorGrantIsCreateOnly(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadmintwice_")
	const tenantID, principalID guid.ID = 5200, 5201
	seedTenantAndPrincipal(t, harness.db, "bootadmintwice_", tenantID, principalID)

	granted, err := harness.repository.GrantFullPlatformAdministratorOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	require.True(t, granted)

	granted, err = harness.repository.GrantFullPlatformAdministratorOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.False(t, granted, "a repeat grant reports no write rather than failing or overwriting")

	administrators, err := harness.repository.ListPlatformAdministratorsOn(t.Context(), harness.db)
	require.NoError(t, err)
	assert.Len(t, administrators, 1)
}

// TestPlatformAdministratorGrantRollsBackWithTheCallersTransaction is the
// property the bootstrap depends on: the principal, the credential, and the
// grant must land together or not at all.
func TestPlatformAdministratorGrantRollsBackWithTheCallersTransaction(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadminrollback_")
	const tenantID, principalID guid.ID = 5300, 5301
	seedTenantAndPrincipal(t, harness.db, "bootadminrollback_", tenantID, principalID)

	err := harness.db.Transaction(func(tx *gorm.DB) error {
		granted, err := harness.repository.GrantFullPlatformAdministratorOn(t.Context(), tx, principalID)
		require.NoError(t, err)
		require.True(t, granted)
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)

	administrators, err := harness.repository.ListPlatformAdministratorsOn(t.Context(), harness.db)
	require.NoError(t, err)
	assert.Empty(t, administrators, "an aborted bootstrap must leave no administrator behind")
}

// TestPlatformAdministratorCapabilitiesRejectIncompleteArguments proves the
// transaction-bound entry points refuse to fall back to the module writer.
func TestPlatformAdministratorCapabilitiesRejectIncompleteArguments(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadminargs_")
	var missing *Repository

	_, err := missing.ListPlatformAdministratorsOn(t.Context(), harness.db)
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = harness.repository.ListPlatformAdministratorsOn(t.Context(), nil)
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = missing.GrantFullPlatformAdministratorOn(t.Context(), harness.db, 1)
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = harness.repository.GrantFullPlatformAdministratorOn(t.Context(), nil, 1)
	assert.ErrorIs(t, err, ErrUnavailable)

	_, err = harness.repository.GrantFullPlatformAdministratorOn(t.Context(), harness.db, 0)
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

// TestPlatformAdministratorStoreReportsUnavailableBeforeWiring covers the guard
// inside the closures, which are provided before InjectServices supplies the
// repository they need.
func TestPlatformAdministratorStoreReportsUnavailableBeforeWiring(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadminunwired_")
	for name, unwired := range map[string]*Module{
		"nil module":    nil,
		"no repository": {},
	} {
		t.Run(name, func(t *testing.T) {
			store := unwired.platformAdministratorStore()

			_, err := store.List(t.Context(), harness.db)
			assert.ErrorIs(t, err, ErrUnavailable)

			_, err = store.GrantFull(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, ErrUnavailable)
		})
	}
}

// TestPlatformAdministratorGrantSurfacesStorageFailures keeps a broken table
// from reading as a successful no-op grant.
func TestPlatformAdministratorGrantSurfacesStorageFailures(t *testing.T) {
	harness := newRepositoryHarness(t, "bootadminstorage_")
	require.NoError(t, harness.db.Exec("DROP TABLE bootadminstorage_iam_platform_administrators").Error)

	granted, err := harness.repository.GrantFullPlatformAdministratorOn(t.Context(), harness.db, 5401)
	require.Error(t, err)
	assert.False(t, granted)

	_, err = harness.repository.ListPlatformAdministratorsOn(t.Context(), harness.db)
	assert.Error(t, err)
}
