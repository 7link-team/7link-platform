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

// DirectoryTables names the tables the organization module owns and the
// authorization statements join: the group, position and department tables and
// the role data scopes attached to them.
//
// Importing that module's query package is not an option: pkg/module's Activate
// resolves a total order over module dependencies, core is activated before
// organization, and a reverse import would make that order unsatisfiable, which
// fails at startup rather than at review.
//
// So the names arrive as the set organization publishes, and the logical names
// below are a request rather than a definition: a name the owner does not
// publish is an error, not a concatenation core guessed wrong in a deployment
// that has a prefix.
type DirectoryTables struct {
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
	Tenants              gormx.TableName
}

// AuthorizationTables is every physical table name an authorization statement
// may name: the tables core owns, the directory tables organization publishes
// and the principal table identity publishes. It exists so a statement is
// handed one value that already carries the deployment prefix, rather than a
// prefix it could apply unevenly to half its joins.
type AuthorizationTables struct {
	Tables    TableNames
	Directory DirectoryTables
	Principal gormx.TableName
}

// BindAuthorizationTables assembles one statement's table names from core's own
// deployment prefix and from the sets the owning modules published.
func BindAuthorizationTables(
	prefix string,
	directory DirectoryTables,
	principal gormx.TableName,
) AuthorizationTables {
	return AuthorizationTables{Tables: BindNames(prefix), Directory: directory, Principal: principal}
}

// BindWithNames applies an already-bound name set to all tables owned by the
// core IAM module. It is the same binding as Bind but with the names resolved
// once, so a caller that holds its names binds from them rather than from a
// prefix string.
func BindWithNames(db *gorm.DB, names TableNames) *Tables {
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

// Bind applies prefix to all tables owned by the core IAM module. It builds
// from BindNames, so the prefix reaches the generated query objects through
// one path rather than fourteen.
func Bind(db *gorm.DB, prefix string) *Tables {
	return BindWithNames(db, BindNames(prefix))
}
