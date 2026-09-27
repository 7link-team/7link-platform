package organization

import (
	"context"
	"net/http"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type platformPageSelector struct {
	PlatformID string `path:"platform_id"`
	Offset     int    `query:"offset" minimum:"0" default:"0"`
	Limit      int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createPlatformDepartmentInput struct {
	PlatformID string `path:"platform_id"`
	Body       struct {
		ParentID  string `json:"parent_id,omitempty" maxLength:"19"`
		Name      string `json:"name" minLength:"1" maxLength:"128"`
		Status    string `json:"status,omitempty" enum:"active,disabled" default:"active"`
		SortOrder int    `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
	}
}

type createPlatformGroupInput struct {
	PlatformID string `path:"platform_id"`
	Body       struct {
		Name        string         `json:"name" minLength:"1" maxLength:"128"`
		Type        string         `json:"type,omitempty" enum:"static,dynamic" default:"static"`
		Rule        MembershipRule `json:"membership_rule,omitempty"`
		Description string         `json:"description,omitempty" maxLength:"1024"`
		Status      string         `json:"status,omitempty" enum:"active,disabled" default:"active"`
		SortOrder   int            `json:"sort_order,omitempty" minimum:"0" maximum:"1000000"`
	}
}

func registerPlatformDirectoryHTTP(api huma.API, service *Service, groups *GroupService, registrar *authz.Registrar) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"operating-platforms"}}
	}
	authz.RegisterOperatingPlatform(registrar, api, op("organization-platform-departments", http.MethodGet, "/departments", "List operating platform departments"), authz.Guard{Resource: "dept", Verb: "view"}, func(ctx context.Context, in *platformPageSelector) (*respx.Body[[]Department], error) {
		items, total, err := service.ListPage(ctx, authnext.TenantIDFromContext(ctx), in.Offset, in.Limit)
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("organization-platform-department-create", http.MethodPost, "/departments", "Create an operating platform department"), authz.Guard{Resource: "dept", Verb: "create"}, func(ctx context.Context, in *createPlatformDepartmentInput) (*respx.Body[Department], error) {
		parent, err := parseOptionalOrganizationID(in.Body.ParentID)
		if err != nil {
			return nil, err
		}
		item, err := service.Create(ctx, authnext.TenantIDFromContext(ctx), CreateDepartment{ParentID: parent, Name: in.Body.Name, Status: in.Body.Status, SortOrder: in.Body.SortOrder})
		if err != nil {
			return nil, departmentError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("organization-platform-groups", http.MethodGet, "/principal-groups", "List operating platform user groups"), authz.Guard{Resource: "group", Verb: "view"}, func(ctx context.Context, in *platformPageSelector) (*respx.Body[[]Group], error) {
		items, total, err := groups.ListPage(ctx, authnext.TenantIDFromContext(ctx), in.Offset, in.Limit)
		if err != nil {
			return nil, groupError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
	})
	authz.RegisterOperatingPlatform(registrar, api, op("organization-platform-group-create", http.MethodPost, "/principal-groups", "Create an operating platform user group"), authz.Guard{Resource: "group", Verb: "create"}, func(ctx context.Context, in *createPlatformGroupInput) (*respx.Body[Group], error) {
		item, err := groups.Create(ctx, authnext.TenantIDFromContext(ctx), CreateGroup{Name: in.Body.Name, Type: in.Body.Type, Rule: in.Body.Rule, Description: in.Body.Description, Status: in.Body.Status, SortOrder: in.Body.SortOrder})
		if err != nil {
			return nil, groupError(err)
		}
		return respx.OK(ctx, item), nil
	})
}
