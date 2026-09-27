package federation

import (
	"context"
	"net/http"
	"net/url"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type IdentityBrowserInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	Cookie        string `header:"Cookie"`
	Origin        string `header:"Origin"`
}

type identityOperationInput struct {
	IdentityBrowserInput
	Body struct {
		ProviderID     guid.ID `json:"provider_id"`
		Proof          string  `json:"proof" minLength:"1" maxLength:"256"`
		ExistingLinkID string  `json:"existing_link_id,omitempty" maxLength:"256"`
		ReturnURL      string  `json:"return_url,omitempty" maxLength:"2048"`
	}
}

type identityOperationOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      respx.Envelope[identityOperationLocation]
}

type identityOperationLocation struct {
	AuthorizationURL string `json:"authorization_url"`
}

type identityUnlinkInput struct {
	IdentityBrowserInput
	ID   string `path:"link_id" maxLength:"256"`
	Body struct {
		Proof string `json:"proof" minLength:"1" maxLength:"256"`
	}
}

type identityContinuationInput struct {
	IdentityBrowserInput
	ProviderID string `path:"provider_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	State      string `query:"state" required:"true" minLength:"1" maxLength:"256"`
}

type identityContinuationOutput struct {
	Location  string `header:"Location"`
	SetCookie string `header:"Set-Cookie"`
}

func (s *Service) IdentityOperationClearCookie() string {
	return (&http.Cookie{Name: identityOperationCookie, Path: "/", HttpOnly: true, Secure: s.authn.CookieSecure(), SameSite: http.SameSiteLaxMode, MaxAge: -1}).String()
}

func identityContinuationURL(providerID guid.ID, state string) string {
	return "/identity-providers/" + providerID.String() + "/identity-continuation?" + url.Values{"state": {state}}.Encode()
}

func registerPersonalIdentityHTTP(api huma.API, service *Service) {
	registrar := authz.NewAuthenticationOnlyRegistrar(service.authn)
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "federation-list-personal-identity-links", Method: http.MethodGet, Path: "/principals/{principal_id}/identity-links", Tags: []string{"account"}, Summary: "List the current principal's linked identities"}, func(ctx context.Context, in *IdentityBrowserInput) (*respx.Body[[]PersonalIdentityLink], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		rows, err := service.ListPersonalIdentityLinks(ctx, in.Cookie)
		if err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, rows), nil
	})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "federation-list-personal-identity-providers", Method: http.MethodGet, Path: "/principals/{principal_id}/identity-link-providers", Tags: []string{"account"}, Summary: "List active providers available through current memberships"}, func(ctx context.Context, in *IdentityBrowserInput) (*respx.Body[[]LinkableProvider], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		rows, err := service.ListLinkableProviders(ctx, in.Cookie)
		if err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, rows), nil
	})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "federation-create-personal-identity-transaction", Method: http.MethodPost, Path: "/principals/{principal_id}/identity-link-transactions", Tags: []string{"account"}, Summary: "Start linking or reauthenticate an existing linked identity", Errors: []int{401, 403, 409, 500}}, func(ctx context.Context, in *identityOperationInput) (*identityOperationOutput, error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if err := service.authn.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		// The local issuer is configured by authn; never derive an OAuth redirect
		// from an untrusted Host or forwarded header on this sensitive operation.
		issuer, ok := service.authn.(interface{ Issuer() string })
		if !ok {
			return nil, federationError(ErrUnavailable)
		}
		base, err := url.Parse(issuer.Issuer())
		if err != nil {
			return nil, federationError(err)
		}
		base.Path = callbackURL(in.Body.ProviderID)
		base.RawQuery = ""
		base.Fragment = ""
		start, err := service.StartIdentityOperation(ctx, in.Cookie, in.Body.ProviderID, in.Body.Proof, in.Body.ExistingLinkID, in.Body.ReturnURL, base.String())
		if err != nil {
			return nil, federationError(err)
		}
		return &identityOperationOutput{SetCookie: start.StateCookie, Body: respx.OK(ctx, identityOperationLocation{AuthorizationURL: start.AuthorizationURL}).Body}, nil
	})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{OperationID: "federation-delete-personal-identity-link", Method: http.MethodDelete, Path: "/principals/{principal_id}/identity-links/{link_id}", Tags: []string{"account"}, Summary: "Unlink one identity while retaining a usable login method", Errors: []int{401, 403, 409, 500}}, func(ctx context.Context, in *identityUnlinkInput) (*respx.Body[map[string]bool], error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if err := service.authn.ValidateCSRF(http.MethodDelete, in.Origin, in.Cookie, ""); err != nil {
			return nil, huma.Error403Forbidden("csrf_rejected")
		}
		if err := service.UnlinkPersonalIdentity(ctx, in.Cookie, in.ID, in.Body.Proof); err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, map[string]bool{"unlinked": true}), nil
	})
	continuation := huma.Operation{OperationID: "federation-complete-personal-identity-transaction", Method: http.MethodGet, Path: "/identity-providers/{provider_id}/identity-continuation", Tags: []string{"applications"}, Summary: "Complete a verified callback in its original browser session", DefaultStatus: http.StatusSeeOther, Errors: []int{401, 403, 409, 500}}
	authz.DenyAccountView(&continuation)
	authz.RegisterSessionAuthenticated(registrar, api, continuation, func(ctx context.Context, in *identityContinuationInput) (*identityContinuationOutput, error) {
		if in.Authorization != "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		providerID, err := parseFederationID(in.ProviderID)
		if err != nil {
			return nil, err
		}
		location, err := service.CompleteIdentityOperation(ctx, providerID, in.State, in.Cookie)
		if err != nil {
			return nil, federationError(err)
		}
		return &identityContinuationOutput{Location: location, SetCookie: service.IdentityOperationClearCookie()}, nil
	})
}
