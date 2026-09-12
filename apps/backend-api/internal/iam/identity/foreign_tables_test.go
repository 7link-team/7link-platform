package identity

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedDirectoryTables is what the organization module fills into the
// connector identity provides. identity cannot import organization --
// organization imports identity, so the import is a cycle -- and a harness that
// runs identity alone has to publish what the owner would, the way the
// connectors do in a running deployment.
func publishedDirectoryTables(prefix string) DirectoryTableNames {
	return DirectoryTableNames{Tenants: gormx.NewTableName(prefix, "iam_tenants")}
}

// publishedOAuthTables is what the oauth module fills in, for the same reason:
// identity reads the refresh tokens it revokes and cannot name them from its
// own prefix.
func publishedOAuthTables(prefix string) OAuthTableNames {
	return OAuthTableNames{RefreshTokens: gormx.NewTableName(prefix, "iam_refresh_tokens")}
}

// bindForeignTables publishes every owner's names into a repository the way the
// module's connectors do, for a harness that builds one outside the module.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	foreign := &foreignTables{}
	require.NoError(t, foreign.bindDirectory(publishedDirectoryTables(prefix)))
	require.NoError(t, foreign.bindOAuth(publishedOAuthTables(prefix)))
	// core is the one owner identity can import: it is activated first, so the
	// names come from core's own constructor rather than from a list repeated
	// here, and a table core renames reaches this harness too.
	require.NoError(t, foreign.bindAccess(core.PublishedAccessTableNames(prefix)))
	repository.foreign = foreign
}

// bindModuleForeignTables does the same for a module built by hand instead of
// by the registry, whose connectors therefore never ran.
func bindModuleForeignTables(t *testing.T, module *Module, prefix string) {
	t.Helper()
	require.NotNil(t, module)
	require.NoError(t, module.foreign.bindDirectory(publishedDirectoryTables(prefix)))
	require.NoError(t, module.foreign.bindOAuth(publishedOAuthTables(prefix)))
	require.NoError(t, module.foreign.bindAccess(core.PublishedAccessTableNames(prefix)))
}

// TestForeignTablesCarryTheOwnersPrefix keeps the resolved names exactly the
// ones the owners published. A name identity invented from its own prefix would
// send a statement to a table the deployment does not have, and the defect
// would appear only where a prefix is configured. Asking for a table an owner
// does not have no longer has a runtime failure to test: it does not compile.
func TestForeignTablesCarryTheOwnersPrefix(t *testing.T) {
	t.Parallel()

	foreign := &foreignTables{}
	require.NoError(t, foreign.bindAccess(core.PublishedAccessTableNames("sl_")))
	require.NoError(t, foreign.bindDirectory(publishedDirectoryTables("sl_")))
	require.NoError(t, foreign.bindOAuth(publishedOAuthTables("sl_")))
	require.True(t, foreign.bound())

	for want, got := range map[string]string{
		"sl_iam_tenant_members": foreign.access.TenantMembers.String(),
		"sl_iam_tenants":        foreign.directory.Tenants.String(),
		"sl_iam_refresh_tokens": foreign.oauth.RefreshTokens.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesRejectAPublicationThatCarriesNoName is the guarantee the
// connectors exist for: an owner that called its connector without filling in
// the name identity asked for is an explicit error, never an empty table name
// in a statement. A holder that is not there at all is the same failure.
func TestForeignTablesRejectAPublicationThatCarriesNoName(t *testing.T) {
	t.Parallel()

	empty := &foreignTables{}
	assert.ErrorIs(t, empty.bindDirectory(DirectoryTableNames{}), errRepositoryUnavailable)
	assert.ErrorIs(t, empty.bindOAuth(OAuthTableNames{}), errRepositoryUnavailable)
	assert.False(t, empty.bound(), "a rejected publication must not leave the holder bound")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bindAccess(core.PublishedAccessTableNames("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindDirectory(publishedDirectoryTables("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindOAuth(publishedOAuthTables("sl_")), errRepositoryUnavailable)
}

// TestForeignTablesAreUnboundUntilEveryOwnerPublishes pins what the startup
// check reads. Two owners publishing is not enough: a module that started on
// part of its names would send an empty table name in the rest of its
// statements.
//
// core is an ordinary provider, so a core that published nothing still arrives
// -- as the zero struct. organization and oauth publish through the connectors
// identity provides, and their binders refuse a name that never arrived, so an
// owner that is not in the deployment is one that never called its connector.
func TestForeignTablesAreUnboundUntilEveryOwnerPublishes(t *testing.T) {
	t.Parallel()

	var absent *foreignTables
	assert.False(t, absent.bound(), "a holder that is not there has nothing bound")
	assert.False(t, (&foreignTables{}).bound(), "no owner has published yet")

	for owner, publish := range map[string]func(*testing.T, *foreignTables){
		"core": func(t *testing.T, foreign *foreignTables) {
			require.NoError(t, foreign.bindAccess(core.AccessTableNames{}))
			require.NoError(t, foreign.bindDirectory(publishedDirectoryTables("sl_")))
			require.NoError(t, foreign.bindOAuth(publishedOAuthTables("sl_")))
		},
		"organization": func(t *testing.T, foreign *foreignTables) {
			require.NoError(t, foreign.bindAccess(core.PublishedAccessTableNames("sl_")))
			require.NoError(t, foreign.bindOAuth(publishedOAuthTables("sl_")))
		},
		"oauth": func(t *testing.T, foreign *foreignTables) {
			require.NoError(t, foreign.bindAccess(core.PublishedAccessTableNames("sl_")))
			require.NoError(t, foreign.bindDirectory(publishedDirectoryTables("sl_")))
		},
	} {
		t.Run("without "+owner, func(t *testing.T) {
			t.Parallel()

			foreign := &foreignTables{}
			publish(t, foreign)
			assert.False(t, foreign.bound())
		})
	}
}

// TestForeignTablesReachTheStatementsTheyWerePublishedFor proves the readers
// use the published names rather than dropping them: the tenant read and the
// refresh-token revocation both run against the prefixed tables.
func TestForeignTablesReachTheStatementsTheyWerePublishedFor(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	harness.seedRefreshToken(t, "live-refresh", 900)

	active, err := harness.repository.tenantActive(ctx, 700)
	require.NoError(t, err, "a bound reader reaches the tenant table")
	assert.False(t, active)

	require.NoError(t, harness.repository.revokeSessionsAndRefreshTokens(ctx, 900, identityTestNow))
	assert.Zero(t, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", 900),
		"the revocation must reach the name oauth published")
}
