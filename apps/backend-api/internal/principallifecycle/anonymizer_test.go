package principallifecycle_test

import (
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/principallifecycle"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The owners erasure composes, with the stream and version each declares.
var erasureOwners = []struct {
	name     string
	source   goosex.Source
	required int64
}{
	{core.Name, core.MigrationSource(), core.PrincipalErasureVersion},
	{authn.Name, authn.MigrationSource(), authn.PrincipalErasureVersion},
	{oauth.Name, oauth.MigrationSource(), oauth.PrincipalErasureVersion},
	{federation.Name, federation.MigrationSource(), federation.PrincipalErasureVersion},
	{provisioning.Name, provisioning.MigrationSource(), provisioning.PrincipalErasureVersion},
	{organization.Name, organization.MigrationSource(), organization.PrincipalErasureVersion},
	{governance.Name, governance.MigrationSource(), governance.PrincipalErasureVersion},
}

func openErasureDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

// migrateOwnerTo installs identity, whose tables the owners' guard triggers
// read, and brings one owner's stream to exactly version. It goes through
// goosex rather than the milestone planner on purpose: the planner cannot hold
// one owner below a version its neighbours' later milestones depend on, and
// what is under test is only whether erasure's tables exist at that version.
// Every other owner stays uninstalled, which erasure must skip.
func migrateOwnerTo(t *testing.T, db *gorm.DB, source goosex.Source, version int64) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, (&identity.Module{}).MigrationSource(), "sqlite"))
	require.NoError(t, goosex.MigrateUpTo(t.Context(), sqlDB, source, "sqlite", version))
}

func erasureRegistry(t *testing.T) *authz.Registry {
	t.Helper()
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	return registry
}

func TestAnonymizePrincipalDataRequiresTransactionAndRegistry(t *testing.T) {
	now := time.Now().UTC()
	principal := identity.Principal{ID: 1}
	assert.ErrorIs(t, principallifecycle.AnonymizePrincipalData(t.Context(), nil, "", erasureRegistry(t), principal, now), identity.ErrUnavailable)
	assert.ErrorIs(t, principallifecycle.AnonymizePrincipalData(t.Context(), openErasureDB(t), "", nil, principal, now), identity.ErrUnavailable)
}

// Every owner is held to the version it declares: one behind refuses, exactly
// that version is enough. An owner whose erasure starts touching a later table
// fails the "exact" case here until it raises its PrincipalErasureVersion.
func TestAnonymizePrincipalDataHoldsEachOwnerToItsErasureVersion(t *testing.T) {
	for _, owner := range erasureOwners {
		t.Run(owner.name+"/behind", func(t *testing.T) {
			db := openErasureDB(t)
			migrateOwnerTo(t, db, owner.source, owner.required-1)
			err := principallifecycle.AnonymizePrincipalData(t.Context(), db, "", erasureRegistry(t), identity.Principal{ID: 1}, time.Now().UTC())
			require.ErrorIs(t, err, identity.ErrUnavailable)
			assert.ErrorContains(t, err, fmt.Sprintf("%s principal erasure needs migration %d", owner.name, owner.required))
		})
		t.Run(owner.name+"/exact", func(t *testing.T) {
			db := openErasureDB(t)
			migrateOwnerTo(t, db, owner.source, owner.required)
			require.NoError(t, principallifecycle.AnonymizePrincipalData(t.Context(), db, "", erasureRegistry(t), identity.Principal{ID: 1}, time.Now().UTC()))
		})
	}
}

func TestAnonymizePrincipalDataSkipsOwnersThatWereNeverInstalled(t *testing.T) {
	db := openErasureDB(t)
	require.NoError(t, principallifecycle.AnonymizePrincipalData(t.Context(), db, "", erasureRegistry(t), identity.Principal{ID: 1}, time.Now().UTC()))
}

func TestAnonymizePrincipalDataReportsSchemaReadFailures(t *testing.T) {
	db := openErasureDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	err = principallifecycle.AnonymizePrincipalData(t.Context(), db, "", erasureRegistry(t), identity.Principal{ID: 1}, time.Now().UTC())
	require.Error(t, err)
	assert.NotErrorIs(t, err, identity.ErrUnavailable, "a failed read must not pass for a missing migration")
	assert.ErrorContains(t, err, "migration version")
}
