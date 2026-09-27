package core

import (
	"context"
	"net/http"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type operatingPlatformPageInput struct {
	PlatformID string `path:"platform_id"`
	Offset     int    `query:"offset" minimum:"0" default:"0"`
	Limit      int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type operatingPlatformRoleInput struct {
	PlatformID string `path:"platform_id"`
	Body       struct {
		Name        string `json:"name" minLength:"1" maxLength:"128"`
		Description string `json:"description,omitempty" maxLength:"1024"`
	}
}

type operatingPlatformMenuInput struct {
	PlatformID string `path:"platform_id"`
	Body       struct {
		Translations   map[string]string `json:"translations,omitempty"`
		ParentID       string            `json:"parent_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Label          string            `json:"label" minLength:"1" maxLength:"128"`
		Route          string            `json:"route,omitempty" maxLength:"512"`
		Icon           string            `json:"icon,omitempty" maxLength:"64"`
		SortOrder      int               `json:"sort_order,omitempty" minimum:"-100000" maximum:"100000"`
		PermissionCode string            `json:"permission_code,omitempty" maxLength:"128"`
		Status         MenuStatus        `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

type operatingPlatformMembersInput struct {
	PlatformID string       `path:"platform_id"`
	Offset     int          `query:"offset" minimum:"0" default:"0"`
	Limit      int          `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Search     string       `query:"search" maxLength:"128"`
	Status     MemberStatus `query:"status" enum:"active,disabled"`
}

type operatingPlatformMemberInput struct {
	PlatformID string `path:"platform_id"`
	Subject    string `path:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type operatingPlatformMemberPutInput struct {
	PlatformID string `path:"platform_id"`
	Subject    string `path:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body       struct {
		DisplayName  string       `json:"display_name" minLength:"1" maxLength:"128"`
		Email        string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       MemberStatus `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

type operatingPlatformMemberCreateInput struct {
	PlatformID string `path:"platform_id"`
	Body       struct {
		Subject      string       `json:"subject" pattern:"^[1-9][0-9]*$" maxLength:"19"`
		DisplayName  string       `json:"display_name" minLength:"1" maxLength:"128"`
		Email        string       `json:"email,omitempty" maxLength:"320"`
		DepartmentID string       `json:"department_id,omitempty" pattern:"^([1-9][0-9]*)?$" maxLength:"19"`
		Status       MemberStatus `json:"status,omitempty" enum:"active,disabled" default:"active"`
	}
}

func registerOperatingPlatformHTTP(api huma.API, svc *Service, registrar *authz.Registrar) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"operating-platforms"}}
	}
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-roles", http.MethodGet, "/roles", "List operating platform roles"), authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, in *operatingPlatformPageInput) (*respx.Body[[]APIRole], error) {
		roles, total, err := svc.ListRolesPage(ctx, authnext.TenantIDFromContext(ctx), in.Offset, in.Limit)
		if err != nil {
			return nil, apiError("list platform roles", err)
		}
		return respx.List(ctx, rolesFromDomain(roles), respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-role-create", http.MethodPost, "/roles", "Create an operating platform role"), authz.Guard{Resource: "role", Verb: "create"}, func(ctx context.Context, in *operatingPlatformRoleInput) (*respx.Body[APIRole], error) {
		role, err := svc.CreateRole(ctx, authnext.TenantIDFromContext(ctx), in.Body.Name, in.Body.Description)
		if err != nil {
			return nil, apiError("create platform role", err)
		}
		return respx.OK(ctx, roleFromDomain(role)), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-permissions", http.MethodGet, "/permission-catalog", "List operating platform role permissions"), authz.Guard{Resource: "role", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]authz.Action], error) {
		result := []authz.Action{}
		for _, action := range svc.registry.All() {
			if authz.IsOperatingPlatformPermission(action) {
				result = append(result, action)
			}
		}
		return respx.OK(ctx, result), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-menus", http.MethodGet, "/menus", "List operating platform menu definitions"), authz.Guard{Resource: "menu", Verb: "view"}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]APIMenu], error) {
		menus, err := svc.ListMenus(ctx, authnext.TenantIDFromContext(ctx))
		if err != nil {
			return nil, apiError("list platform menus", err)
		}
		return respx.OK(ctx, menusFromDomain(menus)), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-menu-create", http.MethodPost, "/menus", "Create an operating platform menu"), authz.Guard{Resource: "menu", Verb: "create"}, func(ctx context.Context, in *operatingPlatformMenuInput) (*respx.Body[APIMenu], error) {
		parent, err := optionalID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		menu, err := svc.CreateMenu(ctx, Menu{TenantID: authnext.TenantIDFromContext(ctx), ParentID: parent, Label: in.Body.Label, Route: in.Body.Route, Icon: in.Body.Icon, Translations: in.Body.Translations, SortOrder: in.Body.SortOrder, PermissionCode: in.Body.PermissionCode, Status: in.Body.Status})
		if err != nil {
			return nil, apiError("create platform menu", err)
		}
		return respx.OK(ctx, menuFromDomain(menu)), nil
	})
	authz.RegisterOperatingPlatformMember(registrar, api, op("iam-platform-navigation", http.MethodGet, "/navigation", "Return my operating platform menu tree"), func(ctx context.Context, _ *struct{}) (*respx.Body[[]MenuItem], error) {
		menus, err := svc.EffectiveMenus(ctx, authnext.TenantIDFromContext(ctx), authnext.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, apiError("effective platform menus", err)
		}
		return respx.OK(ctx, menus), nil
	})
	authz.RegisterOperatingPlatformMember(registrar, api, op("iam-platform-my-permissions", http.MethodGet, "/permissions", "Return my operating platform permissions"), func(ctx context.Context, _ *struct{}) (*respx.Body[[]string], error) {
		codes, err := svc.EffectivePermissions(ctx, authnext.TenantIDFromContext(ctx), authnext.PrincipalIDFromContext(ctx))
		if err != nil {
			return nil, apiError("effective platform permissions", err)
		}
		return respx.OK(ctx, codes), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-members", http.MethodGet, "/members", "List operating platform members"), authz.Guard{Resource: "platform_member", Verb: "view"}, func(ctx context.Context, in *operatingPlatformMembersInput) (*respx.Body[[]APITenantMember], error) {
		members, total, err := svc.ListOperatingPlatformMembers(ctx, authnext.TenantIDFromContext(ctx), MemberFilter{Search: in.Search, Status: in.Status, Offset: in.Offset, Limit: in.Limit})
		if err != nil {
			return nil, apiError("list platform members", err)
		}
		views := make([]APITenantMember, len(members))
		for i, member := range members {
			views[i] = memberFromDomain(member)
		}
		return respx.List(ctx, views, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-member", http.MethodGet, "/members/{subject}", "Get an operating platform member"), authz.Guard{Resource: "platform_member", Verb: "view"}, func(ctx context.Context, in *operatingPlatformMemberInput) (*respx.Body[APITenantMember], error) {
		id, err := requireID(in.Subject)
		if err != nil {
			return nil, err
		}
		member, err := svc.GetOperatingPlatformMember(ctx, authnext.TenantIDFromContext(ctx), id)
		if err != nil {
			return nil, apiError("get platform member", err)
		}
		return respx.OK(ctx, memberFromDomain(member)), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-member-create", http.MethodPost, "/members", "Bind an existing account to an operating platform"), authz.Guard{Resource: "platform_member", Verb: "create"}, func(ctx context.Context, in *operatingPlatformMemberCreateInput) (*respx.Body[APITenantMember], error) {
		id, err := requireID(in.Body.Subject)
		if err != nil {
			return nil, err
		}
		root := authnext.TenantIDFromContext(ctx)
		department, err := optionalID(in.Body.DepartmentID)
		if err != nil {
			return nil, err
		}
		member, err := svc.CreateOperatingPlatformMember(ctx, root, id, in.Body.DisplayName, in.Body.Email, department, in.Body.Status)
		if err != nil {
			return nil, apiError("create platform member", err)
		}
		return respx.OK(ctx, memberFromDomain(member)), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-member-update", http.MethodPatch, "/members/{subject}", "Update an operating platform member"), authz.Guard{Resource: "platform_member", Verb: "update"}, func(ctx context.Context, in *operatingPlatformMemberPutInput) (*respx.Body[APITenantMember], error) {
		id, err := requireID(in.Subject)
		if err != nil {
			return nil, err
		}
		if _, err := svc.GetOperatingPlatformMember(ctx, authnext.TenantIDFromContext(ctx), id); err != nil {
			return nil, apiError("get platform member", err)
		}
		department, err := optionalID(in.Body.DepartmentID)
		if err != nil {
			return nil, err
		}
		member, err := svc.PutOperatingPlatformMember(ctx, authnext.TenantIDFromContext(ctx), id, in.Body.DisplayName, in.Body.Email, department, in.Body.Status)
		if err != nil {
			return nil, apiError("put platform member", err)
		}
		return respx.OK(ctx, memberFromDomain(member)), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("iam-platform-member-delete", http.MethodDelete, "/members/{subject}", "Disable an operating platform membership"), authz.Guard{Resource: "platform_member", Verb: "delete"}, func(ctx context.Context, in *operatingPlatformMemberInput) (*respx.Body[APITenantMember], error) {
		id, err := requireID(in.Subject)
		if err != nil {
			return nil, err
		}
		member, err := svc.SetOperatingPlatformMemberStatus(ctx, authnext.TenantIDFromContext(ctx), id, MemberDisabled)
		if err != nil {
			return nil, apiError("disable platform member", err)
		}
		return respx.OK(ctx, memberFromDomain(member)), nil
	})
}
