package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
)

type discoveryDocument struct {
	Issuer                           string   `json:"issuer" format:"uri"`
	AuthorizationEndpoint            string   `json:"authorization_endpoint" format:"uri"`
	TokenEndpoint                    string   `json:"token_endpoint" format:"uri"`
	JWKSURI                          string   `json:"jwks_uri" format:"uri"`
	UserInfoEndpoint                 string   `json:"userinfo_endpoint" format:"uri"`
	RevocationEndpoint               string   `json:"revocation_endpoint" format:"uri"`
	IntrospectionEndpoint            string   `json:"introspection_endpoint" format:"uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	GrantTypesSupported              []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported    []string `json:"code_challenge_methods_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
}

type jsonWebKey struct {
	KeyType string `json:"kty"`
	Curve   string `json:"crv"`
	Use     string `json:"use"`
	Alg     string `json:"alg"`
	KeyID   string `json:"kid"`
	X       string `json:"x"`
}

type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

type discoveryOutput struct{ Body discoveryDocument }
type jwksOutput struct{ Body jwksDocument }

type authorizeInput struct {
	ClientID            string `query:"client_id" required:"true" maxLength:"19" pattern:"^[1-9][0-9]*$"`
	RedirectURI         string `query:"redirect_uri" required:"true" maxLength:"2048"`
	ResponseType        string `query:"response_type" required:"true" enum:"code"`
	Scope               string `query:"scope" maxLength:"2048"`
	State               string `query:"state" maxLength:"1024"`
	CodeChallenge       string `query:"code_challenge" required:"true" minLength:"43" maxLength:"43"`
	CodeChallengeMethod string `query:"code_challenge_method" required:"true" enum:"S256"`
	Nonce               string `query:"nonce" maxLength:"1024"`
	Prompt              string `query:"prompt" enum:"consent,none"`
	Cookie              string `header:"Cookie" hidden:"true"`
}

type redirectOutput struct {
	Status   int    `status:""`
	Location string `header:"Location"`
}

type formInput struct {
	Authorization string `header:"Authorization" hidden:"true"`
	RawBody       []byte `contentType:"application/x-www-form-urlencoded"`
}

type tokenOutput struct {
	Status int `status:""`
	Body   TokenResponse
}

type revokeOutput struct {
	Status int `status:""`
}

type userInfoInput struct {
	Authorization string `header:"Authorization"`
}

type userInfoDocument struct {
	Subject           string  `json:"sub"`
	PreferredUsername *string `json:"preferred_username,omitempty"`
	Email             *string `json:"email,omitempty"`
	EmailVerified     *bool   `json:"email_verified,omitempty"`
}

type introspectionDocument struct {
	Active   bool     `json:"active"`
	Subject  string   `json:"sub,omitempty"`
	Issuer   string   `json:"iss,omitempty"`
	Audience []string `json:"aud,omitempty"`
	Expires  int64    `json:"exp,omitempty"`
}

type userInfoOutput struct{ Body userInfoDocument }
type introspectOutput struct{ Body introspectionDocument }

type consentInput struct {
	Challenge string `query:"consent_challenge" required:"true" minLength:"43" maxLength:"43"`
	Cookie    string `header:"Cookie" hidden:"true"`
}

// consentDecisionInput carries the decision as a form post so the built-in
// screen is a plain HTML form with no script of its own. Origin and Cookie are
// what authn.ValidateCSRF needs: the decision is a cookie-authenticated state
// change, which is exactly the shape CSRF targets.
type consentDecisionInput struct {
	Origin        string `header:"Origin" hidden:"true"`
	Cookie        string `header:"Cookie" hidden:"true"`
	Authorization string `header:"Authorization" hidden:"true"`
	RawBody       []byte `contentType:"application/x-www-form-urlencoded"`
}

type consentForm struct {
	Challenge string `json:"consent_challenge" minLength:"43" maxLength:"43"`
	Decision  string `json:"decision" enum:"allow,deny"`
}

