package identity

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedDirectoryTables is the set the organization module publishes.
// identity cannot import organization -- organization imports identity, so the
// import is a cycle -- and a harness that runs identity alone has to publish
// what the owner would, the way the connectors do in a running deployment.
var publishedDirectoryTables = []string{
	"iam_groups",
	"iam_group_members",
	"iam_group_role_bindings",
	"iam_positions",
	"iam_position_members",
	"iam_position_role_bindings",
	"iam_departments",
	"iam_department_closure",
	"iam_member_departments",
	"iam_role_data_scopes",
	"iam_role_scope_departments",
	"iam_tenants",
}

// publishedOAuthTables is the set the oauth module publishes. identity reads
// the refresh tokens it revokes, and cannot name them from its own prefix.
var publishedOAuthTables = []string{
	"iam_oauth_clients",
	"iam_oauth_codes",
	"iam_oauth_consents",
	"iam_oauth_consent_requests",
	"iam_refresh_tokens",
}

// bindForeignTables publishes both owners' sets into a repository the way the
// module's connectors do, for a harness that builds one outside the module. A
// reader is unbound until this runs, and an unbound reader reports the
// published tables as missing instead of querying an empty table name.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	foreign := &foreignTables{}
	require.NoError(t, foreign.bindDirectory(publishedTableSet(prefix, publishedDirectoryTables...)))
	require.NoError(t, foreign.bindOAuth(publishedTableSet(prefix, publishedOAuthTables...)))
	// core is the one owner identity can import: it is activated first, so the
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
// ones the owners published. A name identity invented from its own prefix would
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

	refresh, err := foreign.oauthTable("iam_refresh_tokens")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_refresh_tokens", refresh)
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

	// An owner that published a set without the name this module reads is the
	// same failure: the name must not fall back to something identity assembled.
	partial := &foreignTables{}
	require.NoError(t, partial.bindDirectory(publishedTableSet("sl_", "iam_groups")))
	_, err = partial.directoryTable("iam_tenants")
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	var absent *foreignTables
	assert.ErrorIs(t, absent.bindDirectory(publishedTableSet("sl_", publishedDirectoryTables...)), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindOAuth(publishedTableSet("sl_", publishedOAuthTables...)), errRepositoryUnavailable)
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

	harness := newIdentityHarness(t, "sl_")
	// A repository the module's connectors never reached: NewRepository gives it
	// an empty holder, so both accessors report the tables as missing.
	unbound := NewRepository(harness.runtime, newIdentityTestIDs(1000), harness.repository.appender)
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

// TestRevokeReportsUnavailabilityWithoutTouchingStorage is the write-path
// guarantee: a table name the owner never published stops the revocation
// instead of running a statement against a table nobody named. The sessions
// belong to identity, so that half still lands; only the oauth-owned half is
// withheld, and the caller gets an error rather than a silent partial result.
func TestRevokeReportsUnavailabilityWithoutTouchingStorage(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	ctx := t.Context()
	harness.seedRefreshToken(t, "live-refresh", 900)
	harness.seedSession(t, 901, 900)
	// The repository the module wires is bound; dropping the holder reproduces
	// the deployment where the owner's connector never ran.
	harness.repository.foreign = &foreignTables{}

	err := harness.repository.revokeSessionsAndRefreshTokens(ctx, 900, identityTestNow)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", 900),
		"a rejected revocation must not touch a row")
	assert.Zero(t, harness.nullTimeCount(t, "iam_sessions", "revoked_at", 900),
		"the half identity owns still runs; only the unpublished name is withheld")
}

// TestStatementsRejectAnUnpublishedMemberTable walks every statement that
// reads or writes the tenant member table core owns, with core's set published
// complete except for that one name. Each must report the gap rather than
// query an empty table name, and the writes among them must leave storage
// untouched.
func TestStatementsRejectAnUnpublishedMemberTable(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	repository := harness.repository

	published := core.TableSet("sl_")
	names := make(map[string]gormx.TableName, published.Len())
	for _, logical := range published.Names() {
		if logical == "iam_tenant_members" {
			continue
		}
		name, _ := published.Lookup(logical)
		names[logical] = name
	}
	require.NoError(t, repository.foreign.bindAccess(gormx.NewTableSet(names)))

	var before int64
	require.NoError(t, harness.db.Table("sl_iam_tenant_members").Count(&before).Error)

	for name, call := range map[string]func() error{
		"insert member": func() error {
			return repository.insertTenantMember(t.Context(), tenantMemberRecord{
				TenantID: 1, PrincipalID: 2, Status: "active",
			})
		},
		"get principal": func() error {
			_, err := repository.getPrincipal(t.Context(), 1, 2)
			return err
		},
		"list principals": func() error {
			_, _, err := repository.listPrincipals(t.Context(), 1, "", 10, 0)
			return err
		},
		"update member": func() error {
			return repository.updateTenantMember(t.Context(), 1, 2, map[string]any{"status": "inactive"})
		},
		"active tenants": func() error {
			_, err := repository.activeTenantIDs(t.Context(), 2)
			return err
		},
		"get tenant member": func() error {
			_, err := repository.getTenantMember(t.Context(), 1, 2)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")
		})
	}

	var after int64
	require.NoError(t, harness.db.Table("sl_iam_tenant_members").Count(&after).Error)
	assert.Equal(t, before, after, "a rejected write must not reach storage")
}
