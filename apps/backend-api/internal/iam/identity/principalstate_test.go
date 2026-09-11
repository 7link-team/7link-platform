package identity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestPrincipalStateStoreReadsTheOwnersTableUnderTheDeploymentPrefix is the
// point of the contract: a consumer resolves one capability instead of naming
// the principal table, and the prefix still applies exactly once.
func TestPrincipalStateStoreReadsTheOwnersTableUnderTheDeploymentPrefix(t *testing.T) {
	harness := newIdentityHarness(t, "principalstate_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject", Email: "subject@example.test",
	}, identityTestNow)
	require.NoError(t, err)

	store := (&Module{runtime: harness.runtime}).principalStateStore()

	status, found, err := store.Status(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "active", status)

	_, found, err = store.Status(t.Context(), harness.db, id+1)
	require.NoError(t, err, "an id nobody holds is an absence, not a failure")
	assert.False(t, found)
}

// TestPrincipalStateStoreStatusReportsAnUnreadableTableAsAFailure keeps a
// missing table from reading as a missing principal, which a caller rejects for
// an entirely different reason.
func TestPrincipalStateStoreStatusReportsAnUnreadableTableAsAFailure(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatebad_")
	store := principalStateStore(harness.prefix)

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.prefix+"iam_principals").Error)
	_, _, err := store.Status(t.Context(), harness.db, 1)
	assert.ErrorContains(t, err, "load principal status")
}

// principalStateRow is the two columns the locks must leave alone.
type principalStateRow struct {
	Status    string
	UpdatedAt string
}

// TestPrincipalStateStoreLocksWithoutChangingState covers both lock shapes. The
// lock is a write-back of the current value, so it must leave the row's status
// and timestamp exactly as they were while still taking the row.
func TestPrincipalStateStoreLocksWithoutChangingState(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatelock_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject",
	}, identityTestNow)
	require.NoError(t, err)
	store := principalStateStore(harness.prefix)

	read := func() principalStateRow {
		var row principalStateRow
		require.NoError(t, harness.db.Table(harness.prefix+"iam_principals").
			Select("status, updated_at").Where("id = ?", id).Take(&row).Error)
		return row
	}
	before := read()

	require.NoError(t, store.Lock(t.Context(), harness.db, id))
	require.NoError(t, store.LockActive(t.Context(), harness.db, id))
	assert.Equal(t, before, read(), "a lock must take the row without rewriting it")

	// A principal that is not active is left unlocked, and the read that follows
	// is what rejects it -- so neither lock reports the absence as an error.
	require.NoError(t, harness.db.Exec(
		"UPDATE "+harness.prefix+"iam_principals SET status = 'disabled' WHERE id = ?", id,
	).Error)
	require.NoError(t, store.LockActive(t.Context(), harness.db, id))
	require.NoError(t, store.Lock(t.Context(), harness.db, id+1))

	status, found, err := store.Status(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "disabled", status)
}

// TestPrincipalStateStoreRunsInTheCallersTransaction proves the lock and the
// read that follows share the snapshot the caller opened, which is what makes a
// suspension unable to land between the two.
func TestPrincipalStateStoreRunsInTheCallersTransaction(t *testing.T) {
	harness := newIdentityHarness(t, "principalstatetx_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "subject", DisplayName: "Subject",
	}, identityTestNow)
	require.NoError(t, err)
	store := principalStateStore(harness.prefix)

	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"UPDATE "+harness.prefix+"iam_principals SET status = 'disabled' WHERE id = ?", id,
		).Error; err != nil {
			return err
		}
		status, found, err := store.Status(t.Context(), tx, id)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "disabled", status)
		return nil
	}))
}

// TestPrincipalStateStoreRefusesToServeBeforeWiring covers a module that was
// never given a runtime: it must refuse rather than fall back to an unprefixed
// table that belongs to nobody.
func TestPrincipalStateStoreRefusesToServeBeforeWiring(t *testing.T) {
	harness := newIdentityHarness(t, "pstateunwired_")
	for name, unwired := range map[string]*Module{
		"nil module": nil,
		"no runtime": {},
	} {
		t.Run(name, func(t *testing.T) {
			store := unwired.principalStateStore()
			assert.ErrorIs(t, store.Lock(t.Context(), harness.db, 1), ErrUnavailable)
			assert.ErrorIs(t, store.LockActive(t.Context(), harness.db, 1), ErrUnavailable)
			status, found, err := store.Status(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, ErrUnavailable)
			assert.False(t, found)
			assert.Empty(t, status)
		})
	}
}
