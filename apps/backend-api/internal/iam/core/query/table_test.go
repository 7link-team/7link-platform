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

// publishedDirectoryTables and publishedPrincipalTable are what the owning
// modules publish for one deployment. The query package cannot import them to
// ask for the names -- core is activated before them and the import would be a
// cycle -- so a test states the published contract instead of borrowing the
// owner's own test.
func publishedDirectoryTables(prefix string) DirectoryTables {
	return DirectoryTables{
		Groups:               gormx.NewTableName(prefix, "iam_groups"),
		GroupMembers:         gormx.NewTableName(prefix, "iam_group_members"),
		GroupRoleBindings:    gormx.NewTableName(prefix, "iam_group_role_bindings"),
		Positions:            gormx.NewTableName(prefix, "iam_positions"),
		PositionMembers:      gormx.NewTableName(prefix, "iam_position_members"),
		PositionRoleBindings: gormx.NewTableName(prefix, "iam_position_role_bindings"),
		Departments:          gormx.NewTableName(prefix, "iam_departments"),
		DepartmentClosure:    gormx.NewTableName(prefix, "iam_department_closure"),
		MemberDepartments:    gormx.NewTableName(prefix, "iam_member_departments"),
		RoleDataScopes:       gormx.NewTableName(prefix, "iam_role_data_scopes"),
		RoleScopeDepartments: gormx.NewTableName(prefix, "iam_role_scope_departments"),
		Tenants:              gormx.NewTableName(prefix, "iam_tenants"),
	}
}

func publishedPrincipalTable(prefix string) gormx.TableName {
	return gormx.NewTableName(prefix, "iam_principals")
}

// TestBindAuthorizationTablesCarriesEachOwnersNames keeps one statement's
// table names from resolving against two different deployments: core's own set
// comes from core's prefix, and the joined names come from their owners.
func TestBindAuthorizationTablesCarriesEachOwnersNames(t *testing.T) {
	t.Parallel()

	directory := publishedDirectoryTables("sl_")
	principal := publishedPrincipalTable("sl_")

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
