package oauth

import (
	"testing"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedForeignTables is what every owner hands oauth for one deployment
// prefix. The names come from the owners' own constructors rather than from a
// list repeated here, so a table an owner adds or renames reaches these tests
// too.
func publishedForeignTables(prefix string) (
	core.AccessTableNames, identity.PrincipalTableNames,
	authn.CredentialTableNames, organization.DirectoryTableNames,
) {
	return core.PublishedAccessTableNames(prefix), identity.PublishedPrincipalTableNames(prefix),
		authn.PublishedCredentialTableNames(prefix), organization.PublishedDirectoryTableNames(prefix)
}

// bindForeignTables publishes the owners' names into a repository the way
// injection does, for a harness that builds one outside the module.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(publishedForeignTables(prefix)))
}

// TestForeignTablesCarryWhatTheOwnersPublished pins that the names oauth reads
// are the owners' prefixed names, field for field. Asking for a table an owner
// does not have no longer has a runtime failure to test: it does not compile.
func TestForeignTablesCarryWhatTheOwnersPublished(t *testing.T) {
	tables := &foreignTables{}
	require.NoError(t, tables.bind(publishedForeignTables("sl_")))
	require.True(t, tables.bound())

	for want, got := range map[string]string{
		"sl_iam_tenant_members":              tables.access.TenantMembers.String(),
		"sl_iam_principals":                  tables.principal.Principals.String(),
		"sl_iam_service_accounts":            tables.principal.ServiceAccounts.String(),
		"sl_iam_service_account_credentials": tables.principal.ServiceAccountCredentials.String(),
		"sl_iam_credentials":                 tables.credential.Credentials.String(),
		"sl_iam_tenants":                     tables.directory.Tenants.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesAreUnboundUntilEveryOwnerPublishes pins what the startup
// check reads. Three owners publishing is not enough: a module that started on
// part of its names would send an empty table name in the rest of its
// statements.
func TestForeignTablesAreUnboundUntilEveryOwnerPublishes(t *testing.T) {
	var absent *foreignTables
	assert.False(t, absent.bound())
	assert.ErrorIs(t, absent.bind(publishedForeignTables("sl_")), errRepositoryUnavailable)

	assert.False(t, (&foreignTables{}).bound())

	for owner, bind := range map[string]func(*foreignTables) error{
		"core": func(tables *foreignTables) error {
			_, principal, credential, directory := publishedForeignTables("sl_")
			return tables.bind(core.AccessTableNames{}, principal, credential, directory)
		},
		"identity": func(tables *foreignTables) error {
			access, _, credential, directory := publishedForeignTables("sl_")
			return tables.bind(access, identity.PrincipalTableNames{}, credential, directory)
		},
		"authn": func(tables *foreignTables) error {
			access, principal, _, directory := publishedForeignTables("sl_")
			return tables.bind(access, principal, authn.CredentialTableNames{}, directory)
		},
		"organization": func(tables *foreignTables) error {
			access, principal, credential, _ := publishedForeignTables("sl_")
			return tables.bind(access, principal, credential, organization.DirectoryTableNames{})
		},
	} {
		t.Run(owner, func(t *testing.T) {
			tables := &foreignTables{}
			require.NoError(t, bind(tables))
			assert.False(t, tables.bound())
		})
	}
}
