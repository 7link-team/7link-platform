package oauth

import (
	"testing"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	authnext "7link.com/platform/pkg/extension/authn"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindForeignTables publishes the owners' sets into a repository the way
// injection does, for a harness that builds one outside the module. The sets
// come from the owners themselves rather than from a list repeated here, so a
// table an owner stops publishing fails this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(
		core.TableSet(prefix), identity.TableSet(prefix),
		authn.TableSet(prefix), organization.TableSet(prefix),
	))
}

func TestForeignTablesResolveWhatTheOwnersPublished(t *testing.T) {
	tables := &foreignTables{}
	require.NoError(t, tables.bind(
		core.TableSet("sl_"), identity.TableSet("sl_"),
		authn.TableSet("sl_"), organization.TableSet("sl_"),
	))

	members, err := tables.accessTable("iam_tenant_members")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenant_members", members)
	principals, err := tables.principalTable("iam_principals")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_principals", principals)
	credentials, err := tables.credentialTable("iam_credentials")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_credentials", credentials)
	tenants, err := tables.directoryTable("iam_tenants")
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_tenants", tenants)
}

func TestForeignTablesReportUnboundAndIncompletePublications(t *testing.T) {
	unbound := &foreignTables{}
	for name, lookup := range map[string]func(string) (string, error){
		"core":         unbound.accessTable,
		"identity":     unbound.principalTable,
		"authn":        unbound.credentialTable,
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
		identity.TableSet("sl_"), authn.TableSet("sl_"), organization.TableSet("sl_"),
	))
	_, err := partial.accessTable("iam_tenant_members")
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Contains(t, err.Error(), "core did not publish iam_tenant_members")

	var absent *foreignTables
	assert.ErrorIs(t, absent.bind(
		core.TableSet("sl_"), identity.TableSet("sl_"),
		authn.TableSet("sl_"), organization.TableSet("sl_"),
	), errRepositoryUnavailable)
	for _, lookup := range []func(string) (string, error){
		absent.accessTable, absent.principalTable, absent.credentialTable, absent.directoryTable,
	} {
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
// statement that reads a table oauth does not own and removes exactly the one
// name it needs. Each must report the gap instead of querying an empty table
// name, and the guarded write must leave storage untouched.
func TestRepositoryRejectsStatementsWhoseForeignTablesAreMissing(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	prefix := harness.prefix
	full := func() (gormx.TableSet, gormx.TableSet, gormx.TableSet, gormx.TableSet) {
		return core.TableSet(prefix), identity.TableSet(prefix), authn.TableSet(prefix), organization.TableSet(prefix)
	}

	for _, testCase := range []struct {
		name    string
		omitted string
		bind    func(*foreignTables) error
		call    func(*Repository) error
	}{
		{"member status", "iam_tenant_members", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(partialSet(access, "iam_tenant_members"), principal, credential, directory)
		}, func(repo *Repository) error {
			_, err := repo.activeMember(t.Context(), 1, 2)
			return err
		}},
		{"service account credential", "iam_service_account_credentials", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(access, partialSet(principal, "iam_service_account_credentials"), credential, directory)
		}, func(repo *Repository) error {
			_, err := repo.serviceAccountClient(t.Context(), 1)
			return err
		}},
		{"service account", "iam_service_accounts", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(access, partialSet(principal, "iam_service_accounts"), credential, directory)
		}, func(repo *Repository) error {
			_, err := repo.serviceAccountClient(t.Context(), 1)
			return err
		}},
		{"principal", "iam_principals", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(access, partialSet(principal, "iam_principals"), credential, directory)
		}, func(repo *Repository) error {
			_, err := repo.serviceAccountClient(t.Context(), 1)
			return err
		}},
		{"service account member", "iam_tenant_members", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(partialSet(access, "iam_tenant_members"), principal, credential, directory)
		}, func(repo *Repository) error {
			_, err := repo.serviceAccountClient(t.Context(), 1)
			return err
		}},
		{"service account tenant", "iam_tenants", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(access, principal, credential, partialSet(directory, "iam_tenants"))
		}, func(repo *Repository) error {
			_, err := repo.serviceAccountClient(t.Context(), 1)
			return err
		}},
		{"credential use", "iam_service_account_credentials", func(tables *foreignTables) error {
			access, principal, credential, directory := full()
			return tables.bind(access, partialSet(principal, "iam_service_account_credentials"), credential, directory)
		}, func(repo *Repository) error {
			return repo.touchServiceAccountCredential(t.Context(), 1, oauthTestNow)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := harness.repo.withDB(harness.db, false)
			tables := &foreignTables{}
			require.NoError(t, testCase.bind(tables))
			repo.foreign = tables

			err := testCase.call(repo)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+testCase.omitted)
		})
	}
}

// TestGrantAdmissionRejectsUnpublishedMemberAndCredentialTables covers the two
// owners the grant admission lock reads through, one at a time, so neither
// gap can hide behind the other.
func TestGrantAdmissionRejectsUnpublishedMemberAndCredentialTables(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	prefix := harness.prefix
	claims := &authnext.Claims{PrincipalID: 101, TenantID: 7}

	for _, testCase := range []struct {
		name    string
		omitted string
		bind    func(*foreignTables) error
	}{
		{"members", "iam_tenant_members", func(tables *foreignTables) error {
			return tables.bind(
				partialSet(core.TableSet(prefix), "iam_tenant_members"), identity.TableSet(prefix),
				authn.TableSet(prefix), organization.TableSet(prefix),
			)
		}},
		{"credentials", "iam_credentials", func(tables *foreignTables) error {
			return tables.bind(
				core.TableSet(prefix), identity.TableSet(prefix),
				partialSet(authn.TableSet(prefix), "iam_credentials"), organization.TableSet(prefix),
			)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := harness.repo.withDB(harness.db, true)
			tables := &foreignTables{}
			require.NoError(t, testCase.bind(tables))
			repo.foreign = tables

			err := repo.lockGrantAdmission(t.Context(), claims, 7)
			require.ErrorIs(t, err, errRepositoryUnavailable)
			assert.Contains(t, err.Error(), "did not publish "+testCase.omitted)
		})
	}
}
