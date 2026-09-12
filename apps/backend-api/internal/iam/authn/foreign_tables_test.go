package authn

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// publishedDirectoryTables is what the organization module fills into the shape
// authn declared. authn cannot import organization -- organization imports
// authn, so the import is a cycle -- and a harness that runs authn alone has to
// publish what the owner would, the way the connectors do in a running
// deployment.
func publishedDirectoryTables(prefix string) DirectoryTableNames {
	return DirectoryTableNames{Tenants: gormx.NewTableName(prefix, "iam_tenants")}
}

// publishedOAuthTables is the same for the oauth module, which authn cannot
// import for the same reason.
func publishedOAuthTables(prefix string) OAuthTableNames {
	return OAuthTableNames{
		Clients:       gormx.NewTableName(prefix, "iam_oauth_clients"),
		RefreshTokens: gormx.NewTableName(prefix, "iam_refresh_tokens"),
	}
}

// bindForeignTables publishes every owner's names into a repository the way the
// module's wiring does, for a harness that builds one outside the module. A
// repository is unbound until this runs, and a module whose owners never
// published does not start.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	// core and identity are the two owners authn imports, so their names come
	// from the owners' own constructors rather than from a list repeated here.
	require.NoError(t, repository.BindForeignTables(
		publishedDirectoryTables(prefix), publishedOAuthTables(prefix),
		core.PublishedAccessTableNames(prefix), identity.PublishedPrincipalTableNames(prefix),
	))
	// The reads and locks on identity's own table go through identity's
	// capability, which the module receives the same way. A harness that built
	// the repository itself has to supply it, and it comes from identity rather
	// than from a stand-in so these tests exercise the owner's statements.
	repository.principals = identityPrincipalStateFor(t, prefix)
}

// bindModuleForeignTables does the same for a module built by hand instead of
// by the registry, whose connectors therefore never ran.
func bindModuleForeignTables(t *testing.T, module *Module, prefix string) {
	t.Helper()
	require.NotNil(t, module)
	require.NoError(t, module.foreign.bind(
		publishedDirectoryTables(prefix), publishedOAuthTables(prefix),
		core.PublishedAccessTableNames(prefix), identity.PublishedPrincipalTableNames(prefix),
	))
	require.NoError(t, module.bindPrincipalState(identityPrincipalStateFor(t, prefix)))
}

