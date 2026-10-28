package authn

import (
	"context"
	"net/http"

	authni18n "7link.com/platform/internal/iam/authn/i18n"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

func registrationPolicyMenu() core.SystemMenuPage {
	return core.SystemMenuPage{Branch: core.SystemMenuBasicManagement, Menu: core.Menu{
		Label: "System settings", Translations: authni18n.Translations(authni18n.MenuSystemSettings),
		Route: "/system/settings", Icon: "settings", SortOrder: 30,
		PermissionCode: "platform_administer", Status: core.MenuActive,
	}}
}

type registrationPolicyInput struct {
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

func registerRegistrationPolicyHTTP(api huma.API, s *Service, registrar *authz.Registrar) {
	operation := func(id, method, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: "/system/settings/registration", Summary: summary, Tags: []string{"system"}, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable}}
	}
	guard := authz.Guard{Resource: "platform", Verb: "administer"}
	authz.RegisterPlatform(registrar, api, operation("authn-get-registration-policy", http.MethodGet, "Get account registration policy"), guard, func(ctx context.Context, _ *struct{}) (*respx.Body[RegistrationPolicyView], error) {
		value, err := s.RegistrationPolicy(ctx)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
		}
		return respx.OK(ctx, value), nil
	})
	authz.RegisterPlatform(registrar, api, operation("authn-put-registration-policy", http.MethodPut, "Set account registration policy"), guard, func(ctx context.Context, in *registrationPolicyInput) (*respx.Body[RegistrationPolicyView], error) {
		value, err := s.SetRegistrationPolicy(ctx, in.Body.Enabled)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(authni18n.AuthenticationUnavailable)
		}
		return respx.OK(ctx, value), nil
	})
}