// consentPageOutput writes the rendered screen verbatim; huma copies a []byte
// body straight to the wire instead of encoding it.
type consentPageOutput struct {
	Status       int    `status:""`
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

type consentRequestOutput struct{ Body ConsentRequest }

type tokenForm struct {
	GrantType    string `json:"grant_type" enum:"authorization_code,refresh_token,client_credentials"`
	ClientID     string `json:"client_id,omitempty" maxLength:"19" pattern:"^[1-9][0-9]*$"`
	ClientSecret string `json:"client_secret,omitempty" maxLength:"1024"`
	Code         string `json:"code,omitempty" maxLength:"1024"`
	RedirectURI  string `json:"redirect_uri,omitempty" maxLength:"2048"`
	CodeVerifier string `json:"code_verifier,omitempty" minLength:"43" maxLength:"128"`
	RefreshToken string `json:"refresh_token,omitempty" maxLength:"2048"`
	Scope        string `json:"scope,omitempty" maxLength:"2048"`
}

type revokeForm struct {
	Token        string `json:"token"`
	Hint         string `json:"token_type_hint,omitempty" enum:"refresh_token"`
	ClientID     string `json:"client_id,omitempty" maxLength:"19" pattern:"^[1-9][0-9]*$"`
	ClientSecret string `json:"client_secret,omitempty" maxLength:"1024"`
}

type introspectionForm struct {
	Token string `json:"token"`
	Hint  string `json:"token_type_hint,omitempty"`
}

type oauthGrantType string

func (oauthGrantType) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Enum: []any{"authorization_code", "refresh_token", "client_credentials"}}
}

type oauthRedirectURI string

func (oauthRedirectURI) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "uri", Pattern: `^https?://[^#]+$`}
}

type oauthScope string

func (oauthScope) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^\S+$`}
}

// protocolError intentionally does not use the platform response envelope.
// OAuth clients require the RFC protocol shape on protocol endpoints.
type protocolError struct {
	Status      int    `json:"-"`
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func (e *protocolError) Error() string  { return e.Code }
func (e *protocolError) GetStatus() int { return e.Status }

// clientListInput bounds the listing by default: a tenant's client registry is
// open-ended, and an omitted limit used to mean "every row".
type clientListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type clientCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     struct {
		Name         string             `json:"name" minLength:"1" maxLength:"128"`
		RedirectURIs []oauthRedirectURI `json:"redirect_uris,omitempty" maxItems:"32"`
		GrantTypes   []oauthGrantType   `json:"grant_types" minItems:"1" maxItems:"3"`
		Scopes       []oauthScope       `json:"scopes" minItems:"1" maxItems:"128"`
		PublicClient bool               `json:"public_client"`
		ConsentMode  ConsentMode        `json:"consent_mode,omitempty" enum:"implicit,explicit" default:"implicit"`
	}
}

type clientCreateData struct {
	Client       Client `json:"client"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type clientUpdateInput struct {
	ID   string `path:"client_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body struct {
		Name         string             `json:"name" minLength:"1" maxLength:"128"`
		RedirectURIs []oauthRedirectURI `json:"redirect_uris,omitempty" maxItems:"32"`
		GrantTypes   []oauthGrantType   `json:"grant_types" minItems:"1" maxItems:"3"`
		Scopes       []oauthScope       `json:"scopes" minItems:"1" maxItems:"128"`
		PublicClient bool               `json:"public_client"`
		ConsentMode  ConsentMode        `json:"consent_mode,omitempty" enum:"implicit,explicit" default:"implicit"`
		Status       string             `json:"status" enum:"active,disabled"`
	}
}

type clientIDInput struct {
	ID string `path:"client_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type clientSecretData struct {
	ClientSecret string `json:"client_secret"`
}

type consentListInput struct {
	Cookie string `header:"Cookie"`
}

