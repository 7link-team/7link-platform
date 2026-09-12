package query

import (
	"reflect"
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

// TestBindNamesCarryThePrefixExactlyOnce reads every name BindNames produces,
// field by field, at a prefix and without one. A name that lost the prefix
// would read the wrong table in a prefixed deployment and the right one in
// every test that runs without a prefix -- so the two have to be compared.
func TestBindNamesCarryThePrefixExactlyOnce(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	plain, prefixed := reflect.ValueOf(BindNames("")), reflect.ValueOf(BindNames(prefix))
	names := prefixed.Type()
	require.Positive(t, names.NumField(), "a module that binds tables names at least one")

	for index := range names.NumField() {
		field := names.Field(index).Name
		name := prefixed.Field(index).Interface().(gormx.TableName)
		logical := plain.Field(index).Interface().(gormx.TableName).String()
		assert.Equal(t, prefix+logical, name.String(), field)
		assert.Equal(t, logical, name.Logical(), field)
		assert.False(t, name.IsZero(), field)
	}
}
