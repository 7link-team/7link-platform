package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryOrganizationTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{DryRun: true, Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"departments":            {plain.Departments.TableName(), prefixed.Departments.TableName()},
		"department_closures":    {plain.DepartmentClosures.TableName(), prefixed.DepartmentClosures.TableName()},
		"positions":              {plain.Positions.TableName(), prefixed.Positions.TableName()},
		"position_members":       {plain.PositionMembers.TableName(), prefixed.PositionMembers.TableName()},
		"groups":                 {plain.Groups.TableName(), prefixed.Groups.TableName()},
		"group_members":          {plain.GroupMembers.TableName(), prefixed.GroupMembers.TableName()},
		"group_role_bindings":    {plain.GroupRoleBindings.TableName(), prefixed.GroupRoleBindings.TableName()},
		"position_role_bindings": {plain.PositionRoleBindings.TableName(), prefixed.PositionRoleBindings.TableName()},
		"tenants":                {plain.Tenants.TableName(), prefixed.Tenants.TableName()},
		"member_departments":     {plain.MemberDepartments.TableName(), prefixed.MemberDepartments.TableName()},
		"role_data_scopes":       {plain.RoleDataScopes.TableName(), prefixed.RoleDataScopes.TableName()},
		"role_scope_departments": {plain.RoleScopeDepartments.TableName(), prefixed.RoleScopeDepartments.TableName()},
		"invitations":            {plain.Invitations.TableName(), prefixed.Invitations.TableName()},
		"invitation_roles":       {plain.InvitationRoles.TableName(), prefixed.InvitationRoles.TableName()},
	}
	require.Len(t, actual, 14)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