type consentRevokeInput struct {
	Cookie   string `header:"Cookie"`
	ClientID string `path:"client_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type consentRevokeData struct {
	Revoked bool `json:"revoked"`
}

type clientDeleteData struct {
	Deleted bool `json:"deleted"`
}

// RegisterHTTP publishes the OAuth/OIDC protocol and tenant client-management
// operations.
func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		switch ctx.Operation().OperationID {
		case "oauth-authorize", "oauth-consent-page", "oauth-consent-request", "oauth-consent-decision":
			claims, _ := authnext.FromContext(ctx.Context())
			scheme, raw, bearer := strings.Cut(strings.TrimSpace(ctx.Header("Authorization")), " ")
			if (bearer && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(raw, "avt_")) || (claims != nil && claims.AccountView != nil) {
				_ = huma.WriteErr(api, ctx, http.StatusForbidden, oauthi18n.InvalidRequest)
				return
			}
		}
		next(ctx)
	})
	// GET /oauth/authorize is a public operation that nevertheless reads the
	// browser session cookie, so it is the one route whose documented cookie
	// name cannot be resolved from its own registration. Declaring the scheme
	// here, from the bound authenticator, keeps the published name equal to the
	// one the request path reads no matter which module registers first.
	authz.EnsureSecuritySchemes(api, registrar.SessionCookieName())
	registerProtocolHTTP(api, service)
	registerConsentHTTP(api, service)
	registerClientHTTP(api, service, registrar)
	registerGrantHTTP(api, service, registrar)
}

// registerConsentHTTP publishes the consent screen and the endpoint that
// records its answer. Both stay registered even when an operator points the
// consent path at their own application: that application renders from
// GET /oauth/consent/request and posts its answer back here, so the decision
// is always recorded by the authorization server.
func registerConsentHTTP(api huma.API, service *Service) {
	consentPath := service.ConsentPath()
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-consent-page", Method: http.MethodGet, Path: defaultConsentPath,
		Summary:  "Render the consent screen for a parked authorization request",
		Security: []map[string][]string{{authz.SessionScheme: {}}}, Tags: []string{"oauth"},
	}, func(ctx context.Context, in *consentInput) (*consentPageOutput, error) {
		page, err := service.RenderConsent(ctx, in.Cookie, in.Challenge)
		if err != nil {
			return nil, consentError(ctx, err)
		}
		return &consentPageOutput{
			Status: http.StatusOK, ContentType: "text/html; charset=utf-8",
			// The screen names a client and a set of scopes for one subject,
			// so it must not be reused from a cache by anyone else.
			CacheControl: "no-store", Body: page,
		}, nil
	})
	documentConsentPage(api)
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-consent-request", Method: http.MethodGet, Path: defaultConsentPath + "/request",
		Summary:  "Read a parked authorization request for an external consent screen",
		Security: []map[string][]string{{authz.SessionScheme: {}}}, Tags: []string{"oauth"},
	}, func(ctx context.Context, in *consentInput) (*consentRequestOutput, error) {
		request, err := service.PendingConsent(ctx, in.Cookie, in.Challenge)
		if err != nil {
			return nil, consentError(ctx, err)
		}
		return &consentRequestOutput{Body: request}, nil
	})
	documentOAuthErrors(api, api.OpenAPI().Paths[defaultConsentPath+"/request"].Get,
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError)
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-consent-decision", Method: http.MethodPost, Path: defaultConsentPath,
		Summary: "Record a consent decision and return to the client", DefaultStatus: http.StatusFound,
		Security: []map[string][]string{{authz.SessionScheme: {}}}, Tags: []string{"oauth"}, SkipValidateParams: true,
	}, func(ctx context.Context, in *consentDecisionInput) (*redirectOutput, error) {
		if err := service.authn.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie, in.Authorization); err != nil {
			return nil, oauthError(ctx, http.StatusForbidden, "invalid_request")
		}
		form, err := url.ParseQuery(string(in.RawBody))
		if err != nil {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		decision := form.Get("decision")
		if decision != "allow" && decision != "deny" {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		location, err := service.ResolveConsent(ctx, in.Cookie, form.Get("consent_challenge"), decision == "allow")
		if err != nil {
			return nil, consentError(ctx, err)
		}
		return &redirectOutput{Status: http.StatusFound, Location: location}, nil
	})
	documentOAuthForm[consentForm](api, defaultConsentPath)
	documentOAuthErrors(api, api.OpenAPI().Paths[defaultConsentPath].Post, http.StatusForbidden)
	if consentPath != defaultConsentPath {
		// The built-in screen is still reachable, but the authorization
		// endpoint no longer sends anyone to it, so say so in the document
		// rather than letting the description quietly lie.
		api.OpenAPI().Paths[defaultConsentPath].Get.Description =
			"Superseded by the configured consent screen at " + consentPath + "."
	}
}

// consentError keeps a failed consent lookup indistinguishable from a failed
// authentication. Telling the two apart would let a caller probe which
// challenges exist.
func consentError(ctx context.Context, err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, authnext.ErrInvalidCredentials) {
		return oauthError(ctx, http.StatusBadRequest, "invalid_request")
	}
	return oauthError(ctx, http.StatusInternalServerError, "server_error")
}

// documentConsentPage replaces the generated JSON response with the HTML the
// screen actually returns.
func documentConsentPage(api huma.API) {
	operation := api.OpenAPI().Paths[defaultConsentPath].Get
	operation.Responses["200"] = &huma.Response{
		Description: "Consent screen",
		Content:     map[string]*huma.MediaType{"text/html": {Schema: &huma.Schema{Type: huma.TypeString}}},
	}
	documentOAuthErrors(api, operation, http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError)
}

func registerProtocolHTTP(api huma.API, service *Service) {
	issuer := service.authn.Issuer()
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-discovery", Method: http.MethodGet,
		Path: "/.well-known/openid-configuration", Summary: "OpenID Connect discovery metadata",
		Tags: []string{"oauth"},
	}, func(context.Context, *struct{}) (*discoveryOutput, error) {
		return &discoveryOutput{Body: discoveryDocument{
			Issuer: issuer, AuthorizationEndpoint: issuer + "/oauth/authorize",
			TokenEndpoint: issuer + "/oauth/token", JWKSURI: issuer + "/.well-known/jwks.json",
			UserInfoEndpoint: issuer + "/oauth/userinfo", RevocationEndpoint: issuer + "/oauth/revoke",
			IntrospectionEndpoint: issuer + "/oauth/introspect", ResponseTypesSupported: []string{"code"},
			GrantTypesSupported:              []string{"authorization_code", "refresh_token", "client_credentials"},
			CodeChallengeMethodsSupported:    []string{"S256"},
			SubjectTypesSupported:            []string{"public"},
			IDTokenSigningAlgValuesSupported: []string{"EdDSA"},
		}}, nil
	})
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-jwks", Method: http.MethodGet, Path: "/.well-known/jwks.json",
		Summary: "JSON Web Key Set", Tags: []string{"oauth"},
	}, func(context.Context, *struct{}) (*jwksOutput, error) {
		document, err := decodeOAuthDocument[jwksDocument](service.authn.JWKS())
		if err != nil {
			return nil, err
		}
		if document.Keys == nil {
			document.Keys = []jsonWebKey{}
		}
		return &jwksOutput{Body: document}, nil
	})
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-authorize", Method: http.MethodGet, Path: "/oauth/authorize",
		Summary: "Start an authorization code flow with PKCE", DefaultStatus: http.StatusFound,
		Security: []map[string][]string{{authz.SessionScheme: {}}}, Tags: []string{"oauth"}, SkipValidateParams: true,
	}, func(ctx context.Context, in *authorizeInput) (*redirectOutput, error) {
		if !validAuthorizeInput(in) {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		location, err := service.Authorize(
			ctx, in.Cookie, in.ClientID, in.RedirectURI, in.ResponseType, in.Scope, in.State,
			in.CodeChallenge, in.CodeChallengeMethod, in.Nonce, in.Prompt,
		)
		if err != nil {
			if errors.Is(err, ErrInvalidRequest) || errors.Is(err, authnext.ErrInvalidCredentials) {
				return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
			}
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		return &redirectOutput{Status: http.StatusFound, Location: location}, nil
	})
	documentOAuthErrors(api, api.OpenAPI().Paths["/oauth/authorize"].Get, http.StatusBadRequest, http.StatusInternalServerError)
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-token", Method: http.MethodPost, Path: "/oauth/token",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitOAuth),
		Summary:  "Exchange a grant for OAuth tokens", SkipValidateParams: true,
		Security: []map[string][]string{{authz.ClientBasicScheme: {}}, {}}, Tags: []string{"oauth"},
	}, func(ctx context.Context, in *formInput) (*tokenOutput, error) {
		form, err := url.ParseQuery(string(in.RawBody))
		if err != nil {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		if !containsWord("authorization_code refresh_token client_credentials", form.Get("grant_type")) {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		response, err := service.Token(ctx, form, in.Authorization)
		if err != nil {
			if errors.Is(err, errInvalidClient) {
				return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_client")
			}
			if errors.Is(err, ErrInvalidRequest) {
				return nil, oauthError(ctx, http.StatusBadRequest, "invalid_grant")
			}
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		return &tokenOutput{Status: http.StatusOK, Body: response}, nil
	})
	documentOAuthForm[tokenForm](api, "/oauth/token")
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-revoke", Method: http.MethodPost, Path: "/oauth/revoke",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitOAuth),
		Summary:  "Revoke an OAuth refresh-token family", SkipValidateParams: true,
		Security: []map[string][]string{{authz.ClientBasicScheme: {}}, {}}, Tags: []string{"oauth"}, DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *formInput) (*revokeOutput, error) {
		form, err := url.ParseQuery(string(in.RawBody))
		if err != nil {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		if form.Get("token") == "" {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		if hint := form.Get("token_type_hint"); hint != "" && hint != "refresh_token" {
			return nil, oauthError(ctx, http.StatusBadRequest, "unsupported_token_type")
		}
		if err := service.Revoke(ctx, form, in.Authorization); err != nil {
			if errors.Is(err, ErrInvalidRequest) {
				return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_client")
			}
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		return &revokeOutput{Status: http.StatusOK}, nil
	})
	documentOAuthForm[revokeForm](api, "/oauth/revoke")
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-introspect", Method: http.MethodPost, Path: "/oauth/introspect",
		Metadata: authnext.RateLimitMetadata(authnext.RateLimitOAuth),
		Summary:  "Inspect an OAuth token", Security: authz.ClientSecurity(), Tags: []string{"oauth"}, SkipValidateParams: true,
	}, func(ctx context.Context, in *formInput) (*introspectOutput, error) {
		form, err := url.ParseQuery(string(in.RawBody))
		if err != nil {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		if form.Get("token") == "" {
			return nil, oauthError(ctx, http.StatusBadRequest, "invalid_request")
		}
		if !hasBasicAuthorization(in.Authorization) {
			return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_client")
		}
		result, err := service.Introspect(ctx, form, in.Authorization)
		if err != nil {
			if errors.Is(err, ErrInvalidRequest) {
				return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_client")
			}
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		document, err := decodeOAuthDocument[introspectionDocument](result)
		if err != nil {
			return nil, oauthError(ctx, http.StatusInternalServerError, "server_error")
		}
		return &introspectOutput{Body: document}, nil
	})
	documentOAuthForm[introspectionForm](api, "/oauth/introspect")
	// The shared request limiter answers these three public endpoints directly,
	// so their contract has to carry its refusals even when no shared storage is
	// configured. It replies with an RFC 6749 error object rather than the
	// platform envelope, which is why the schema is the protocol one.
	for _, limited := range []string{"/oauth/token", "/oauth/revoke", "/oauth/introspect"} {
		documentOAuthErrors(api, api.OpenAPI().Paths[limited].Post,
			http.StatusTooManyRequests, http.StatusServiceUnavailable)
	}
	authz.RegisterPublic(api, huma.Operation{
		OperationID: "oauth-userinfo", Method: http.MethodGet, Path: "/oauth/userinfo",
		Summary:  "Return OpenID Connect claims for an access token",
		Security: authz.BearerSecurity(), Tags: []string{"oauth"},
	}, func(ctx context.Context, in *userInfoInput) (*userInfoOutput, error) {
		claims, err := service.authn.Authenticate(ctx, in.Authorization, "")
		if err != nil || claims == nil || claims.AccountView != nil || claims.SubjectType != authnext.SubjectTypePrincipal {
			return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_token")
		}
		if !claimHasScope(claims, "openid") {
			return nil, oauthError(ctx, http.StatusForbidden, "insufficient_scope")
		}
		scope, _ := claims.Raw["scope"].(string)
		profile, err := service.authn.ProjectProfile(ctx, claims, scope)
		if err != nil {
			if errors.Is(err, authnext.ErrInvalidSession) {
				return nil, oauthError(ctx, http.StatusUnauthorized, "invalid_token")
			}
			if errors.Is(err, authz.ErrFieldDenied) {
				return nil, oauthError(ctx, http.StatusForbidden, "access_denied")
			}
			return nil, oauthError(ctx, http.StatusServiceUnavailable, "server_error")
		}
		return &userInfoOutput{Body: userInfoDocument{
			Subject: claims.Subject, PreferredUsername: profile.PreferredUsername,
			Email: profile.Email, EmailVerified: profile.EmailVerified,
		}}, nil
	})
	documentOAuthErrors(api, api.OpenAPI().Paths["/oauth/userinfo"].Get, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable)
}

func registerClientHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-list-clients", Method: http.MethodGet, Path: "/oauth-clients",
		Summary: "List tenant OAuth clients", Tags: []string{"applications"},
	}, authz.Guard{Resource: "oauth_client", Verb: "view"}, func(ctx context.Context, in *clientListInput) (*respx.Body[[]Client], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		clients, total, err := service.ListClientsPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, oauthClientError(err, false)
		}
		return respx.List(ctx, clients, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(clients), Total: total}), nil
	})
	authz.Register(registrar, api, huma.Operation{
		OperationID: "oauth-create-client", Method: http.MethodPost, Path: "/oauth-clients",
		Summary: "Create a tenant OAuth client", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated,
	}, authz.Guard{Resource: "oauth_client", Verb: "create"}, func(ctx context.Context, in *clientCreateInput) (*respx.Body[clientCreateData], error) {
		tenantID, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, err
		}
		client, secret, err := service.CreateClient(
			ctx, tenantID, in.Body.Name, oauthInputStrings(in.Body.RedirectURIs),
			oauthInputStrings(in.Body.GrantTypes), oauthInputStrings(in.Body.Scopes),
			in.Body.PublicClient, in.Body.ConsentMode,
		)
		if err != nil {
			return nil, oauthClientError(err, false)
		}
		return respx.OK(ctx, clientCreateData{Client: client, ClientSecret: secret}), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "oauth-update-client", Method: http.MethodPut, Path: "/oauth-clients/{client_id}",
		Summary: "Replace a tenant OAuth client", Tags: []string{"applications"},
	}, "client_id", service.resolveClientOwner, authz.Guard{Resource: "oauth_client", Verb: "update"}, func(ctx context.Context, in *clientUpdateInput) (*respx.Body[Client], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		tenantID := authnext.TenantIDFromContext(ctx)
		client, err := service.UpdateClient(
			ctx, tenantID, id, in.Body.Name, oauthInputStrings(in.Body.RedirectURIs),
			oauthInputStrings(in.Body.GrantTypes), oauthInputStrings(in.Body.Scopes),
			in.Body.PublicClient, in.Body.ConsentMode, in.Body.Status,
		)
		if err != nil {
			return nil, oauthClientError(err, false)
		}
		return respx.OK(ctx, client), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "oauth-rotate-client-secret", Method: http.MethodPost,
		Path: "/oauth-clients/{client_id}/credentials", Summary: "Create and activate a confidential OAuth client credential",
		Tags: []string{"applications"}, Errors: []int{http.StatusNotFound},
	}, "client_id", service.resolveClientOwner, authz.Guard{Resource: "oauth_client", Verb: "update"}, func(ctx context.Context, in *clientIDInput) (*respx.Body[clientSecretData], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		tenantID := authnext.TenantIDFromContext(ctx)
		secret, err := service.RotateClientSecret(ctx, tenantID, id)
		if err != nil {
			return nil, oauthClientError(err, true)
		}
		return respx.OK(ctx, clientSecretData{ClientSecret: secret}), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "oauth-delete-client", Method: http.MethodDelete, Path: "/oauth-clients/{client_id}",
		Summary: "Delete a tenant OAuth client", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound},
	}, "client_id", service.resolveClientOwner, authz.Guard{Resource: "oauth_client", Verb: "delete"}, func(ctx context.Context, in *clientIDInput) (*respx.Body[clientDeleteData], error) {
		id, err := guid.RequireID(in.ID)
		if err != nil {
			return nil, err
		}
		tenantID := authnext.TenantIDFromContext(ctx)
		if err := service.DeleteClient(ctx, tenantID, id); err != nil {
			return nil, oauthClientError(err, true)
		}
		return respx.OK(ctx, clientDeleteData{Deleted: true}), nil
	})
}

// registerGrantHTTP publishes the standing authorizations a person holds and
// the way to take one back. Both are self-service: they name no tenant, because
// a grant belongs to the principal who gave it rather than to any one tenant,
// and no tenant permission stands between somebody and their own consent.
func registerGrantHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{
		OperationID: "oauth-list-consents", Method: http.MethodGet, Path: "/principals/{principal_id}/oauth-consents",
		Summary: "List the applications the caller has authorized", Tags: []string{"account"},
		Errors: []int{http.StatusUnauthorized, http.StatusInternalServerError},
	}, func(ctx context.Context, in *consentListInput) (*respx.Body[[]GrantedConsent], error) {
		principalID := authnext.PrincipalIDFromContext(ctx)
		consents, err := service.ListConsents(ctx, principalID)
		if err != nil {
			return nil, oauthConsentError(err)
		}
		return respx.OK(ctx, consents), nil
	})
	authz.RegisterPrincipalSessionAuthenticated(registrar, api, huma.Operation{
		OperationID: "oauth-revoke-consent", Method: http.MethodDelete,
		Path: "/principals/{principal_id}/oauth-consents/{client_id}", Tags: []string{"account"},
		Summary: "Withdraw the caller's authorization of one application",
		Errors:  []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError},
	}, func(ctx context.Context, in *consentRevokeInput) (*respx.Body[consentRevokeData], error) {
		principalID := authnext.PrincipalIDFromContext(ctx)
		clientID, err := guid.RequireID(in.ClientID)
		if err != nil {
			return nil, err
		}
		if err := service.RevokeConsent(ctx, principalID, clientID); err != nil {
			return nil, oauthConsentError(err)
		}
		return respx.OK(ctx, consentRevokeData{Revoked: true}), nil
	})
}

// oauthConsentError reports a grant the caller does not hold as absent. There is
// nothing to leak by being specific -- a caller can only ever address their own
// consents here -- but a revocation that finds nothing to revoke is a mistyped
// client, not a server fault.
func oauthConsentError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, ErrInvalidRequest) {
		return huma.Error404NotFound(oauthi18n.ConsentNotFound)
	}
	return huma.Error500InternalServerError(oauthi18n.ServerError)
}

func oauthClientError(err error, notFound bool) error {
	if respx.IsStatusError(err) {
		return err
	}
	if errors.Is(err, ErrInvalidRequest) {
		if notFound {
			return huma.Error404NotFound(oauthi18n.ClientNotFound)
		}
		return huma.Error422UnprocessableEntity(oauthi18n.InvalidClientConfiguration)
	}
	return huma.Error500InternalServerError(oauthi18n.ServerError)
}

func validAuthorizeInput(in *authorizeInput) bool {
	if in == nil || in.ClientID == "" || len(in.ClientID) > 19 || in.RedirectURI == "" || len(in.RedirectURI) > 2048 ||
		in.ResponseType != "code" || len(in.Scope) > 2048 || len(in.State) > 1024 || len(in.Nonce) > 1024 ||
		len(in.CodeChallenge) != 43 || in.CodeChallengeMethod != "S256" {
		return false
	}
	return in.Prompt == "" || in.Prompt == "consent" || in.Prompt == "none"
}

func claimHasScope(claims *authnext.Claims, expected string) bool {
	if claims == nil || claims.Raw == nil {
		return false
	}
	scope, _ := claims.Raw["scope"].(string)
	return containsWord(scope, expected)
}

func hasBasicAuthorization(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) > 6 && strings.EqualFold(value[:6], "Basic ")
}

func oauthInputStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func decodeOAuthDocument[T any](value any) (T, error) {
	var document T
	encoded, err := json.Marshal(value)
	if err != nil {
		return document, err
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		return document, err
	}
	return document, nil
}

func oauthError(ctx context.Context, status int, code string) error {
	key := map[string]string{
		"invalid_request":        oauthi18n.InvalidRequest,
		"invalid_grant":          oauthi18n.InvalidGrant,
		"invalid_client":         oauthi18n.InvalidClient,
		"invalid_token":          oauthi18n.InvalidToken,
		"insufficient_scope":     oauthi18n.InsufficientScope,
		"unsupported_token_type": oauthi18n.UnsupportedTokenType,
		"server_error":           oauthi18n.ServerError,
		"consent_required":       oauthi18n.ConsentRequired,
	}[code]
	return &protocolError{
		Status: status, Code: code, Description: platformi18n.TContext(ctx, key),
	}
}

func documentOAuthForm[T any](api huma.API, path string) {
	operation := api.OpenAPI().Paths[path].Post
	operation.RequestBody = &huma.RequestBody{
		Required: true,
		Content: map[string]*huma.MediaType{
			"application/x-www-form-urlencoded": {
				Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), false, ""),
			},
		},
	}
	documentOAuthErrors(api, operation, http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError)
}

func documentOAuthErrors(api huma.API, operation *huma.Operation, statuses ...int) {
	errorSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[protocolError](), false, "")
	for _, status := range statuses {
		operation.Responses[fmt.Sprintf("%d", status)] = &huma.Response{
			Description: http.StatusText(status),
			Content:     map[string]*huma.MediaType{"application/json": {Schema: errorSchema}},
		}
	}
}
