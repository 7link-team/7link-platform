package provisioning

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testForeignTables publishes the owners' names the way injection does. The
// names come from the owners' own constructors rather than from a list
// repeated here, so a table an owner adds or renames reaches these tests too.
func testForeignTables(t *testing.T, prefix string) *foreignTables {
	t.Helper()
	tables := &foreignTables{}
	require.NoError(t, tables.bind(
		core.PublishedAccessTableNames(prefix), identity.PublishedPrincipalTableNames(prefix),
		organization.PublishedDirectoryTableNames(prefix),
	))
	return tables
}

// bindForeignTables publishes the owners' names into a repository the way
// injection does, for a harness that builds one outside the module.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(
		core.PublishedAccessTableNames(prefix), identity.PublishedPrincipalTableNames(prefix),
		organization.PublishedDirectoryTableNames(prefix),
	))
}

// TestForeignTablesCarryWhatTheOwnersPublished pins that the names
// provisioning reads are the owners' prefixed names, field for field. Asking
// for a table an owner does not have no longer has a runtime failure to test:
// it does not compile.
func TestForeignTablesCarryWhatTheOwnersPublished(t *testing.T) {
	tables := testForeignTables(t, "sl_")
	require.True(t, tables.bound())

	for want, got := range map[string]string{
		"sl_iam_tenant_members": tables.access.TenantMembers.String(),
		"sl_iam_principals":     tables.principal.Principals.String(),
		"sl_iam_tenants":        tables.directory.Tenants.String(),
		"sl_iam_groups":         tables.directory.Groups.String(),
		"sl_iam_group_members":  tables.directory.GroupMembers.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesAreUnboundUntilEveryOwnerPublishes pins what the startup
// check reads. One owner publishing is not enough: a module that started on
// part of its names would send an empty table name in the rest of its
// statements.
func TestForeignTablesAreUnboundUntilEveryOwnerPublishes(t *testing.T) {
	var absent *foreignTables
	assert.False(t, absent.bound())
	assert.ErrorIs(t, absent.bind(
		core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"),
		organization.PublishedDirectoryTableNames("sl_"),
	), errRepositoryUnavailable)

	assert.False(t, (&foreignTables{}).bound())

	for name, missing := range map[string]struct {
		access    core.AccessTableNames
		principal identity.PrincipalTableNames
		directory organization.DirectoryTableNames
	}{
		"core": {core.AccessTableNames{}, identity.PublishedPrincipalTableNames("sl_"),
			organization.PublishedDirectoryTableNames("sl_")},
		"identity": {core.PublishedAccessTableNames("sl_"), identity.PrincipalTableNames{},
			organization.PublishedDirectoryTableNames("sl_")},
		"organization": {core.PublishedAccessTableNames("sl_"), identity.PublishedPrincipalTableNames("sl_"),
			organization.DirectoryTableNames{}},
	} {
		tables := &foreignTables{}
		require.NoError(t, tables.bind(missing.access, missing.principal, missing.directory))
		assert.False(t, tables.bound(), name)
	}
}

// TestServiceBindsPublishedTablesOutsideModuleWiring covers the entry point a
// caller that builds a service itself has to use, because injection is what
// binds the names everywhere else.
func TestServiceBindsPublishedTablesOutsideModuleWiring(t *testing.T) {
	env := newProvisioningEnvironment(t)
	require.NoError(t, env.service.BindForeignTables(
		core.PublishedAccessTableNames(env.prefix), identity.PublishedPrincipalTableNames(env.prefix),
		organization.PublishedDirectoryTableNames(env.prefix),
	))
	assert.True(t, env.service.repo.foreign.bound())
	assert.Equal(t, env.prefix+"iam_tenants", env.service.repo.foreign.directory.Tenants.String())

	var absent *Service
	assert.ErrorIs(t, absent.BindForeignTables(
		core.PublishedAccessTableNames(""), identity.PublishedPrincipalTableNames(""),
		organization.PublishedDirectoryTableNames(""),
	), errRepositoryUnavailable)
}
