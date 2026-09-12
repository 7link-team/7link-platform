package federation

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// bindForeignTables publishes the owners' sets into a repository the way
// injection does, for a harness that builds one outside the module. The sets
// come from the owners themselves rather than from a list repeated here, so a
// table an owner stops publishing fails this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(core.TableSet(prefix), organization.TableSet(prefix)))
}

func TestForeignTablesResolveWhatTheOwnersPublished(t *testing.T) {
	tables := &foreignTables{}
	require.NoError(t, tables.bind(core.TableSet("sl_"), organization.TableSet("sl_")))

	for logical, want := range map[string]string{
		"iam_tenant_members":   "sl_iam_tenant_members",
		"iam_roles":            "sl_iam_roles",
		"iam_role_members":     "sl_iam_role_members",
		"iam_policy_revisions": "sl_iam_policy_revisions",
	} {
		name, err := tables.accessTable(logical)
		require.NoError(t, err, logical)
		assert.Equal(t, want, name)
	}
	tenants, err := tables.directoryTable("iam_tenants")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", tenants)
}

func TestForeignTablesReportUnboundAndIncompletePublications(t *testing.T) {
	unbound := &foreignTables{}
	for name, lookup := range map[string]func(string) (string, error){
		"core":         unbound.accessTable,
		"organization": unbound.directoryTable,
	} {
		_, err := lookup("iam_tenant_members")
		assert.ErrorIs(t, err, errRepositoryUnavailable, name)
		assert.Contains(t, err.Error(), name+" has not published its table names")
	}

	// A set that arrived without the name this module reads is an error too:
	// a published-but-incomplete set must not degrade into an empty name.
	partial := &foreignTables{}
	require.NoError(t, partial.bind(
		gormx.NewTableSet(map[string]gormx.TableName{"iam_roles": gormx.NewTableName("sl_", "iam_roles")}),
		organization.TableSet("sl_"),
	))
	_, err := partial.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bind(core.TableSet("sl_"), organization.TableSet("sl_")), errRepositoryUnavailable)
	for _, lookup := range []func(string) (string, error){absent.accessTable, absent.directoryTable} {
		_, err := lookup("iam_tenant_members")
		assert.ErrorIs(t, err, errRepositoryUnavailable)
	}
}

// partialSet publishes every logical name an owner owns except one, so a
// lookup for the missing table fails while the rest of the statement's names
// still resolve.
func partialSet(full gormx.TableSet, omit string) gormx.TableSet {
	names := make(map[string]gormx.TableName, full.Len())
	for _, logical := range full.Names() {
		if logical == omit {
			continue
		}
		name, _ := full.Lookup(logical)
		names[logical] = name
	}
	return gormx.NewTableSet(names)
}

// TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing walks every
// repository statement that reads a table federation does not own and removes
// exactly the one name it needs. Each must report the gap instead of querying
// an empty table name, and the guarded writes must leave storage untouched.
func TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing(t *testing.T) {
	tenantID, roleID, principalID := testID("tenant-a"), testID("role-a"), testID("principal")

	for _, testCase := range []struct {
		name    string
		omitted string
		bind    func(*foreignTables, string) error
		call    func(*Repository) error
	}{
		{"default role lookup", "iam_roles", func(tables *foreignTables, prefix string) error {
			return tables.bind(partialSet(core.TableSet(prefix), "iam_roles"), organization.TableSet(prefix))
		}, func(repo *Repository) error {
			_, err := repo.roleExists(t.Context(), tenantID, roleID)
			return err
		}},
		{"default role grant", "iam_role_members", func(tables *foreignTables, prefix string) error {
			return tables.bind(partialSet(core.TableSet(prefix), "iam_role_members"), organization.TableSet(prefix))
		}, func(repo *Repository) error {
			_, err := repo.grantDefaultRole(t.Context(), tenantID, roleID, principalID, federationTestNow)
			return err
		}},
		{"policy revision", "iam_policy_revisions", func(tables *foreignTables, prefix string) error {
			return tables.bind(partialSet(core.TableSet(prefix), "iam_policy_revisions"), organization.TableSet(prefix))
		}, func(repo *Repository) error {
			_, err := repo.grantDefaultRole(t.Context(), tenantID, roleID, principalID, federationTestNow)
			return err
		}},
		{"member status", "iam_tenant_members", func(tables *foreignTables, prefix string) error {
			return tables.bind(partialSet(core.TableSet(prefix), "iam_tenant_members"), organization.TableSet(prefix))
		}, func(repo *Repository) error {
			return repo.verifyPrincipalAndMember(t.Context(), tenantID, principalID)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newFederationEnvironment(t, "sl_")
			require.NoError(t, env.db.Table(env.prefix+"iam_roles").Create(&federationRole{
				TenantID: tenantID, ID: roleID, Name: "Member", NameKey: "member",
				Status: "active", CreatedAt: federationTestNow, UpdatedAt: federationTestNow,
			}).Error)
			require.NoError(t, env.db.Table(env.prefix+"iam_principals").Create(&federationPrincipal{
				ID: principalID, LoginName: "principal", Email: "principal@example.test",
				Status: "active", CreatedAt: federationTestNow, UpdatedAt: federationTestNow,
			}).Error)

			repo := env.service.repo
			tables := &foreignTables{}
			require.NoError(t, testCase.bind(tables, env.prefix))
			repo.foreign = tables

			err := testCase.call(repo)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+testCase.omitted)

			// The guarded writes must not have reached storage.
			for _, table := range []string{"iam_role_members", "iam_policy_revisions"} {
				var count int64
				require.NoError(t, env.db.Table(env.prefix+table).Count(&count).Error)
				assert.Zero(t, count, table)
			}
		})
	}
}

