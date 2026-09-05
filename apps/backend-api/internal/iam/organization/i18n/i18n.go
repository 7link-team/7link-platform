// Package organizationi18n publishes the organization module's locale bundle.
package organizationi18n

import (
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "organization"

	DepartmentNotFound          = Namespace + ".department_not_found"
	DepartmentNameExists        = Namespace + ".department_name_exists"
	DepartmentHierarchyCycle    = Namespace + ".department_hierarchy_cycle"
	DepartmentHasChildren       = Namespace + ".department_has_children"
	DepartmentInUse             = Namespace + ".department_in_use"
	DepartmentVersionConflict   = Namespace + ".department_version_conflict"
	InvalidDepartment           = Namespace + ".invalid_department"
	Unavailable                 = Namespace + ".organization_unavailable"
	GroupNotFound               = Namespace + ".group_not_found"
	GroupNameExists             = Namespace + ".group_name_exists"
	GroupVersionConflict        = Namespace + ".group_version_conflict"
	GroupHasMembers             = Namespace + ".group_has_members"
	GroupRoleBound              = Namespace + ".group_role_bound"
	GroupRelationshipBound      = Namespace + ".group_relationship_bound"
	GroupMemberInactive         = Namespace + ".group_member_inactive"
	DynamicGroupMembersComputed = Namespace + ".dynamic_group_members_computed"
	GroupRuleRequiresDynamic    = Namespace + ".group_rule_requires_dynamic_type"
	InvalidGroupRule            = Namespace + ".invalid_group_membership_rule"
	LastTenantAdministrator     = Namespace + ".last_tenant_administrator"
	InvalidGroup                = Namespace + ".invalid_group"
	PositionNotFound            = Namespace + ".position_not_found"
	PositionCodeExists          = Namespace + ".position_code_exists"
	PositionVersionConflict     = Namespace + ".position_version_conflict"
	PositionHasMembers          = Namespace + ".position_has_members"
	PositionRoleBound           = Namespace + ".position_role_bound"
	PositionRelationshipBound   = Namespace + ".position_relationship_bound"
	PositionMemberInactive      = Namespace + ".position_member_inactive"
	InvalidPosition             = Namespace + ".invalid_position"
	TenantNotFound              = Namespace + ".tenant_not_found"
	TenantSlugExists            = Namespace + ".tenant_slug_exists"
	TenantVersionConflict       = Namespace + ".tenant_version_conflict"
	InvalidTenant               = Namespace + ".invalid_tenant"
	TenantOwnerNotFound         = Namespace + ".tenant_owner_not_found"
	TenantOwnerInactive         = Namespace + ".tenant_owner_inactive"
	InvitationNotFound          = Namespace + ".invitation_not_found"
	InvalidInvitationCredential = Namespace + ".invalid_invitation_credential"
	InvitationExpired           = Namespace + ".invitation_expired"
	InvitationStateConflict     = Namespace + ".invitation_state_conflict"
	InvitationLoginNameExists   = Namespace + ".invitation_login_name_exists"
	InvitationBindingNotFound   = Namespace + ".invitation_binding_not_found"
	InvitationBindingInactive   = Namespace + ".invitation_binding_inactive"
	InvalidInvitation           = Namespace + ".invalid_invitation"
)

//go:embed locales/*.json
var locales embed.FS

// Register publishes the organization locale bundle under its module namespace.
func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
