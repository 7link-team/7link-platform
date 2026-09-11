package query

import (
	"7link.com/platform/internal/iam/core/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Entities               = entity
	FieldPolicies          = fieldPolicy
	Menus                  = menu
	PlatformAdministrators = platformAdministrator
	PlatformGrants         = platformGrant
	PolicyRevisions        = policyRevision
	Relationships          = relationship
	ResourceRelationships  = resourceRelationship
	Roles                  = role
	RoleBindings           = roleBinding
	RoleMembers            = roleMember
	RolePermissions        = rolePermission
	TemporaryRoleGrants    = temporaryRoleGrant
	TenantMembers          = tenantMember
)

// Tables is the complete generated query set bound to one deployment prefix.
// Rebinding the whole set together prevents a transaction from mixing prefixed
// and unprefixed IAM tables.
type Tables struct {
	Entities               *Entities
	FieldPolicies          *FieldPolicies
	Menus                  *Menus
	PlatformAdministrators *PlatformAdministrators
	PlatformGrants         *PlatformGrants
	PolicyRevisions        *PolicyRevisions
	Relationships          *Relationships
	ResourceRelationships  *ResourceRelationships
	Roles                  *Roles
	RoleBindings           *RoleBindings
	RoleMembers            *RoleMembers
	RolePermissions        *RolePermissions
	TemporaryRoleGrants    *TemporaryRoleGrants
	TenantMembers          *TenantMembers
}

// TableNames is the physical name of every table core owns under one
// deployment prefix.
type TableNames struct {
	Entities               gormx.TableName
	FieldPolicies          gormx.TableName
	Menus                  gormx.TableName
	PlatformAdministrators gormx.TableName
	PlatformGrants         gormx.TableName
	PolicyRevisions        gormx.TableName
	Relationships          gormx.TableName
	ResourceRelationships  gormx.TableName
	Roles                  gormx.TableName
	RoleBindings           gormx.TableName
	RoleMembers            gormx.TableName
	RolePermissions        gormx.TableName
	TemporaryRoleGrants    gormx.TableName
	TenantMembers          gormx.TableName
}

// BindNames returns the physical name of every table core owns. It is the only
// place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of core's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Entities:               gormx.NewTableName(prefix, model.Entity{}.TableName()),
		FieldPolicies:          gormx.NewTableName(prefix, model.FieldPolicy{}.TableName()),
		Menus:                  gormx.NewTableName(prefix, model.Menu{}.TableName()),
		PlatformAdministrators: gormx.NewTableName(prefix, model.PlatformAdministrator{}.TableName()),
		PlatformGrants:         gormx.NewTableName(prefix, model.PlatformGrant{}.TableName()),
		PolicyRevisions:        gormx.NewTableName(prefix, model.PolicyRevision{}.TableName()),
		Relationships:          gormx.NewTableName(prefix, model.Relationship{}.TableName()),
		ResourceRelationships:  gormx.NewTableName(prefix, model.ResourceRelationship{}.TableName()),
		Roles:                  gormx.NewTableName(prefix, model.Role{}.TableName()),
		RoleBindings:           gormx.NewTableName(prefix, model.RoleBinding{}.TableName()),
		RoleMembers:            gormx.NewTableName(prefix, model.RoleMember{}.TableName()),
		RolePermissions:        gormx.NewTableName(prefix, model.RolePermission{}.TableName()),
		TemporaryRoleGrants:    gormx.NewTableName(prefix, model.TemporaryRoleGrant{}.TableName()),
		TenantMembers:          gormx.NewTableName(prefix, model.TenantMember{}.TableName()),
	}
}