// TestForeignTablesCarryTheOwnersPrefix keeps the resolved names exactly the
// ones the owners published. A name authn invented from its own prefix would
// send a statement to a table the deployment does not have, and the defect
// would appear only where a prefix is configured. Asking for a table an owner
// does not have no longer has a runtime failure to pin: it does not compile.
func TestForeignTablesCarryTheOwnersPrefix(t *testing.T) {
	t.Parallel()

	foreign := &foreignTables{}
	require.NoError(t, foreign.bind(
		publishedDirectoryTables("sl_"), publishedOAuthTables("sl_"),
		core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"),
	))
	require.True(t, foreign.bound())

	for want, got := range map[string]string{
		"sl_iam_tenants":          foreign.directory.Tenants.String(),
		"sl_iam_oauth_clients":    foreign.oauth.Clients.String(),
		"sl_iam_refresh_tokens":   foreign.oauth.RefreshTokens.String(),
		"sl_iam_tenant_members":   foreign.access.TenantMembers.String(),
		"sl_iam_principals":       foreign.principal.Principals.String(),
		"sl_iam_service_accounts": foreign.principal.ServiceAccounts.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesRejectAnIncompletePublication covers the two guards every
// binder carries. A holder that is not there at all has nowhere to record the
// names, and a field authn declared and the owner left empty is refused here
// rather than carried into a statement as an empty table name -- which is what
// the per-name lookups used to report one statement at a time.
func TestForeignTablesRejectAnIncompletePublication(t *testing.T) {
	t.Parallel()

	var absent *foreignTables
	assert.ErrorIs(t, absent.bindDirectory(publishedDirectoryTables("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindOAuth(publishedOAuthTables("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindAccess(core.PublishedAccessTableNames("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bindPrincipal(identity.PublishedPrincipalTableNames("sl_")), errRepositoryUnavailable)
	assert.ErrorIs(t, absent.bind(
		publishedDirectoryTables("sl_"), publishedOAuthTables("sl_"),
		core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"),
	), errRepositoryUnavailable)

	err := (&foreignTables{}).bindDirectory(DirectoryTableNames{})
	require.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "organization left Tenants empty")

	for name, half := range map[string]OAuthTableNames{
		"no clients":        {RefreshTokens: gormx.NewTableName("sl_", "iam_refresh_tokens")},
		"no refresh tokens": {Clients: gormx.NewTableName("sl_", "iam_oauth_clients")},
	} {
		t.Run(name, func(t *testing.T) {
			err := (&foreignTables{}).bindOAuth(half)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "oauth left Clients or RefreshTokens empty")
		})
	}

	// bind stops at the owner that published nothing usable, so the holder is
	// left unbound rather than half filled from the owners that came after.
	for name, refused := range map[string]func(*foreignTables) error{
		"organization published nothing": func(tables *foreignTables) error {
			return tables.bind(DirectoryTableNames{}, publishedOAuthTables("sl_"),
				core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"))
		},
		"oauth published nothing": func(tables *foreignTables) error {
			return tables.bind(publishedDirectoryTables("sl_"), OAuthTableNames{},
				core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			tables := &foreignTables{}
			assert.ErrorIs(t, refused(tables), errRepositoryUnavailable)
			assert.False(t, tables.bound())
		})
	}
}

// TestForeignTablesAreUnboundUntilEveryOwnerPublishes pins what the startup
// check reads. One owner short is still unbound: a module that started on part
// of its names would send an empty table name in the statements that need the
// rest.
func TestForeignTablesAreUnboundUntilEveryOwnerPublishes(t *testing.T) {
	t.Parallel()

	var absent *foreignTables
	assert.False(t, absent.bound(), "a holder that is not there has nothing bound")
	assert.False(t, (&foreignTables{}).bound(), "the zero value must report that nothing arrived")

	complete := foreignTables{
		directory: publishedDirectoryTables("sl_"),
		oauth:     publishedOAuthTables("sl_"),
		access:    core.PublishedAccessTableNames("sl_"),
		principal: identity.PublishedPrincipalTableNames("sl_"),
	}
	require.True(t, complete.bound())

	for owner, withhold := range map[string]func(*foreignTables){
		"organization":  func(tables *foreignTables) { tables.directory = DirectoryTableNames{} },
		"oauth":         func(tables *foreignTables) { tables.oauth = OAuthTableNames{} },
		"oauth clients": func(tables *foreignTables) { tables.oauth.Clients = gormx.TableName{} },
		"oauth refresh": func(tables *foreignTables) { tables.oauth.RefreshTokens = gormx.TableName{} },
		"core":          func(tables *foreignTables) { tables.access = core.AccessTableNames{} },
		"identity":      func(tables *foreignTables) { tables.principal = identity.PrincipalTableNames{} },
	} {
		t.Run(owner, func(t *testing.T) {
			short := complete
			withhold(&short)
			assert.False(t, short.bound())
		})
	}
}

// TestRepositoryReportsForeignTablesAsUnbound proves a repository built without
// module wiring stays unbound until it publishes the owners' names itself, and
// covers the two states the exported binder can be reached in: a repository
// that is not there, and one built outside its constructor with no holder to
// record into.
func TestRepositoryReportsForeignTablesAsUnbound(t *testing.T) {
	t.Parallel()

	published := func(repository *Repository) error {
		return repository.BindForeignTables(
			publishedDirectoryTables("sl_"), publishedOAuthTables("sl_"),
			core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"),
		)
	}
	assert.ErrorIs(t, published(nil), errRepositoryUnavailable)

	// A repository the module's connectors never reached: NewRepository gives
	// it an empty holder, and the module refuses to start until every owner has
	// filled it.
	runtime := appenv.NewRuntime(appenv.NewIdentity("authn-test", "1"), appenv.WithTablePrefix("sl_"))
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	unwired := NewRepository(runtime, newRepositoryTestIDs(1), appender)
	assert.False(t, unwired.foreign.bound())
	require.NoError(t, published(unwired))
	assert.True(t, unwired.foreign.bound())

	// A repository assembled by hand has no holder at all, and the binder
	// creates one instead of dereferencing nil.
	withoutHolder := &Repository{}
	assert.False(t, withoutHolder.foreign.bound())
	require.NoError(t, published(withoutHolder))
	assert.True(t, withoutHolder.foreign.bound())
}

// TestForeignTablesReachTheStatementsTheyWerePublishedFor proves the readers
// use the published names rather than dropping them. The fixture database has
// every prefixed table, so a name that was not the owner's would come back as a
// SQL error rather than as the statement's own answer.
func TestForeignTablesReachTheStatementsTheyWerePublishedFor(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")
	ctx := t.Context()
	principalID := guid.ID(1)

	_, err := harness.repository.oauthClientState(ctx, principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	revoked, err := harness.repository.revokeRefreshTokens(ctx, principalID, repositoryTestNow)
	require.NoError(t, err)
	assert.Zero(t, revoked)

	// The three statements that still name identity's, core's and
	// organization's tables each JOIN a table authn owns, so they cannot be
	// handed to the owner and have to carry the published names themselves.
	_, err = harness.repository.principalState(ctx, principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, _, err = harness.repository.activePrincipalClaims(ctx, principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = harness.repository.serviceAccountState(ctx, principalID)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

// TestReadsOnIdentityTablesRefuseAnUnwiredCapability covers the other side of
// the same guarantee. These statements no longer name identity's tables at all:
// identity owns them and runs them, so what authn has to refuse is a capability
// that never arrived -- the repository must report it rather than call a nil
// function or read nothing and call it an absence.
func TestReadsOnIdentityTablesRefuseAnUnwiredCapability(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "sl_")

	for name, call := range map[string]func(*Repository) error{
		"login principal": func(r *Repository) error {
			_, err := r.findLoginPrincipal(t.Context(), "alice")
			return err
		},
		"recovery principal": func(r *Repository) error {
			_, err := r.findRecoveryPrincipal(t.Context(), "a@b.test")
			return err
		},
		"active principal": func(r *Repository) error {
			_, err := r.getActivePrincipal(t.Context(), 1)
			return err
		},
		"principal lock": func(r *Repository) error {
			_, err := r.lockPrincipal(t.Context(), 1)
			return err
		},
		"principal lock by login": func(r *Repository) error {
			_, err := r.lockPrincipalByLogin(t.Context(), "alice")
			return err
		},
		"email verification write": func(r *Repository) error {
			_, err := r.verifyPrincipalEmail(t.Context(), 1, "a@b.test", repositoryTestNow)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			repository := harness.repository.withDB(harness.db, false)
			repository.principals = identity.PrincipalStateStore{}
			assert.ErrorIs(t, call(repository), errRepositoryUnavailable)
		})
	}
}

// identityPrincipalStateFor resolves identity's principal-state capability from
// identity itself, bound to one deployment prefix. A stand-in here would make
// every assertion about the statements authn sends meaningless.
func identityPrincipalStateFor(t *testing.T, prefix string) identity.PrincipalStateStore {
	t.Helper()
	runtime := appenv.NewRuntime(appenv.NewIdentity("authn-test", "1"), appenv.WithTablePrefix(prefix))
	store, err := identityPrincipalState(t, identity.NewModule(runtime, identity.Config{}))
	require.NoError(t, err)
	return store
}
