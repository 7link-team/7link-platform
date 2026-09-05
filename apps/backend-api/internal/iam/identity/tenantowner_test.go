package identity

import (
	"gorm.io/gorm"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindPrincipalOnReadsTheOwnerThroughTheCallersTransaction is what tenant
// creation depends on: the owner is resolved inside the transaction that
// inserts the tenant, so a principal suspended between request and commit
// cannot still end up administering it.
func TestFindPrincipalOnReadsTheOwnerThroughTheCallersTransaction(t *testing.T) {
	harness := newIdentityHarness(t, "tenantowner_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "owner", DisplayName: "Tenant Owner", Email: "owner@example.test",
	}, identityTestNow)
	require.NoError(t, err)

	owner, found, err := harness.repository.FindPrincipalOn(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, id, owner.ID)
	assert.Equal(t, "Tenant Owner", owner.DisplayName)
	assert.Equal(t, "owner@example.test", owner.Email)
	assert.Equal(t, "active", owner.Status)

	_, found, err = harness.repository.FindPrincipalOn(t.Context(), harness.db, id+1)
	require.NoError(t, err, "an owner nobody created is an absence, not a failure")
	assert.False(t, found)
}

// TestFindPrincipalOnSeesADisableMadeInTheSameTransaction proves the lookup
// reads the caller's uncommitted state rather than a separate connection.
func TestFindPrincipalOnSeesADisableMadeInTheSameTransaction(t *testing.T) {
	harness := newIdentityHarness(t, "tenantownertx_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "owner", DisplayName: "Tenant Owner",
	}, identityTestNow)
	require.NoError(t, err)

	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"UPDATE "+harness.prefix+"iam_principals SET status = 'disabled' WHERE id = ?", id,
		).Error; err != nil {
			return err
		}
		owner, found, err := harness.repository.FindPrincipalOn(t.Context(), tx, id)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "disabled", owner.Status)
		return nil
	}))
}

// TestFindPrincipalOnRefusesInputItCannotResolve keeps a missing argument from
// reading as a missing principal, which the caller would treat very
// differently.
func TestFindPrincipalOnRefusesInputItCannotResolve(t *testing.T) {
	harness := newIdentityHarness(t, "tenantownerbad_")

	_, _, err := harness.repository.FindPrincipalOn(t.Context(), harness.db, 0)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = harness.repository.FindPrincipalOn(t.Context(), nil, 1)
	assert.ErrorIs(t, err, ErrInvalid)

	var absent *Repository
	_, _, err = absent.FindPrincipalOn(t.Context(), harness.db, 1)
	assert.ErrorIs(t, err, ErrInvalid)

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.prefix+"iam_principals").Error)
	_, _, err = harness.repository.FindPrincipalOn(t.Context(), harness.db, 1)
	assert.Error(t, err, "an unreadable table must not be reported as a missing owner")
}

// TestTenantOwnerLookupReportsUnavailableBeforeWiring covers the guard inside
// the closure, which is provided before InjectServices supplies the
// collaborators it builds its repository from.
func TestTenantOwnerLookupReportsUnavailableBeforeWiring(t *testing.T) {
	harness := newIdentityHarness(t, "tenantownerunwired_")
	for name, unwired := range map[string]*Module{
		"nil module":  nil,
		"no runtime":  {ids: newIdentityTestIDs(8000), appender: harness.repository.appender},
		"no ids":      {runtime: harness.runtime, appender: harness.repository.appender},
		"no appender": {runtime: harness.runtime, ids: newIdentityTestIDs(8000)},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := unwired.tenantOwnerLookup()(t.Context(), harness.db, 1)
			assert.ErrorIs(t, err, ErrUnavailable)
		})
	}
}

// TestTenantOwnerLookupDelegatesToTheWiredRepository proves the capability the
// organization module resolves is the same read path exercised above.
func TestTenantOwnerLookupDelegatesToTheWiredRepository(t *testing.T) {
	harness := newIdentityHarness(t, "tenantownerwired_")
	id, err := harness.repository.CreateBootstrapPrincipalOn(t.Context(), harness.db, BootstrapPrincipalInput{
		LoginName: "owner", DisplayName: "Tenant Owner",
	}, identityTestNow)
	require.NoError(t, err)

	lookup := (&Module{
		runtime:  harness.runtime,
		ids:      newIdentityTestIDs(8100),
		appender: harness.repository.appender,
	}).tenantOwnerLookup()

	owner, found, err := lookup(t.Context(), harness.db, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Tenant Owner", owner.DisplayName)
}