// ForeignTables names the tables the core authorizer reads but does not own.
//
// The authorization statements join the principal table of the authn module
// and the group, position and department tables of the organization module.
// Importing those modules' query packages is not an option: pkg/module's
// Activate resolves a total order over module dependencies, core is activated
// before them, and a reverse import would make that order unsatisfiable, which
// fails at startup rather than at review.
//
// TODO(P5): the owning modules publish these names through their TableSet, and
// this is where the injected set is read instead of the prefix. Until then this
// function is the single point in core where a prefix reaches a table the
// module does not own, so the concatenation stays auditable.
type ForeignTables struct {
	Principals           gormx.TableName
	Groups               gormx.TableName
	GroupMembers         gormx.TableName
	GroupRoleBindings    gormx.TableName
	Positions            gormx.TableName
	PositionMembers      gormx.TableName
	PositionRoleBindings gormx.TableName
	Departments          gormx.TableName
	DepartmentClosure    gormx.TableName
	MemberDepartments    gormx.TableName
	RoleDataScopes       gormx.TableName
	RoleScopeDepartments gormx.TableName
}

// BindForeign applies prefix to every table core joins but does not own. The
// logical names are written down here and nowhere else in the module.
func BindForeign(prefix string) ForeignTables {
	return ForeignTables{
		Principals:           gormx.NewTableName(prefix, "iam_principals"),
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
	}
}

// AuthorizationTables is every physical table name an authorization statement
// may name: the tables core owns and the ones it only joins. It exists so a
// statement is handed one value that already carries the deployment prefix,
// rather than a prefix it could apply unevenly to half its joins.
type AuthorizationTables struct {
	Tables  TableNames
	Foreign ForeignTables
}

// BindAuthorizationTables resolves both sets from one deployment prefix.
func BindAuthorizationTables(prefix string) AuthorizationTables {
	return AuthorizationTables{Tables: BindNames(prefix), Foreign: BindForeign(prefix)}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN core's tables.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.Entity{}.TableName():                names.Entities,
		model.FieldPolicy{}.TableName():           names.FieldPolicies,
		model.Menu{}.TableName():                  names.Menus,
		model.PlatformAdministrator{}.TableName(): names.PlatformAdministrators,
		model.PlatformGrant{}.TableName():         names.PlatformGrants,
		model.PolicyRevision{}.TableName():        names.PolicyRevisions,
		model.Relationship{}.TableName():          names.Relationships,
		model.ResourceRelationship{}.TableName():  names.ResourceRelationships,
		model.Role{}.TableName():                  names.Roles,
		model.RoleBinding{}.TableName():           names.RoleBindings,
		model.RoleMember{}.TableName():            names.RoleMembers,
		model.RolePermission{}.TableName():        names.RolePermissions,
		model.TemporaryRoleGrant{}.TableName():    names.TemporaryRoleGrants,
		model.TenantMember{}.TableName():          names.TenantMembers,
	}
}

// Bind applies prefix to all tables owned by the core IAM module. It builds
// from BindNames, so the prefix reaches the generated query objects through
// one path rather than fourteen.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		Entities:               q.Entity.Table(names.Entities.String()),
		FieldPolicies:          q.FieldPolicy.Table(names.FieldPolicies.String()),
		Menus:                  q.Menu.Table(names.Menus.String()),
		PlatformAdministrators: q.PlatformAdministrator.Table(names.PlatformAdministrators.String()),
		PlatformGrants:         q.PlatformGrant.Table(names.PlatformGrants.String()),
		PolicyRevisions:        q.PolicyRevision.Table(names.PolicyRevisions.String()),
		Relationships:          q.Relationship.Table(names.Relationships.String()),
		ResourceRelationships:  q.ResourceRelationship.Table(names.ResourceRelationships.String()),
		Roles:                  q.Role.Table(names.Roles.String()),
		RoleBindings:           q.RoleBinding.Table(names.RoleBindings.String()),
		RoleMembers:            q.RoleMember.Table(names.RoleMembers.String()),
		RolePermissions:        q.RolePermission.Table(names.RolePermissions.String()),
		TemporaryRoleGrants:    q.TemporaryRoleGrant.Table(names.TemporaryRoleGrants.String()),
		TenantMembers:          q.TenantMember.Table(names.TenantMembers.String()),
	}
}
