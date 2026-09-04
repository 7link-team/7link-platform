// Command dbgen generates organization's typed query package.
package main

import (
	"7link.com/platform/internal/iam/organization/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(
		model.Department{}, model.DepartmentClosure{},
		model.Position{}, model.PositionMember{},
		model.Group{}, model.GroupMember{},
		model.GroupRoleBinding{}, model.PositionRoleBinding{},
		model.Tenant{}, model.MemberDepartment{},
		model.RoleDataScope{}, model.RoleScopeDepartment{},
		model.Invitation{}, model.InvitationRole{},
	)
}
