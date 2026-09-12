package federation

import (
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindForeignTables publishes the owners' names into a repository the way
// injection does, for a harness that builds one outside the module. The names
// come from the owners' own constructors rather than from a list repeated
// here, so a table an owner adds or renames reaches this harness too.
func bindForeignTables(t *testing.T, repository *Repository, prefix string) {
	t.Helper()
	require.NotNil(t, repository)
	require.NoError(t, repository.foreign.bind(
		core.PublishedAccessTableNames(prefix), organization.PublishedDirectoryTableNames(prefix),
	))
}

// TestForeignTablesCarryWhatTheOwnersPublished pins that the names federation
// reads are the owners' prefixed names, field for field. Asking for a table an
// owner does not have no longer has a runtime failure to test: it does not
// compile.
func TestForeignTablesCarryWhatTheOwnersPublished(t *testing.T) {
	tables := &foreignTables{}
	require.NoError(t, tables.bind(
		core.PublishedAccessTableNames("sl_"), organization.PublishedDirectoryTableNames("sl_"),
	))
	require.True(t, tables.bound())

	for want, got := range map[string]string{
		"sl_iam_tenant_members":   tables.access.TenantMembers.String(),
		"sl_iam_roles":            tables.access.Roles.String(),
		"sl_iam_role_members":     tables.access.RoleMembers.String(),
		"sl_iam_policy_revisions": tables.access.PolicyRevisions.String(),
		"sl_iam_tenants":          tables.directory.Tenants.String(),
	} {
		assert.Equal(t, want, got)
	}
}

// TestForeignTablesAreUnboundUntilEveryOwnerPublishes pins what the startup
// check reads. One owner publishing is not enough: a module that started on
// half its names would send an empty table name in the other half's statements.
func TestForeignTablesAreUnboundUntilEveryOwnerPublishes(t *testing.T) {
	var absent *foreignTables
	assert.False(t, absent.bound())
	assert.ErrorIs(t, absent.bind(
		core.PublishedAccessTableNames("sl_"), organization.PublishedDirectoryTableNames("sl_"),
	), errRepositoryUnavailable)

	assert.False(t, (&foreignTables{}).bound())

	accessOnly := &foreignTables{}
	require.NoError(t, accessOnly.bind(
		core.PublishedAccessTableNames("sl_"), organization.DirectoryTableNames{},
	))
	assert.False(t, accessOnly.bound())

	directoryOnly := &foreignTables{}
	require.NoError(t, directoryOnly.bind(
		core.AccessTableNames{}, organization.PublishedDirectoryTableNames("sl_"),
	))
	assert.False(t, directoryOnly.bound())
}
