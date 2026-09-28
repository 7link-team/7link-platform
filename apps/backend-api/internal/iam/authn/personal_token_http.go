package authn

import (
	"context"
	"errors"
	"net/http"

	authni18n "7link.com/platform/internal/iam/authn/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type PersonalTokenBrowserInput struct {
	Cookie        string `header:"Cookie"`
	Authorization string `header:"Authorization" hidden:"true"`
}

type personalTokenPrepareInput struct {
	PersonalTokenBrowserInput
	Body PersonalTokenRequest
}

type personalTokenCreateInput struct {
	PersonalTokenBrowserInput
	Body struct {
		PersonalTokenRequest
		Proof string `json:"proof" minLength:"1" maxLength:"256"`
	}
}

type personalTokenListInput struct {
	PersonalTokenBrowserInput
	Offset int `query:"offset" minimum:"0" default:"0"`
	Limit  int `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type personalTokenDeleteInput struct {
	PersonalTokenBrowserInput
	ID string `path:"token_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type personalTokenPermissionsInput struct {
	PersonalTokenBrowserInput
	TenantID string `query:"tenant_id" required:"true" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func personalTokenError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, errPersonalTokenInput) {
		return huma.Error422UnprocessableEntity(authni18n.InvalidPersonalToken)
	}
	if errors.Is(err, authnext.ErrUnavailable) {
		return huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
	}
	return authenticationError(err)
}

func registerPersonalTokenHTTP(api huma.API, service *Service) {
	registrar := authz.NewAuthenticationOnlyRegistrar(service)
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-create-personal-token-preparation", Method: http.MethodPost, DefaultStatus: http.StatusCreated,
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Path:     "/principals/{principal_id}/personal-token-preparations", Summary: "Prepare exact permissions and expiry for fresh verification", Tags: []string{"account"}, Errors: []int{401, 403, 422, 500, 503}},
		func(ctx context.Context, in *personalTokenPrepareInput) (*respx.Body[PersonalTokenPreparation], error) {
			if in.Authorization != "" {
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			}
			result, err := service.PreparePersonalToken(ctx, in.Cookie, in.Body)
			if err != nil {
				return nil, personalTokenError(err)
			}
			return respx.OK(ctx, result), nil
		})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-create-personal-token", Method: http.MethodPost, DefaultStatus: http.StatusCreated,
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Path:     "/principals/{principal_id}/personal-tokens", Summary: "Create a tenant-scoped personal token; return its secret once", Tags: []string{"account"}, Errors: []int{401, 403, 422, 500, 503}},
		func(ctx context.Context, in *personalTokenCreateInput) (*respx.Body[PersonalTokenCreated], error) {
			if in.Authorization != "" {
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			}
			result, err := service.CreatePersonalToken(ctx, in.Cookie, in.Body.Proof, in.Body.PersonalTokenRequest)
			if err != nil {
				return nil, personalTokenError(err)
			}
			return respx.OK(ctx, result), nil
		})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-list-personal-tokens", Method: http.MethodGet,
		Path: "/principals/{principal_id}/personal-tokens", Summary: "List the current browser principal's personal token metadata", Tags: []string{"account"}, Errors: []int{401, 422, 500, 503}},
		func(ctx context.Context, in *personalTokenListInput) (*respx.Body[[]PersonalTokenView], error) {
			if in.Authorization != "" {
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			}
			rows, total, err := service.ListPersonalTokens(ctx, in.Cookie, in.Offset, in.Limit)
			if err != nil {
				return nil, personalTokenError(err)
			}
			return respx.List(ctx, rows, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(rows), Total: total}), nil
		})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-delete-personal-token", Method: http.MethodDelete,
		Path: "/principals/{principal_id}/personal-tokens/{token_id}", Summary: "Revoke an owned personal token", Tags: []string{"account"}, Errors: []int{401, 403, 500, 503}},
		func(ctx context.Context, in *personalTokenDeleteInput) (*respx.Body[map[string]bool], error) {
			if in.Authorization != "" {
				return nil, huma.Error401Unauthorized(respx.Unauthorized)
			}
			id, err := guid.RequireID(in.ID)
			if err != nil {
				return nil, err
			}
			if err := service.RevokePersonalToken(ctx, in.Cookie, id); err != nil {
				return nil, personalTokenError(err)
			}
			return respx.OK(ctx, map[string]bool{"revoked": true}), nil
		})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-list-personal-token-permissions", Method: http.MethodGet,
		Path: "/principals/{principal_id}/personal-token-permissions", Summary: "List currently granted permissions available for a personal token", Tags: []string{"account"}, Errors: []int{401, 422, 500, 503}},
		func(ctx context.Context, in *personalTokenPermissionsInput) (*respx.Body[[]authz.Action], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			result, err := service.PersonalTokenPermissions(ctx, in.Cookie, tenantID)
			if err != nil {
				return nil, personalTokenError(err)
			}
			return respx.OK(ctx, result), nil
		})
}
