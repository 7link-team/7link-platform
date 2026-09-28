package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface{ TableName() string }

func TestOrganizationModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{
		Department{}, DepartmentClosure{}, Position{}, PositionMember{}, Group{}, GroupMember{},
		GroupRoleBinding{}, PositionRoleBinding{}, Tenant{}, MemberDepartment{}, RoleDataScope{},
		RoleScopeDepartment{}, Invitation{}, InvitationRole{},
	}
	expected := []string{
		"iam_departments", "iam_department_closure", "iam_positions", "iam_position_members",
		"iam_groups", "iam_group_members", "iam_group_role_bindings", "iam_position_role_bindings",
		"iam_tenants", "iam_member_departments", "iam_role_data_scopes", "iam_role_scope_departments",
		"iam_invitations", "iam_invitation_roles",
	}
	require.Len(t, models, 14)
	for index, item := range models {
		assert.Equal(t, expected[index], item.TableName())
	}
}
