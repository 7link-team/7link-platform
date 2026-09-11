package query

import (
	"testing"

	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func dryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		DryRun: true,
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	return db
}

func TestBindAppliesPrefixToEveryCoreTable(t *testing.T) {
	t.Parallel()

	db := dryRunDB(t)
	plain := Bind(db, "")
	prefixed := Bind(db, "sl_")
	actual := map[string][2]string{
		"entities":                {plain.Entities.TableName(), prefixed.Entities.TableName()},
		"menus":                   {plain.Menus.TableName(), prefixed.Menus.TableName()},
		"platform_administrators": {plain.PlatformAdministrators.TableName(), prefixed.PlatformAdministrators.TableName()},
		"platform_grants":         {plain.PlatformGrants.TableName(), prefixed.PlatformGrants.TableName()},
		"policy_revisions":        {plain.PolicyRevisions.TableName(), prefixed.PolicyRevisions.TableName()},
		"relationships":           {plain.Relationships.TableName(), prefixed.Relationships.TableName()},
		"resource_relationships":  {plain.ResourceRelationships.TableName(), prefixed.ResourceRelationships.TableName()},
		"roles":                   {plain.Roles.TableName(), prefixed.Roles.TableName()},
		"role_bindings":           {plain.RoleBindings.TableName(), prefixed.RoleBindings.TableName()},
		"role_members":            {plain.RoleMembers.TableName(), prefixed.RoleMembers.TableName()},
		"role_permissions":        {plain.RolePermissions.TableName(), prefixed.RolePermissions.TableName()},
		"temporary_role_grants":   {plain.TemporaryRoleGrants.TableName(), prefixed.TemporaryRoleGrants.TableName()},
		"tenant_members":          {plain.TenantMembers.TableName(), prefixed.TenantMembers.TableName()},
	}

	require.Len(t, actual, 13)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}

// directoryLogicalNames is the contract core requests from the organization
// module: every table an authorization statement JOINs and core does not own.
var directoryLogicalNames = []string{
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

// publishedSet builds the set a table-name owner publishes for one deployment.
// The query package cannot import the owning modules to ask them for it -- core
// is activated before them and the import would be a cycle -- so a test states
// the published contract instead of borrowing the owner's own test.
func publishedSet(prefix string, logical ...string) gormx.TableSet {
	names := make(map[string]gormx.TableName, len(logical))
	for _, name := range logical {
		names[name] = gormx.NewTableName(prefix, name)
	}
	return gormx.NewTableSet(names)
}

// TestBindDirectoryTablesResolvesEveryJoinedTable keeps the resolved names
// exactly the ones the owner published, prefix included. A name core invented
// from its own prefix would send an authorization statement to a table the
// deployment does not have, and the defect would only appear where a prefix is
// configured -- never in the tests that run without one.
func TestBindDirectoryTablesResolvesEveryJoinedTable(t *testing.T) {
	t.Parallel()

	plain, err := BindDirectoryTables(publishedSet("", directoryLogicalNames...))
	require.NoError(t, err)
	prefixed, err := BindDirectoryTables(publishedSet("sl_", directoryLogicalNames...))
	require.NoError(t, err)

	actual := map[string][2]string{
		"iam_groups":                 {plain.Groups.String(), prefixed.Groups.String()},
		"iam_group_members":          {plain.GroupMembers.String(), prefixed.GroupMembers.String()},
		"iam_group_role_bindings":    {plain.GroupRoleBindings.String(), prefixed.GroupRoleBindings.String()},
		"iam_positions":              {plain.Positions.String(), prefixed.Positions.String()},
		"iam_position_members":       {plain.PositionMembers.String(), prefixed.PositionMembers.String()},
		"iam_position_role_bindings": {plain.PositionRoleBindings.String(), prefixed.PositionRoleBindings.String()},
		"iam_departments":            {plain.Departments.String(), prefixed.Departments.String()},
		"iam_department_closure":     {plain.DepartmentClosure.String(), prefixed.DepartmentClosure.String()},
		"iam_member_departments":     {plain.MemberDepartments.String(), prefixed.MemberDepartments.String()},
		"iam_role_data_scopes":       {plain.RoleDataScopes.String(), prefixed.RoleDataScopes.String()},
		"iam_role_scope_departments": {plain.RoleScopeDepartments.String(), prefixed.RoleScopeDepartments.String()},
		"iam_tenants":                {plain.Tenants.String(), prefixed.Tenants.String()},
	}

	require.Len(t, actual, len(directoryLogicalNames))
	for logical, pair := range actual {
		assert.Equal(t, logical, pair[0], logical)
		assert.Equal(t, "sl_"+logical, pair[1], logical)
	}
	assert.Equal(t, "iam_groups", prefixed.Groups.Logical())
	assert.False(t, prefixed.Groups.IsZero())
}

// TestBindDirectoryTablesRejectsANameTheOwnerDoesNotPublish keeps a missing
// name an error rather than a zero TableName: an unbound name would reach a
// statement as an empty table and read whatever the database makes of that.
func TestBindDirectoryTablesRejectsANameTheOwnerDoesNotPublish(t *testing.T) {
	t.Parallel()

	incomplete := directoryLogicalNames[:len(directoryLogicalNames)-1]
	tables, err := BindDirectoryTables(publishedSet("sl_", incomplete...))
	require.Error(t, err)
	assert.Contains(t, err.Error(), directoryLogicalNames[len(directoryLogicalNames)-1])
	assert.Equal(t, DirectoryTables{}, tables)
}

// TestBindPrincipalTableResolvesThePublishedName keeps the one name core reads
// from the identity module coming from that module's set.
func TestBindPrincipalTableResolvesThePublishedName(t *testing.T) {
	t.Parallel()

	principal, err := BindPrincipalTable(publishedSet("sl_", "iam_principals"))
	require.NoError(t, err)
	assert.Equal(t, "sl_iam_principals", principal.String())
	assert.Equal(t, "iam_principals", principal.Logical())
	assert.False(t, principal.IsZero())

	missing, err := BindPrincipalTable(publishedSet("sl_", "iam_service_accounts"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iam_principals")
	assert.True(t, missing.IsZero())
	assert.Equal(t, gormx.TableName{}, missing)
}

// TestBindAuthorizationTablesCarriesEachOwnersNames keeps one statement's
// table names from resolving against two different deployments: core's own set
// comes from core's prefix, and the joined names come from their owners.
func TestBindAuthorizationTablesCarriesEachOwnersNames(t *testing.T) {
	t.Parallel()

	directory, err := BindDirectoryTables(publishedSet("sl_", directoryLogicalNames...))
	require.NoError(t, err)
	principal, err := BindPrincipalTable(publishedSet("sl_", "iam_principals"))
	require.NoError(t, err)

	bound := BindAuthorizationTables("sl_", directory, principal)
	assert.Equal(t, BindNames("sl_"), bound.Tables)
	assert.Equal(t, directory, bound.Directory)
	assert.Equal(t, principal, bound.Principal)
}

// TestBindNamesMapPublishesPrefixedNames keeps the published set and the bound
// queries from drifting apart: a table reachable through Bind but missing from
// the map would be invisible to every consumer, and a name in the map that
// Bind does not bind would point at a table this module does not own.
func TestBindNamesMapPublishesPrefixedNames(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	published := BindNames(prefix).Map()
	require.NotEmpty(t, published, "a module that binds tables publishes them")
	for logical, name := range published {
		assert.Equal(t, prefix+logical, name.String(), logical)
		assert.Equal(t, logical, name.Logical(), logical)
		assert.False(t, name.IsZero(), logical)
	}
}
