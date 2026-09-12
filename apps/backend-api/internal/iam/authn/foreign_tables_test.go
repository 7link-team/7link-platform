package authn

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// publishedOAuthTables is the set the oauth module publishes. authn cannot
// import oauth -- oauth imports authn -- so a harness that runs authn alone has
// to publish what the owner would, the way the connectors do in a running
// deployment.
var publishedOAuthTables = []string{
	"iam_oauth_clients",
	"iam_oauth_codes",
	"iam_oauth_consents",
	"iam_oauth_consent_requests",
	"iam_refresh_tokens",
}

// bindForeignTables publishes both owners' sets into a repository the way the
// module's connectors do, for a harness that builds one outside the module. A
// reader is unbound until this runs, and an unbound reader reports the published
// tables as missing instead of querying an empty table name.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	foreign := &foreignTables{}
	require.NoError(t, foreign.bindDirectory(publishedTableSet(prefix, publishedDirectoryTables...)))
	require.NoError(t, foreign.bindOAuth(publishedTableSet(prefix, publishedOAuthTables...)))
	// core is the one owner authn can import: it is activated first, so the
	// set comes from core itself rather than from a list repeated here.
	require.NoError(t, foreign.bindAccess(core.TableSet(prefix)))
	repository.foreign = foreign
}

// bindModuleForeignTables does the same for a module built by hand instead of
// by the registry, whose connectors therefore never ran.
func bindModuleForeignTables(t *testing.T, module *Module, prefix string) {
	t.Helper()
	require.NotNil(t, module)
	require.NoError(t, module.foreign.bindDirectory(publishedTableSet(prefix, publishedDirectoryTables...)))
	require.NoError(t, module.foreign.bindOAuth(publishedTableSet(prefix, publishedOAuthTables...)))
	require.NoError(t, module.foreign.bindAccess(core.TableSet(prefix)))
}

// publishedTableSet builds the set an owner publishes: every logical name it
// owns, bound to the deployment prefix.
func publishedTableSet(prefix string, logical ...string) gormx.TableSet {
	names := make(map[string]gormx.TableName, len(logical))
	for _, name := range logical {
		names[name] = gormx.NewTableName(prefix, name)
	}
	return gormx.NewTableSet(names)
}

// TestForeignTablesCarryTheOwnersPrefix keeps the resolved names exactly the
// ones the owners published. A name authn invented from its own prefix would
// send a statement to a table the deployment does not have, and the defect
// would appear only where a prefix is configured.
func TestForeignTablesCarryTheOwnersPrefix(t *testing.T) {
	t.Parallel()

	foreign := &foreignTables{}
	require.NoError(t, foreign.bindDirectory(publishedTableSet("sl_", publishedDirectoryTables...)))
	require.NoError(t, foreign.bindOAuth(publishedTableSet("sl_", publishedOAuthTables...)))

	tenant, err := foreign.directoryTable("iam_tenants")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", tenant)

	clients, err := foreign.oauthTable("iam_oauth_clients")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_oauth_clients", clients)

	refresh, err := foreign.oauthTable("iam_refresh_tokens")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_refresh_tokens", refresh)

	require.NoError(t, foreign.bindAccess(core.TableSet("sl_")))
	members, err := foreign.accessTable("iam_tenant_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenant_members", members)
}

// TestForeignTablesReportUnboundAsUnavailable is the guarantee the connectors
// exist for: a table-name set that never arrived is an explicit error, never an
// empty table name in a statement. Every state a holder can be in is checked,
// including a holder that is not there at all.
func TestForeignTablesReportUnboundAsUnavailable(t *testing.T) {
	t.Parallel()

	unbound := &foreignTables{}
	_, err := unbound.directoryTable("iam_tenants")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = unbound.oauthTable("iam_refresh_tokens")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = unbound.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	// An owner that published a set without the name this module reads is the
	// same failure: the name must not fall back to something authn assembled.
	partial := &foreignTables{}
	require.NoError(t, partial.bindOAuth(publishedTableSet("sl_", "iam_oauth_clients")))
	_, err = partial.oauthTable("iam_refresh_tokens")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	require.NoError(t, partial.bindAccess(publishedTableSet("sl_", "iam_roles")))
	_, err = partial.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	var absent *foreignTables
	assert.ErrorIs(t, absent.bindDirectory(publishedTableSet("sl_", publishedDirectoryTables...)), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindOAuth(publishedTableSet("sl_", publishedOAuthTables...)), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindAccess(core.TableSet("sl_")), errRepositoryUnavailable)
	_, err = absent.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = absent.directoryTable("iam_tenants")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = absent.oauthTable("iam_refresh_tokens")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

// TestRepositoryReportsForeignTablesAsUnbound proves the repository a caller
// builds without module wiring fails closed on both accessors rather than
// resolving a name from its own prefix.
func TestRepositoryReportsForeignTablesAsUnbound(t *testing.T) {
	t.Parallel()

	harness := newAuthnRepositoryHarness(t, "sl_")
	// A repository the module's connectors never reached: NewRepository gives
	// it an empty holder, so both accessors report the tables as missing.
	unbound := NewRepository(harness.runtime, newRepositoryTestIDs(1000), harness.repository.audit)
	_, err := unbound.tenantTable()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = unbound.oauthTable("iam_refresh_tokens")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (*Repository)(nil).tenantTable()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (*Repository)(nil).oauthTable("iam_refresh_tokens")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
}

// TestForeignTablesReachTheStatementsTheyWerePublishedFor proves the readers
// use the published names rather than dropping them: the tenant read and the
// refresh-token revocation both run against the prefixed tables.
func TestForeignTablesReachTheStatementsTheyWerePublishedFor(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	ctx := t.Context()
	principalID := guid.ID(1)

	_, err := harness.repository.oauthClientState(ctx, principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	revoked, err := harness.repository.revokeRefreshTokens(ctx, principalID, repositoryTestNow)
	require.NoError(t, err)
	assert.Zero(t, revoked)
}

// TestServiceAccountStateStopsWhenTheMemberTableWasNeverPublished covers the
// one table authn reads from core. The set core publishes is complete here
// except for that name, so the statement cannot be reached by resolving the
// rest -- which is what an owner publishing an incomplete set looks like.
func TestServiceAccountStateStopsWhenTheMemberTableWasNeverPublished(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	bindForeignTables(t, harness.repository, "sl_")

	published := core.TableSet("sl_")
	names := make(map[string]gormx.TableName, published.Len())
	for _, logical := range published.Names() {
		if logical == "iam_tenant_members" {
			continue
		}
		name, _ := published.Lookup(logical)
		names[logical] = name
	}
	require.NoError(t, harness.repository.foreign.bindAccess(gormx.NewTableSet(names)))

	_, err := harness.repository.serviceAccountState(t.Context(), 1)
	require.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")
}
