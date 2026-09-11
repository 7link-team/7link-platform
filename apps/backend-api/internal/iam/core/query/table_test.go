package query

import (
	"testing"

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

// TestBindForeignAppliesPrefixToEveryJoinedTable keeps the prefix reaching a
// single point in this package even for the tables the module only joins. A
// name that lost its prefix would send an authorization statement to a table
// the deployment does not have, and the defect would only appear where a prefix
// is configured -- never in the tests that run without one.
func TestBindForeignAppliesPrefixToEveryJoinedTable(t *testing.T) {
	t.Parallel()
	const prefix = "sl_"
	plain, prefixed := BindAuthorizationTables(""), BindAuthorizationTables(prefix)

	actual := map[string][2]string{
		"principals":             {plain.Foreign.Principals.String(), prefixed.Foreign.Principals.String()},
		"groups":                 {plain.Foreign.Groups.String(), prefixed.Foreign.Groups.String()},
		"group_members":          {plain.Foreign.GroupMembers.String(), prefixed.Foreign.GroupMembers.String()},
		"group_role_bindings":    {plain.Foreign.GroupRoleBindings.String(), prefixed.Foreign.GroupRoleBindings.String()},
		"positions":              {plain.Foreign.Positions.String(), prefixed.Foreign.Positions.String()},
		"position_members":       {plain.Foreign.PositionMembers.String(), prefixed.Foreign.PositionMembers.String()},
		"position_role_bindings": {plain.Foreign.PositionRoleBindings.String(), prefixed.Foreign.PositionRoleBindings.String()},
		"departments":            {plain.Foreign.Departments.String(), prefixed.Foreign.Departments.String()},
		"department_closure":     {plain.Foreign.DepartmentClosure.String(), prefixed.Foreign.DepartmentClosure.String()},
		"member_departments":     {plain.Foreign.MemberDepartments.String(), prefixed.Foreign.MemberDepartments.String()},
		"role_data_scopes":       {plain.Foreign.RoleDataScopes.String(), prefixed.Foreign.RoleDataScopes.String()},
		"role_scope_departments": {plain.Foreign.RoleScopeDepartments.String(), prefixed.Foreign.RoleScopeDepartments.String()},
	}

	require.Len(t, actual, 12)
	for name, pair := range actual {
		assert.Equal(t, "iam_"+name, pair[0], name)
		assert.Equal(t, prefix+"iam_"+name, pair[1], name)
	}
}

// TestBindAuthorizationTablesSharesOnePrefix keeps the owned and the joined
// names from resolving a statement's JOINs against two different schemas.
func TestBindAuthorizationTablesSharesOnePrefix(t *testing.T) {
	t.Parallel()
	bound := BindAuthorizationTables("sl_")
	assert.Equal(t, BindNames("sl_"), bound.Tables)
	assert.Equal(t, BindForeign("sl_"), bound.Foreign)
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