// TestIdentityLinkStatementsRejectUnpublishedForeignTables covers the browser
// facing statements, which join core's membership table and organization's
// tenant table in the same query: each owner is removed on its own so neither
// gap can hide behind the other.
func TestIdentityLinkStatementsRejectUnpublishedForeignTables(t *testing.T) {
	for _, owner := range []struct {
		name    string
		omitted string
		bind    func(*foreignTables, string) error
	}{
		{"core", "iam_tenant_members", func(tables *foreignTables, prefix string) error {
			return tables.bind(partialSet(core.TableSet(prefix), "iam_tenant_members"), organization.TableSet(prefix))
		}},
		{"organization", "iam_tenants", func(tables *foreignTables, prefix string) error {
			return tables.bind(core.TableSet(prefix), partialSet(organization.TableSet(prefix), "iam_tenants"))
		}},
	} {
		t.Run("linkable providers "+owner.name, func(t *testing.T) {
			env, _, cookie := newIdentityLinkForeignTableEnvironment(t)
			tables := &foreignTables{}
			require.NoError(t, owner.bind(tables, env.prefix))
			env.service.repo.foreign = tables

			_, err := env.service.ListLinkableProviders(t.Context(), cookie)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+owner.omitted)
		})

		t.Run("unlink alternatives "+owner.name, func(t *testing.T) {
			env, provider, cookie := newIdentityLinkForeignTableEnvironment(t)
			tables := &foreignTables{}
			require.NoError(t, owner.bind(tables, env.prefix))
			env.service.repo.foreign = tables

			err := env.service.UnlinkPersonalIdentity(t.Context(), cookie,
				identityLinkID(provider.ID, "external-subject"), "proof")
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+owner.omitted)

			// The link the caller asked to remove is still there.
			var count int64
			require.NoError(t, env.db.Table(env.prefix+model.IdentityLink{}.TableName()).Count(&count).Error)
			assert.Equal(t, int64(1), count)
		})
	}
}

// TestIdentityOperationRejectsUnpublishedTenantTable covers the continuation
// path, which reads the tenant's status through organization's published name
// before it will let a linking or proof transaction proceed.
func TestIdentityOperationRejectsUnpublishedTenantTable(t *testing.T) {
	env, provider, _ := newIdentityLinkForeignTableEnvironment(t)
	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)

	state := "account.continuation"
	require.NoError(t, env.db.Table(env.prefix+model.IdentityLinkTransaction{}.TableName()).Create(&model.IdentityLinkTransaction{
		IDHash: identityOperationHash(state), PrincipalID: testID("principal"), ProviderID: provider.ID,
		TenantID: row.TenantID, SessionHash: "session", CredentialVersion: 1, Purpose: "link",
		ProviderFingerprint: providerFingerprint(row), ChallengeHash: "challenge",
		CodeVerifierCiphertext: "cipher", CallbackURL: "https://iam.example/cb", ReturnURL: "https://app.example/",
		ExternalSubject: "external-subject", Email: "user@example.com", DisplayName: "User",
		CreatedAt: federationTestNow, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}).Error)

	tables := &foreignTables{}
	require.NoError(t, tables.bind(
		core.TableSet(env.prefix), partialSet(organization.TableSet(env.prefix), "iam_tenants"),
	))
	env.service.repo.foreign = tables

	_, _, err = env.service.identityOperation(t.Context(), env.service.repo, provider.ID, state)
	require.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "organization did not publish iam_tenants")
}

// newIdentityLinkForeignTableEnvironment builds the smallest deployment the
// browser-facing identity link statements need: one provider, one link owned
// by the session's principal, and the reauthentication hooks stubbed so the
// flow reaches the statement whose table names are under test.
func newIdentityLinkForeignTableEnvironment(t *testing.T) (*federationEnvironment, Provider, string) {
	t.Helper()
	env := newFederationEnvironment(t, "sl_")
	principalID := testID("principal")
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("Primary", "https://issuer.example/"))
	require.NoError(t, env.db.Table(env.prefix+model.IdentityLink{}.TableName()).Create(&model.IdentityLink{
		ProviderID: provider.ID, TenantID: provider.TenantID, ExternalSubject: "external-subject",
		PrincipalID: principalID, Email: "user@example.com", DisplayName: "User",
		LastLoginAt: federationTestNow, CreatedAt: federationTestNow, UpdatedAt: federationTestNow,
	}).Error)
	env.service.accountProofs = authn.ReauthenticationConsumer(
		func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil },
	)
	env.service.accountReauthentication.LocalMethodsOn = func(context.Context, *gorm.DB, guid.ID) (int, error) {
		return 1, nil
	}
	token, err := env.web.CreateSession(t.Context(), principalID, federationTestNow,
		authnext.Assurance{AuthTime: federationTestNow, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	return env, provider, env.web.SessionCookie(token)
}
