package organization

import (
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// DirectoryTableNames is the physical name of every table organization owns, as organization hands
// them to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table organization does not have and cannot misspell the one it wants.
type DirectoryTableNames struct {
	Departments        gormx.TableName
	DepartmentClosures gormx.TableName
	// Legacy names support historical schema checks and upgrade migrations only.
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

// PublishedDirectoryTableNames binds every name organization owns to one deployment prefix. It
// builds from the module's own BindNames, so the prefix is applied the same way
// it is everywhere else organization queries, and a table added to organization cannot be
// forgotten here without the drift test noticing.
func PublishedDirectoryTableNames(prefix string) DirectoryTableNames {
	names := query.BindNames(prefix)
	return DirectoryTableNames{
		Departments:          names.Departments,
		DepartmentClosures:   names.DepartmentClosures,
		Positions:            names.Positions,
		PositionMembers:      names.PositionMembers,
		Groups:               names.Groups,
		GroupMembers:         names.GroupMembers,
		GroupRoleBindings:    names.GroupRoleBindings,
		PositionRoleBindings: names.PositionRoleBindings,
		Tenants:              names.Tenants,
		MemberDepartments:    names.MemberDepartments,
		RoleDataScopes:       names.RoleDataScopes,
		RoleScopeDepartments: names.RoleScopeDepartments,
		Invitations:          names.Invitations,
		InvitationRoles:      names.InvitationRoles,
	}
}

// Bound reports whether these names arrived. PublishedDirectoryTableNames fills every field
// at once, so one zero field means the consumer was never given the set rather
// than that organization published an incomplete one.
func (t DirectoryTableNames) Bound() bool { return !t.Tenants.IsZero() }

// CoreDirectoryTableNames fills the shape core declares from what organization
// publishes. It is the one mapping between the two field sets: the connector
// binds it during Wire, and a harness that builds an evaluator outside the
// module calls it rather than writing the twelve fields out again, so a
// harness cannot pair core with names the deployment never uses.
func CoreDirectoryTableNames(prefix string) core.DirectoryTableNames {
	published := PublishedDirectoryTableNames(prefix)
	return core.DirectoryTableNames{
		Groups:               published.Groups,
		GroupMembers:         published.GroupMembers,
		GroupRoleBindings:    published.GroupRoleBindings,
		Departments:          published.Departments,
		DepartmentClosure:    published.DepartmentClosures,
		MemberDepartments:    published.MemberDepartments,
		RoleDataScopes:       published.RoleDataScopes,
		RoleScopeDepartments: published.RoleScopeDepartments,
		Tenants:              published.Tenants,
	}
}
