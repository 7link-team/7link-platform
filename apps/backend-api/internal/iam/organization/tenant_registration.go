package organization

import (
	"context"
	"net/http"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

// TenantRegistrationPolicy tells an authenticated principal whether this
// deployment lets it register a tenant of its own.
type TenantRegistrationPolicy struct {
	Enabled bool `json:"enabled"`
}

func registerTenantRegistrationHTTP(api huma.API, service *TenantService, registrar *authz.Registrar) {
	authz.RegisterAuthenticated(registrar, api, huma.Operation{
		OperationID: "organization-tenant-registration-policy", Method: http.MethodGet,
		Path: "/tenant-registrations/options", Summary: "Get tenant self-registration availability", Tags: []string{"tenant-registrations"},
	}, func(ctx context.Context, _ *struct{}) (*respx.Body[TenantRegistrationPolicy], error) {
		return respx.OK(ctx, TenantRegistrationPolicy{Enabled: service.registrationEnabled}), nil
	})
	authz.RegisterAuthenticated(registrar, api, huma.Operation{
		OperationID: "organization-register-tenant", Method: http.MethodPost,
		Path: "/tenant-registrations", Summary: "Register a tenant owned by the authenticated principal", Tags: []string{"tenant-registrations"},
		DefaultStatus: http.StatusCreated, Errors: []int{http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Slug string `json:"slug" minLength:"3" maxLength:"63"`
			Name string `json:"name" minLength:"1" maxLength:"128"`
		}
	}) (*respx.Body[Tenant], error) {
		claims, ok := authnext.FromContext(ctx)
		if !service.registrationEnabled || !ok || claims == nil || claims.AccountView != nil || !claims.EmailVerified {
			return nil, huma.Error403Forbidden(respx.Forbidden)
		}
		item, err := service.Create(ctx, CreateTenant{Slug: in.Body.Slug, Name: in.Body.Name, OwnerPrincipalID: claims.PrincipalID, requireVerifiedOwner: true})
		if err != nil {
			return nil, tenantError(err)
		}
		return respx.OK(ctx, item), nil
	})
}
