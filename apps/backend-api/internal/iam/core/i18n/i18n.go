package corei18n

import (
	"context"
	"embed"

	platformi18n "7link.com/platform/pkg/i18n"
)

const (
	Namespace = "iam"

	RoleNameConflict                   = Namespace + ".role_name_conflict"
	PermissionNotFound                 = Namespace + ".permission_not_found"
	InvalidRequest                     = Namespace + ".invalid_request"
	ResourceNotFound                   = Namespace + ".resource_not_found"
	ResourceConflict                   = Namespace + ".resource_conflict"
	TenantMemberInactive               = Namespace + ".tenant_member_inactive"
	DirectoryAssigneeNotFound          = Namespace + ".directory_assignee_not_found"
	DirectoryAssigneeInactive          = Namespace + ".directory_assignee_inactive"
	LastTenantAdministrator            = Namespace + ".last_tenant_administrator"
	EntityNotFound                     = Namespace + ".entity_not_found"
	EntityConflict                     = Namespace + ".entity_conflict"
	EntityHasChildren                  = Namespace + ".entity_has_children"
	EntityHasRoleBindings              = Namespace + ".entity_has_role_bindings"
	EntityHierarchyConflict            = Namespace + ".entity_hierarchy_conflict"
	AuthorizationPolicyChanged         = Namespace + ".authorization_policy_changed"
	InvalidRoleDataScope               = Namespace + ".invalid_role_data_scope"
	RoleDataScopeDepartmentRequired    = Namespace + ".role_data_scope_department_required"
	RoleDataScopeDepartmentsNotAllowed = Namespace + ".role_data_scope_departments_not_allowed"
	RoleScopeDepartmentNotFound        = Namespace + ".role_scope_department_not_found"
	RoleScopeDepartmentInactive        = Namespace + ".role_scope_department_inactive"
	MemberDepartmentNotFound           = Namespace + ".member_department_not_found"
	MemberDepartmentInactive           = Namespace + ".member_department_inactive"
	EntityHasRelationships             = Namespace + ".entity_has_relationships"
	InvalidRelationship                = Namespace + ".invalid_relationship"
	RelationshipSubjectNotFound        = Namespace + ".relationship_subject_not_found"
	RelationshipSubjectInactive        = Namespace + ".relationship_subject_inactive"
	RelationshipResourceInactive       = Namespace + ".relationship_resource_inactive"
	RelationshipHierarchyConflict      = Namespace + ".relationship_hierarchy_conflict"
	InvalidResourceAuthorization       = Namespace + ".invalid_resource_authorization"
	InvalidRelationshipWindow          = Namespace + ".invalid_relationship_window"
	InvalidRelationshipCondition       = Namespace + ".invalid_relationship_condition"
	RolePermissionNotGranted           = Namespace + ".role_permission_not_granted"
	InvalidRolePermissionCondition     = Namespace + ".invalid_role_permission_condition"
	PlatformAdministratorNotFound      = Namespace + ".platform_administrator_not_found"
	LastPlatformAdministrator          = Namespace + ".last_platform_administrator"
	PlatformPermissionScope            = Namespace + ".platform_permission_scope"
	PrivilegeEscalationDenied          = Namespace + ".privilege_escalation_denied"
	Unavailable                        = Namespace + ".unavailable"
)

//go:embed locales/*.json
var locales embed.FS

var fieldMessages = platformi18n.NewModuleI18nFS(platformi18n.Base, locales, "locales")

// Translations supplies all supported translations for built-in stored labels.
func Translations(key string) map[string]string {
	labels := make(map[string]string)
	for _, locale := range platformi18n.Supported() {
		ctx := platformi18n.WithLocale(context.Background(), locale.Code)
		labels[locale.Code] = fieldMessages.TContext(ctx, key)
	}
	return labels
}

func Register() error {
	return platformi18n.RegisterFS(Namespace, locales, "locales")
}
