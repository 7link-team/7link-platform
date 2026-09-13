package query

import (
	"7link.com/platform/internal/iam/organization/model"
	gormx "7link.com/platform/pkg/extension/gorm"
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

// TableNames is the physical name of every table organization owns under one
// deployment prefix.
type TableNames struct {
	Departments          gormx.TableName
	DepartmentClosures   gormx.TableName
	Positions            gormx.TableName
	PositionMembers      gormx.TableName
	Groups               gormx.TableName
	GroupMembers         gormx.TableName
	GroupRoleBindings    gormx.TableName
	PositionRoleBindings gormx.TableName
	Tenants              gormx.TableName
	MemberDepartments    gormx.TableName
	RoleDataScopes       gormx.TableName
	RoleScopeDepartments gormx.TableName
	Invitations          gormx.TableName
	InvitationRoles      gormx.TableName
}

// BindNames returns the physical name of every table organization owns. It is
// the only place the deployment prefix is applied to one of them, so a
// deployment cannot end up with half of organization's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Departments:          gormx.NewTableName(prefix, model.Department{}.TableName()),
		DepartmentClosures:   gormx.NewTableName(prefix, model.DepartmentClosure{}.TableName()),
		Positions:            gormx.NewTableName(prefix, model.Position{}.TableName()),
		PositionMembers:      gormx.NewTableName(prefix, model.PositionMember{}.TableName()),
		Groups:               gormx.NewTableName(prefix, model.Group{}.TableName()),
		GroupMembers:         gormx.NewTableName(prefix, model.GroupMember{}.TableName()),
		GroupRoleBindings:    gormx.NewTableName(prefix, model.GroupRoleBinding{}.TableName()),
		PositionRoleBindings: gormx.NewTableName(prefix, model.PositionRoleBinding{}.TableName()),
		Tenants:              gormx.NewTableName(prefix, model.Tenant{}.TableName()),
		MemberDepartments:    gormx.NewTableName(prefix, model.MemberDepartment{}.TableName()),
		RoleDataScopes:       gormx.NewTableName(prefix, model.RoleDataScope{}.TableName()),
		RoleScopeDepartments: gormx.NewTableName(prefix, model.RoleScopeDepartment{}.TableName()),
		Invitations:          gormx.NewTableName(prefix, model.Invitation{}.TableName()),
		InvitationRoles:      gormx.NewTableName(prefix, model.InvitationRole{}.TableName()),
	}
}

// Bind applies prefix to every table owned by the organization module. It
// builds from BindNames, so the prefix reaches the generated query objects
// through one path rather than fourteen.
func Bind(db *gorm.DB, prefix string) *Tables {
	return BindWithNames(db, BindNames(prefix))
}

// BindWithNames binds the query objects to names a caller already resolved, so
// a repository that resolved them at construction applies the same names to
// every statement it composes. It is the resolver path: a module that holds its
// names does not build them again from the prefix per statement.
func BindWithNames(db *gorm.DB, names TableNames) *Tables {
	q := Use(db)
	return &Tables{
		Departments:          q.Department.Table(names.Departments.String()),
		DepartmentClosures:   q.DepartmentClosure.Table(names.DepartmentClosures.String()),
		Positions:            q.Position.Table(names.Positions.String()),
		PositionMembers:      q.PositionMember.Table(names.PositionMembers.String()),
		Groups:               q.Group.Table(names.Groups.String()),
		GroupMembers:         q.GroupMember.Table(names.GroupMembers.String()),
		GroupRoleBindings:    q.GroupRoleBinding.Table(names.GroupRoleBindings.String()),
		PositionRoleBindings: q.PositionRoleBinding.Table(names.PositionRoleBindings.String()),
		Tenants:              q.Tenant.Table(names.Tenants.String()),
		MemberDepartments:    q.MemberDepartment.Table(names.MemberDepartments.String()),
		RoleDataScopes:       q.RoleDataScope.Table(names.RoleDataScopes.String()),
		RoleScopeDepartments: q.RoleScopeDepartment.Table(names.RoleScopeDepartments.String()),
		Invitations:          q.Invitation.Table(names.Invitations.String()),
		InvitationRoles:      q.InvitationRole.Table(names.InvitationRoles.String()),
	}
}
