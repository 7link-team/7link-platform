package core

import (
	"context"
	"net/http"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type APISystemRole struct {
	APIRole
	Builtin bool `json:"builtin"`
}
type APISystemMember struct {
	APITenantMember
	Protected bool `json:"protected"`
}
type systemPageInput struct {
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}
type systemRoleCreateInput struct {
	Body struct {
		Name        string `json:"name" minLength:"1" maxLength:"128"`
		Description string `json:"description,omitempty" maxLength:"4096"`
	}
}
type systemRolePermissionInput struct {
	RoleID         string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PermissionCode string `path:"permission_code" maxLength:"128"`
}
type systemRoleMemberInput struct {
	RoleID      string `path:"role_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type systemMemberListInput struct {
	Offset int          `query:"offset" minimum:"0" default:"0"`
	Limit  int          `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Search string       `query:"search" maxLength:"128"`
	Status MemberStatus `query:"status" enum:"active,disabled"`
}
type systemMemberInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type systemMemberCreateInput struct {
	Body struct {
		PrincipalID  string       `json:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		DisplayName  string       `json:"display_name" minLength:"1" maxLength:"128"`
		Email        string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       MemberStatus `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}
type systemMemberUpdateInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		DisplayName  *string       `json:"display_name,omitempty" minLength:"1" maxLength:"128"`
		Email        *string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID *string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       *MemberStatus `json:"status,omitempty" enum:"active,disabled"`
	}
}

func systemRoleView(role Role) APISystemRole {
	return APISystemRole{APIRole: roleFromDomain(role), Builtin: role.Name == SystemSuperadministratorRoleName}
}
func systemMutationResult(ctx context.Context, changed bool, err error) (*respx.Body[MutationResult], error) {
	if err != nil {
		return nil, platformAPIError("system access mutation", err)
	}
	return respx.OK(ctx, MutationResult{Changed: changed, SyncStatus: "applied"}), nil
}

func registerSystemAccessHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"system"}}
	}
	authz.RegisterPlatform(registrar, api, op("iam-system-roles", http.MethodGet, "/system/roles", "List system roles"), authz.Guard{Resource: "system_role", Verb: "view"}, func(ctx context.Context, in *systemPageInput) (*respx.Body[[]APISystemRole], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system roles", err)
		}
		roles, total, err := s.ListRolesPage(ctx, owner, in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("system roles", err)
		}
		items := make([]APISystemRole, 0, len(roles))
		for _, role := range roles {
			items = append(items, systemRoleView(role))
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role-create", http.MethodPost, "/system/roles", "Create a system role"), authz.Guard{Resource: "system_role", Verb: "create"}, func(ctx context.Context, in *systemRoleCreateInput) (*respx.Body[APISystemRole], error) {
		if reservedSystemRoleName(in.Body.Name) {
			return nil, huma.Error422UnprocessableEntity(respx.InvalidArgument)
		}
		var role Role
		err := svc.systemAccessMutation(ctx, 0, "system_role_create", func(tx *Service, owner guid.ID) error {
			var err error
			role, err = tx.CreateRole(ctx, owner, in.Body.Name, in.Body.Description)
			return err
		})
		if err != nil {
			return nil, apiError("create system role", err)
		}
		return respx.OK(ctx, systemRoleView(role)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role", http.MethodGet, "/system/roles/{role_id}", "Get a system role"), authz.Guard{Resource: "system_role", Verb: "view"}, func(ctx context.Context, in *roleInput) (*respx.Body[APISystemRole], error) {
		id, err := requireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system role", err)
		}
		role, err := s.GetRole(ctx, owner, id)
		if err != nil {
			return nil, apiError("system role", err)
		}
		return respx.OK(ctx, systemRoleView(role)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role-update", http.MethodPatch, "/system/roles/{role_id}", "Update a system role"), authz.Guard{Resource: "system_role", Verb: "update"}, func(ctx context.Context, in *updateRoleInput) (*respx.Body[APISystemRole], error) {
		id, err := requireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		var result Role
		err = svc.systemAccessMutation(ctx, 0, "system_role_update", func(tx *Service, owner guid.ID) error {
			role, err := tx.GetRole(ctx, owner, id)
			if err != nil {
				return err
			}
			if err := requireMutableSystemRole(role); err != nil {
				return err
			}
			if in.Body.Name != nil && *in.Body.Name != role.Name && reservedSystemRoleName(*in.Body.Name) {
				return ErrInvalidArgument
			}
			if in.Body.Name != nil && *in.Body.Name != role.Name && strings.HasPrefix(role.Name, SystemLegacyRolePrefix) {
				permissions, err := tx.repo.ListPermissions(ctx, owner, id)
				if err != nil {
					return err
				}
				for _, code := range permissions {
					if code == "platform_administer" {
						return ErrPrivilegeEscalation
					}
				}
			}
			result, err = tx.UpdateRole(ctx, owner, id, in.Body.Name, in.Body.Description)
			return err
		})
		if err != nil {
			return nil, apiError("update system role", err)
		}
		return respx.OK(ctx, systemRoleView(result)), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role-delete", http.MethodDelete, "/system/roles/{role_id}", "Delete a system role"), authz.Guard{Resource: "system_role", Verb: "delete"}, func(ctx context.Context, in *roleInput) (*respx.Body[MutationResult], error) {
		id, err := requireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		err = svc.systemAccessMutation(ctx, 0, "system_role_delete", func(tx *Service, owner guid.ID) error {
			role, err := tx.GetRole(ctx, owner, id)
			if err != nil {
				return err
			}
			if err := requireMutableSystemRole(role); err != nil {
				return err
			}
			return tx.DeleteRole(ctx, owner, id)
		})
		return systemMutationResult(ctx, err == nil, err)
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role-permission-catalog", http.MethodGet, "/system/role-permission-catalog", "List assignable system role permissions"), authz.Guard{Resource: "system_role", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]authz.Action], error) {
		actions := []authz.Action{}
		for _, action := range svc.PlatformPermissionCatalog(ctx) {
			if action.Code != "platform_administer" {
				actions = append(actions, action)
			}
		}
		return respx.OK(ctx, actions), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-role-permission-grants", http.MethodGet, "/system/roles/{role_id}/permission-grants", "List system role permission grants"), authz.Guard{Resource: "system_role", Verb: "view"}, func(ctx context.Context, in *roleInput) (*respx.Body[[]APIRolePermissionGrant], error) {
		id, err := requireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system role grants", err)
		}
		role, err := s.GetRole(ctx, owner, id)
		if err != nil {
			return nil, apiError("system role grants", err)
		}
		var grants []RolePermissionGrant
		if role.Name == SystemSuperadministratorRoleName {
			for _, action := range s.PlatformPermissionCatalog(ctx) {
				grants = append(grants, RolePermissionGrant{PermissionCode: action.Code, CreatedAt: role.CreatedAt})
			}
		} else {
			grants, err = s.ListPermissionGrants(ctx, owner, id)
		}
		if err != nil {
			return nil, apiError("system role grants", err)
		}
		return respx.OK(ctx, rolePermissionGrantsFromDomain(grants)), nil
	})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		verb := "grant"
		if method == http.MethodDelete {
			verb = "revoke"
		}
		authz.RegisterPlatform(registrar, api, op("iam-system-role-permission-"+verb, method, "/system/roles/{role_id}/permissions/{permission_code}", "Change a system role permission"), authz.Guard{Resource: "system_role", Verb: "grant_permission"}, func(ctx context.Context, in *systemRolePermissionInput) (*respx.Body[MutationResult], error) {
			id, err := requireID(in.RoleID)
			if err != nil {
				return nil, err
			}
			changed := false
			err = svc.systemAccessMutation(ctx, 0, "system_role_grant_permission", func(tx *Service, owner guid.ID) error {
				role, err := tx.GetRole(ctx, owner, id)
				if err != nil {
					return err
				}
				if err := requireMutableSystemRole(role); err != nil {
					return err
				}
				if in.PermissionCode == "platform_administer" && (method == http.MethodPut || !strings.HasPrefix(role.Name, SystemLegacyRolePrefix)) {
					return ErrPrivilegeEscalation
				}
				if method == http.MethodPut {
					changed, err = tx.GrantPermission(ctx, owner, id, in.PermissionCode)
				} else {
					changed, err = tx.RevokePermission(ctx, owner, id, in.PermissionCode)
				}
				return err
			})
			return systemMutationResult(ctx, changed, err)
		})
	}
	authz.RegisterPlatform(registrar, api, op("iam-system-role-members", http.MethodGet, "/system/roles/{role_id}/members", "List direct system role assignments"), authz.Guard{Resource: "system_role", Verb: "view"}, func(ctx context.Context, in *listRoleMembersInput) (*respx.Body[[]guid.ID], error) {
		id, err := requireID(in.RoleID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system role members", err)
		}
		role, err := s.GetRole(ctx, owner, id)
		if err != nil {
			return nil, apiError("system role members", err)
		}
		var ids []guid.ID
		var total int64
		if role.Name == SystemSuperadministratorRoleName {
			err = s.repo.writer().WithContext(ctx).Table(s.repo.names.PlatformAdministrators.String()).Order("principal_id").Pluck("principal_id", &ids).Error
			ids, total = pageOf(ids, in.Offset, in.Limit)
		} else {
			ids, total, err = s.ListRoleMembersPage(ctx, owner, id, in.Offset, in.Limit)
		}
		if err != nil {
			return nil, apiError("system role members", err)
		}
		return respx.List(ctx, ids, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		verb := "add"
		if method == http.MethodDelete {
			verb = "remove"
		}
		authz.RegisterPlatform(registrar, api, op("iam-system-role-member-"+verb, method, "/system/roles/{role_id}/members/{principal_id}", "Change a direct system role assignment"), authz.Guard{Resource: "system_role", Verb: "manage_member"}, func(ctx context.Context, in *systemRoleMemberInput) (*respx.Body[MutationResult], error) {
			role, err := requireID(in.RoleID)
			if err != nil {
				return nil, err
			}
			principal, err := requireID(in.PrincipalID)
			if err != nil {
				return nil, err
			}
			changed, err := svc.changeSystemRoleMember(ctx, role, principal, method == http.MethodPut)
			return systemMutationResult(ctx, changed, err)
		})
	}
	registerSystemMemberHTTP(api, svc, registrar, op)
}

func (s *Service) systemMemberView(ctx context.Context, member TenantMember) (APISystemMember, error) {
	full, err := s.repo.isFullSystemAdministrator(ctx, member.PrincipalID)
	if err != nil {
		return APISystemMember{}, err
	}
	out := APISystemMember{APITenantMember: memberFromDomain(member), Protected: full}
	out.RoleIDs = []guid.ID{}
	err = s.repo.writer().WithContext(ctx).Table(s.repo.names.RoleMembers.String()).Where("tenant_id = ? AND principal_id = ?", member.TenantID, member.PrincipalID).Order("role_id").Pluck("role_id", &out.RoleIDs).Error
	if err != nil {
		return APISystemMember{}, err
	}
	if full {
		roles, err := s.ListRoles(ctx, member.TenantID)
		if err != nil {
			return APISystemMember{}, err
		}
		for _, role := range roles {
			if role.Name == SystemSuperadministratorRoleName {
				out.RoleIDs = append(out.RoleIDs, role.ID)
			}
		}
	}
	return out, nil
}

func registerSystemMemberHTTP(api huma.API, svc *Service, registrar *authz.Registrar, op func(string, string, string, string) huma.Operation) {
	authz.RegisterPlatform(registrar, api, op("iam-system-members", http.MethodGet, "/system/members", "List system account memberships"), authz.Guard{Resource: "system_member", Verb: "view"}, func(ctx context.Context, in *systemMemberListInput) (*respx.Body[[]APISystemMember], error) {
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system members", err)
		}
		members, total, err := s.ListTenantMembers(ctx, owner, MemberFilter{Offset: in.Offset, Limit: in.Limit, Search: in.Search, Status: in.Status})
		if err != nil {
			return nil, apiError("system members", err)
		}
		items := make([]APISystemMember, 0, len(members))
		for _, member := range members {
			item, err := s.systemMemberView(ctx, member)
			if err != nil {
				return nil, apiError("system members", err)
			}
			items = append(items, item)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-member", http.MethodGet, "/system/members/{principal_id}", "Get a system account membership"), authz.Guard{Resource: "system_member", Verb: "view"}, func(ctx context.Context, in *systemMemberInput) (*respx.Body[APISystemMember], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system member", err)
		}
		member, err := s.GetTenantMember(ctx, owner, id)
		if err != nil {
			return nil, apiError("system member", err)
		}
		item, err := s.systemMemberView(ctx, member)
		if err != nil {
			return nil, apiError("system member", err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-member-create", http.MethodPost, "/system/members", "Add an existing account to system administration"), authz.Guard{Resource: "system_member", Verb: "create"}, func(ctx context.Context, in *systemMemberCreateInput) (*respx.Body[APISystemMember], error) {
		id, err := requireID(in.Body.PrincipalID)
		if err != nil {
			return nil, err
		}
		department, err := optionalID(in.Body.DepartmentID)
		if err != nil {
			return nil, err
		}
		var member TenantMember
		err = svc.systemAccessMutation(ctx, id, "system_member_create", func(tx *Service, owner guid.ID) error {
			var err error
			member, err = tx.putRootMember(ctx, "system", owner, id, in.Body.DisplayName, in.Body.Email, department, in.Body.Status, true)
			return err
		})
		if err != nil {
			return nil, apiError("create system member", err)
		}
		item, err := svc.systemMemberView(ctx, member)
		if err != nil {
			return nil, apiError("system member", err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-member-update", http.MethodPatch, "/system/members/{principal_id}", "Update a system membership and primary department"), authz.Guard{Resource: "system_member", Verb: "update"}, func(ctx context.Context, in *systemMemberUpdateInput) (*respx.Body[APISystemMember], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		var member TenantMember
		err = svc.systemAccessMutation(ctx, id, "system_member_update", func(tx *Service, owner guid.ID) error {
			current, err := tx.GetTenantMember(ctx, owner, id)
			if err != nil {
				return err
			}
			if in.Body.DisplayName != nil {
				current.DisplayName = *in.Body.DisplayName
			}
			if in.Body.Email != nil {
				current.Email = *in.Body.Email
			}
			if in.Body.Status != nil {
				current.Status = *in.Body.Status
			}
			if in.Body.DepartmentID != nil {
				department, err := optionalID(*in.Body.DepartmentID)
				if err != nil {
					return err
				}
				current.DepartmentID = department
			}
			if current.Status == MemberDisabled {
				full, err := tx.repo.isFullSystemAdministrator(ctx, id)
				if err != nil {
					return err
				}
				if full {
					return ErrPrivilegeEscalation
				}
			}
			member, err = tx.putRootMember(ctx, "system", owner, id, current.DisplayName, current.Email, current.DepartmentID, current.Status, false)
			return err
		})
		if err != nil {
			return nil, apiError("update system member", err)
		}
		item, err := svc.systemMemberView(ctx, member)
		if err != nil {
			return nil, apiError("system member", err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterPlatform(registrar, api, op("iam-system-member-roles", http.MethodGet, "/system/members/{principal_id}/roles", "List direct system roles assigned to an account"), authz.Guard{Resource: "system_member", Verb: "view"}, func(ctx context.Context, in *systemMemberInput) (*respx.Body[[]guid.ID], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		s, owner, err := svc.systemMenuService(ctx)
		if err != nil {
			return nil, apiError("system member roles", err)
		}
		member, err := s.GetTenantMember(ctx, owner, id)
		if err != nil {
			return nil, apiError("system member roles", err)
		}
		view, err := s.systemMemberView(ctx, member)
		if err != nil {
			return nil, apiError("system member roles", err)
		}
		return respx.OK(ctx, view.RoleIDs), nil
	})
}
