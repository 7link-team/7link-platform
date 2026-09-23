package authn

import (
	"context"
	"encoding/json"
	"net/http"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type reauthenticationBeginInput struct {
	Cookie        string `header:"Cookie"`
	Origin        string `header:"Origin"`
	Authorization string `header:"Authorization" hidden:"true"`
	Body          struct {
		Purpose string `json:"purpose" enum:"identity_link,identity_unlink,personal_token_create,principal_close,tenant_leave"`
		Target  string `json:"target" minLength:"1" maxLength:"512"`
		Method  string `json:"method" enum:"password,passkey,federation"`
	}
}

type reauthenticationVerifyInput struct {
	Cookie        string `header:"Cookie"`
	Origin        string `header:"Origin"`
	Authorization string `header:"Authorization" hidden:"true"`
	Body          struct {
		Proof    string          `json:"proof" minLength:"1" maxLength:"256"`
		Password string          `json:"password,omitempty" maxLength:"1024"`
		Code     string          `json:"code,omitempty" maxLength:"128"`
		Response json.RawMessage `json:"response,omitempty"`
	}
}

func registerReauthenticationHTTP(api huma.API, service *Service) {
	registrar := authz.NewAuthenticationOnlyRegistrar(service)
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-list-reauthentication-methods", Method: http.MethodGet,
		Path: "/me/reauthentication/methods", Summary: "List current local reauthentication methods", Tags: []string{"account"}}, func(ctx context.Context, in *meInput) (*respx.Body[ReauthenticationMethods], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		methods, err := service.AvailableReauthenticationMethods(ctx, in.Cookie)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, methods), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-create-reauthentication-challenge", Method: http.MethodPost,
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Path:     "/me/reauthentication/challenges", Summary: "Begin fresh verification for one sensitive operation", Tags: []string{"account"},
		Security: authz.UserSecurity(), Errors: []int{401, 403, 500}}, func(ctx context.Context, in *reauthenticationBeginInput) (*respx.Body[ReauthenticationOptions], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if err := service.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		options, err := service.BeginReauthentication(ctx, in.Cookie, in.Body.Purpose, in.Body.Target, in.Body.Method, in.Origin)
		if err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, options), nil
	})
	authz.RegisterSessionAuthenticated(registrar, api, huma.Operation{OperationID: "authn-create-reauthentication-verification", Method: http.MethodPost,
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitAuthentication),
		Path:     "/me/reauthentication/verifications", Summary: "Verify a fresh password and MFA or passkey assertion", Tags: []string{"account"},
		Security: authz.UserSecurity(), Errors: []int{401, 403, 500}}, func(ctx context.Context, in *reauthenticationVerifyInput) (*respx.Body[map[string]bool], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if err := service.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := service.VerifyReauthentication(ctx, in.Cookie, in.Body.Proof, in.Body.Password, in.Body.Code, in.Origin, in.Body.Response); err != nil {
			return nil, authenticationError(err)
		}
		return respx.OK(ctx, map[string]bool{"verified": true}), nil
	})
}
