package organization

import (
	"7link.com/platform/internal/iam/organization/query"
	gormx "7link.com/platform/pkg/extension/gorm"
)

// TableSet publishes the physical names of the tables organization owns to the
// modules that JOIN them.
//
// It carries names rather than a database handle or a prefix: a consumer
// resolves the names it needs and queries with its own connection, so it cannot
// build a name organization never published.
func TableSet(prefix string) gormx.TableSet {
	return gormx.NewTableSet(query.BindNames(prefix).Map())
}

// DirectoryTableNames is the physical name of every table organization owns, as organization hands
// them to the modules that JOIN them.
//
// The fields are named, not looked up by string, so a consumer cannot ask for a
// table organization does not have and cannot misspell the one it wants.
type DirectoryTableNames struct {
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

// DirectoryTables is the opaque form of the same publication, still resolved by the
// consumers that have not moved to the typed names yet. It goes away with the
// last of them.
type DirectoryTables struct {
	Published gormx.TableSet
}
