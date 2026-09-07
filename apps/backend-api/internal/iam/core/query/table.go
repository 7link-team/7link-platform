package query

import (
	"7link.com/platform/internal/iam/core/model"
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

// Bind applies prefix to all tables owned by the core IAM module.
func Bind(db *gorm.DB, prefix string) *Tables {
	q := Use(db)
	return &Tables{
		Entities:               q.Entity.Table(prefix + model.Entity{}.TableName()),
		FieldPolicies:          q.FieldPolicy.Table(prefix + model.FieldPolicy{}.TableName()),
		Menus:                  q.Menu.Table(prefix + model.Menu{}.TableName()),
		PlatformAdministrators: q.PlatformAdministrator.Table(prefix + model.PlatformAdministrator{}.TableName()),
		PlatformGrants:         q.PlatformGrant.Table(prefix + model.PlatformGrant{}.TableName()),
		PolicyRevisions:        q.PolicyRevision.Table(prefix + model.PolicyRevision{}.TableName()),
		Relationships:          q.Relationship.Table(prefix + model.Relationship{}.TableName()),
		ResourceRelationships:  q.ResourceRelationship.Table(prefix + model.ResourceRelationship{}.TableName()),
		Roles:                  q.Role.Table(prefix + model.Role{}.TableName()),
		RoleBindings:           q.RoleBinding.Table(prefix + model.RoleBinding{}.TableName()),
		RoleMembers:            q.RoleMember.Table(prefix + model.RoleMember{}.TableName()),
		RolePermissions:        q.RolePermission.Table(prefix + model.RolePermission{}.TableName()),
		TemporaryRoleGrants:    q.TemporaryRoleGrant.Table(prefix + model.TemporaryRoleGrant{}.TableName()),
		TenantMembers:          q.TenantMember.Table(prefix + model.TenantMember{}.TableName()),
	}
}
