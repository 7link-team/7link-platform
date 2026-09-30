package organization

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type platformIDInput struct {
	ID string `path:"platform_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type updatePlatformInput struct {
	ID   string `path:"platform_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body struct {
		Name    *string `json:"name,omitempty" minLength:"1" maxLength:"128"`
		Status  *string `json:"status,omitempty" enum:"active,suspended"`
		Version int64   `json:"version" minimum:"1"`
	}
}

type deletePlatformInput struct {
	ID      string `path:"platform_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Version int64  `query:"version" minimum:"1"`
}

func registerPlatformHTTP(api huma.API, service *PlatformService, registrar *authz.Registrar) {
	authz.RegisterPrincipalAuthenticated(registrar, api, huma.Operation{
		OperationID: "organization-my-operating-platforms", Method: http.MethodGet, Path: "/principals/{principal_id}/operating-platforms",
		Summary: "List the account's operating platform memberships", Tags: []string{"account"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[[]Platform], error) {
		claims, ok := authnext.FromContext(ctx)
		if !ok || claims == nil {
			return nil, huma.Error401Unauthorized("respx.unauthorized")
		}
		items, err := service.ListByMember(ctx, claims.PrincipalID)
		if err != nil {
			return nil, tenantError(err)
		}
		return respx.OK(ctx, items), nil
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-list-operating-platforms", Method: http.MethodGet, Path: "/operating-platforms",
		Summary: "List operating platforms", Tags: []string{"directory"},
	}, authz.Guard{Resource: "operating_platform", Verb: "view"},
		func(ctx context.Context, in *listTenantsInput) (*respx.Body[[]Platform], error) {
			items, total, err := service.ListPage(ctx, in.IncludeDeleted, in.Search, in.Offset, in.Limit)
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-create-operating-platform", Method: http.MethodPost, Path: "/operating-platforms",
		Summary: "Create an operating platform", Tags: []string{"directory"}, DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "operating_platform", Verb: "create"},
		func(ctx context.Context, in *createTenantInput) (*respx.Body[Platform], error) {
			owner, err := guid.RequireID(in.Body.OwnerPrincipalID)
			if err != nil {
				return nil, err
			}
			item, err := service.Create(ctx, CreatePlatform{Slug: in.Body.Slug, Name: in.Body.Name, OwnerPrincipalID: owner})
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-get-operating-platform", Method: http.MethodGet, Path: "/operating-platforms/{platform_id}",
		Summary: "Get an operating platform", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "operating_platform", Verb: "view"},
		func(ctx context.Context, in *platformIDInput) (*respx.Body[Platform], error) {
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Get(ctx, id)
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-update-operating-platform", Method: http.MethodPatch, Path: "/operating-platforms/{platform_id}",
		Summary: "Update or suspend an operating platform", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "operating_platform", Verb: "update"},
		func(ctx context.Context, in *updatePlatformInput) (*respx.Body[Platform], error) {
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return nil, err
			}
			item, err := service.Update(ctx, id, UpdatePlatform{Name: in.Body.Name, Status: in.Body.Status, Version: in.Body.Version})
			if err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "organization-delete-operating-platform", Method: http.MethodDelete, Path: "/operating-platforms/{platform_id}",
		Summary: "Soft-delete an operating platform", Tags: []string{"directory"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "operating_platform", Verb: "delete"},
		func(ctx context.Context, in *deletePlatformInput) (*respx.Body[deletedResource], error) {
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.Delete(ctx, id, in.Version); err != nil {
				return nil, tenantError(err)
			}
			return respx.OK(ctx, deletedResource{Deleted: true}), nil
		})
}
