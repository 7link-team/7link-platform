package query

import (
	"7link.com/platform/internal/iam/organization/model"
	"gorm.io/gorm"
)

type (
	Departments          = department
	DepartmentClosures   = departmentClosure
	Positions            = position
	PositionMembers      = positionMember
	Groups               = group
	GroupMembers         = groupMember
	GroupRoleBindings    = groupRoleBinding
	PositionRoleBindings = positionRoleBinding
	Tenants              = tenant
	MemberDepartments    = memberDepartment
	RoleDataScopes       = roleDataScope
	RoleScopeDepartments = roleScopeDepartment
	Invitations          = invitation
	InvitationRoles      = invitationRole
)

type Tables struct {
	Departments          *Departments
	DepartmentClosures   *DepartmentClosures
	Positions            *Positions
	PositionMembers      *PositionMembers
	Groups               *Groups
	GroupMembers         *GroupMembers
	GroupRoleBindings    *GroupRoleBindings
	PositionRoleBindings *PositionRoleBindings
	Tenants              *Tenants
	MemberDepartments    *MemberDepartments
	RoleDataScopes       *RoleDataScopes
	RoleScopeDepartments *RoleScopeDepartments
	Invitations          *Invitations
	InvitationRoles      *InvitationRoles
}

// Bind applies prefix to every table owned by the organization module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Departments:          q.Department.Table(prefix + model.Department{}.TableName()),
		DepartmentClosures:   q.DepartmentClosure.Table(prefix + model.DepartmentClosure{}.TableName()),
		Positions:            q.Position.Table(prefix + model.Position{}.TableName()),
		PositionMembers:      q.PositionMember.Table(prefix + model.PositionMember{}.TableName()),
		Groups:               q.Group.Table(prefix + model.Group{}.TableName()),
		GroupMembers:         q.GroupMember.Table(prefix + model.GroupMember{}.TableName()),
		GroupRoleBindings:    q.GroupRoleBinding.Table(prefix + model.GroupRoleBinding{}.TableName()),
		PositionRoleBindings: q.PositionRoleBinding.Table(prefix + model.PositionRoleBinding{}.TableName()),
		Tenants:              q.Tenant.Table(prefix + model.Tenant{}.TableName()),
		MemberDepartments:    q.MemberDepartment.Table(prefix + model.MemberDepartment{}.TableName()),
		RoleDataScopes:       q.RoleDataScope.Table(prefix + model.RoleDataScope{}.TableName()),
		RoleScopeDepartments: q.RoleScopeDepartment.Table(prefix + model.RoleScopeDepartment{}.TableName()),
		Invitations:          q.Invitation.Table(prefix + model.Invitation{}.TableName()),
		InvitationRoles:      q.InvitationRole.Table(prefix + model.InvitationRole{}.TableName()),
	}
}
