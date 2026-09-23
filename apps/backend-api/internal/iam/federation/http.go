package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	federationi18n "7link.com/platform/internal/iam/federation/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

func parseFederationID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil {
		return 0, huma.Error422UnprocessableEntity("invalid_id")
	}
	return id, nil
}

// providerListInput bounds the listing by default: a tenant's provider registry
// is open-ended, and an omitted limit used to mean "every row".
type providerListInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type providerIDInput struct {
	ProviderID string `path:"provider_id" maxLength:"128"`
}

type providerBody struct {
	Name          string `json:"name" minLength:"1" maxLength:"128"`
	ProviderType  string `json:"provider_type,omitempty" enum:"oidc,saml" default:"oidc"`
	Issuer        string `json:"issuer" minLength:"1" maxLength:"255"`
	ClientID      string `json:"client_id" minLength:"1" maxLength:"128"`
	ClientSecret  string `json:"client_secret,omitempty" maxLength:"2048" doc:"Upstream client secret; an empty value keeps the stored secret on update"`
	Scopes        string `json:"scopes,omitempty" maxLength:"255"`
	AutoProvision bool   `json:"auto_provision,omitempty" default:"true"`
	DefaultRoleID string `json:"default_role_id,omitempty" maxLength:"32" doc:"Role granted to just-in-time provisioned members"`
	Status        string `json:"status,omitempty" enum:"active,disabled" default:"active"`
}

type providerCreateInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     providerBody
}

type providerReplaceInput struct {
	ProviderID string `path:"provider_id" maxLength:"128"`
	Body       providerBody
}

// RegisterHTTP mounts the tenant management API and the public browser login
// flow. The management endpoints are tenant-authorized; the browser endpoints
// are intentionally public because their security is carried by the sealed
// state cookie and PKCE verifier.
func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	registerPersonalIdentityHTTP(api, service)
	registerAdminHTTP(api, service, registrar)
	registerBrowserHTTP(api, service)
	registerSAMLAdminHTTP(api, service, registrar)
	registerSAMLBrowserHTTP(api, service)
}

func registerAdminHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	guard := authz.Guard{Resource: "identity_provider", Verb: "view"}
	authz.Register(registrar, api, huma.Operation{OperationID: "federation-list-identity-providers", Method: http.MethodGet, Path: "/identity-providers", Summary: "List tenant identity providers", Tags: []string{"applications"}}, guard, func(ctx context.Context, in *providerListInput) (*respx.Body[[]Provider], error) {
		tenantID, err := parseFederationID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListProvidersPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, federationError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})
	authz.Register(registrar, api, huma.Operation{OperationID: "federation-create-identity-provider", Method: http.MethodPost, Path: "/identity-providers", Summary: "Create an OIDC identity provider", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}, authz.Guard{Resource: "identity_provider", Verb: "create"}, func(ctx context.Context, in *providerCreateInput) (*respx.Body[Provider], error) {
		tenantID, err := parseFederationID(in.TenantID)
		if err != nil {
			return nil, err
		}
		input, err := providerInputFromBody(in.Body)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateProvider(ctx, tenantID, input)
		if err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "federation-update-identity-provider", Method: http.MethodPut, Path: "/identity-providers/{provider_id}", Summary: "Replace an OIDC identity provider", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}, "provider_id", service.resolveProviderOwner, authz.Guard{Resource: "identity_provider", Verb: "update"}, func(ctx context.Context, in *providerReplaceInput) (*respx.Body[Provider], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		providerID, err := parseFederationID(in.ProviderID)
		if err != nil {
			return nil, err
		}
		input, err := providerInputFromBody(in.Body)
		if err != nil {
			return nil, err
		}
		item, err := service.UpdateProvider(ctx, tenantID, providerID, input)
		if err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, item), nil
	})
	authz.RegisterOwned(registrar, api, huma.Operation{OperationID: "federation-delete-identity-provider", Method: http.MethodDelete, Path: "/identity-providers/{provider_id}", Summary: "Delete an OIDC identity provider and its identity links", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity}}, "provider_id", service.resolveProviderOwner, authz.Guard{Resource: "identity_provider", Verb: "delete"}, func(ctx context.Context, in *providerIDInput) (*respx.Body[map[string]bool], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		providerID, err := parseFederationID(in.ProviderID)
		if err != nil {
			return nil, err
		}
		if err := service.DeleteProvider(ctx, tenantID, providerID); err != nil {
			return nil, federationError(err)
		}
		return respx.OK(ctx, map[string]bool{"deleted": true}), nil
	})
}

// RegisterBrowserREST mounts the public browser endpoints. They are
// unauthenticated by design; the sealed state cookie and PKCE verifier carry
// the flow's security. The raw adapter pattern gives the handlers direct
// access to request metadata and response cookies.
func registerBrowserHTTP(api huma.API, service *Service) {
	start := huma.Operation{
		OperationID: "federation-start-login", Method: http.MethodGet, Path: "/identity-providers/{provider_id}/authorization",
		Summary: "Start an OIDC login for a tenant identity provider", Tags: []string{"applications"}, DefaultStatus: http.StatusFound,
		Parameters: []*huma.Param{
			{Name: "provider_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
			{Name: "return_url", In: "query", Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(2048), Description: "Allowlisted application URL to return to after login"}},
		},
	}
	authz.Public(&start)
	start.Responses = browserResponses(api.OpenAPI().Components.Schemas)
	api.OpenAPI().AddOperation(&start)
	api.Adapter().Handle(&start, func(ctx huma.Context) {
		providerID, err := parseFederationID(ctx.Param("provider_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		result, err := service.StartLogin(ctx.Context(), providerID, ctx.Query("return_url"), requestCallbackURL(ctx, providerID))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		ctx.AppendHeader("Set-Cookie", result.StateCookie)
		ctx.SetHeader("Location", result.AuthorizationURL)
		ctx.SetStatus(http.StatusFound)
	})

	callback := huma.Operation{
		OperationID: "federation-callback-login", Method: http.MethodGet, Path: "/identity-providers/{provider_id}/callback",
		Summary: "Complete the OIDC authorization code flow and set the browser session", Tags: []string{"applications"}, DefaultStatus: http.StatusFound,
		Parameters: []*huma.Param{
			{Name: "provider_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
			{Name: "code", In: "query", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(4096)}},
			{Name: "state", In: "query", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(256)}},
		},
	}
	authz.Public(&callback)
	callback.Responses = browserResponses(api.OpenAPI().Components.Schemas)
	api.OpenAPI().AddOperation(&callback)
	api.Adapter().Handle(&callback, func(ctx huma.Context) {
		providerID, err := parseFederationID(ctx.Param("provider_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		if strings.HasPrefix(ctx.Query("state"), "account.") {
			if err := service.VerifyIdentityOperation(ctx.Context(), providerID, ctx.Query("state"), ctx.Query("code"), "", requestCallbackURL(ctx, providerID)); err != nil {
				writeFederationError(api, ctx, err)
				return
			}
			ctx.SetHeader("Location", identityContinuationURL(providerID, ctx.Query("state")))
			ctx.SetStatus(http.StatusSeeOther)
			return
		}
		result, err := service.CompleteLogin(ctx.Context(), providerID, ctx.Query("code"), ctx.Query("state"), ctx.Header("Cookie"), requestCallbackURL(ctx, providerID))
		if err != nil {
			ctx.AppendHeader("Set-Cookie", service.StateClearCookie())
			writeFederationError(api, ctx, err)
			return
		}
		ctx.AppendHeader("Set-Cookie", service.StateClearCookie())
		ctx.AppendHeader("Set-Cookie", service.authn.SessionCookie(result.SessionToken))
		ctx.SetHeader("Location", result.ReturnURL)
		ctx.SetStatus(http.StatusFound)
	})

	samlCallback := huma.Operation{
		OperationID: "federation-callback-saml-login", Method: http.MethodPost, Path: "/identity-providers/{provider_id}/callback",
		Summary: "Complete the SAML SP-initiated assertion flow and set the browser session", Tags: []string{"applications"}, DefaultStatus: http.StatusFound,
		Parameters: []*huma.Param{
			{Name: "provider_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
		},
	}
	authz.Public(&samlCallback)
	samlCallback.RequestBody = samlFormRequestBody[samlResponseForm](api)
	samlCallback.Responses = browserResponses(api.OpenAPI().Components.Schemas)
	api.OpenAPI().AddOperation(&samlCallback)
	api.Adapter().Handle(&samlCallback, func(ctx huma.Context) {
		providerID, err := parseFederationID(ctx.Param("provider_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		body, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), samlFlateLimit))
		if err != nil {
			writeFederationError(api, ctx, ErrOIDCState)
			return
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			writeFederationError(api, ctx, ErrOIDCState)
			return
		}
		if strings.HasPrefix(values.Get("RelayState"), "account.") {
			if err := service.VerifyIdentityOperation(ctx.Context(), providerID, values.Get("RelayState"), "", values.Get("SAMLResponse"), requestCallbackURL(ctx, providerID)); err != nil {
				writeFederationError(api, ctx, err)
				return
			}
			ctx.SetHeader("Location", identityContinuationURL(providerID, values.Get("RelayState")))
			ctx.SetStatus(http.StatusSeeOther)
			return
		}
		sessionToken, returnURL, err := service.CompleteSAMLLogin(ctx.Context(), providerID, values.Get("SAMLResponse"), ctx.Header("Cookie"), requestCallbackURL(ctx, providerID))
		if err != nil {
			ctx.AppendHeader("Set-Cookie", service.StateClearCookie())
			writeFederationError(api, ctx, err)
			return
		}
		ctx.AppendHeader("Set-Cookie", service.StateClearCookie())
		ctx.AppendHeader("Set-Cookie", service.authn.SessionCookie(sessionToken))
		ctx.SetHeader("Location", returnURL)
		ctx.SetStatus(http.StatusFound)
	})
}

func browserResponses(schemas huma.Registry) map[string]*huma.Response {
	redirect := &huma.Response{
		Description: http.StatusText(http.StatusFound),
		Headers: map[string]*huma.Param{
			"Location":   {Name: "Location", In: "header", Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
			"Set-Cookie": {Name: "Set-Cookie", In: "header", Schema: &huma.Schema{Type: huma.TypeString}},
		},
	}
	errorSchema := errorResponseSchema(schemas)
	return map[string]*huma.Response{
		"302": redirect,
		"303": {Description: http.StatusText(http.StatusSeeOther), Headers: redirect.Headers},
		"400": {Description: "Bad request", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"403": {Description: "Login denied", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"404": {Description: "Provider not found", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"409": {Description: "Provider conflict", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"422": {Description: "Invalid identifier", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"500": {Description: "Federation unavailable", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"503": {Description: "Federation storage unavailable", Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
	}
}

// errorResponseSchema resolves the error component the same way huma's own
// operation registration does: from the concrete type huma.NewError returns.
// The application installs a custom error envelope over that constructor before
// any route registers, so deriving the type here keeps these hand-written
// responses pointing at the same $ref as every huma.Register-generated one.
func errorResponseSchema(registry huma.Registry) *huma.Schema {
	errorType := reflect.TypeOf(huma.NewError(0, ""))
	for errorType.Kind() == reflect.Pointer {
		errorType = errorType.Elem()
	}
	hint := errorType.Name()
	if hint == "" {
		hint = "Error"
	}
	return registry.Schema(errorType, true, hint)
}

func writeFederationError(api huma.API, ctx huma.Context, err error) {
	var mapped huma.StatusError
	if errors.As(err, &mapped) {
		_ = huma.WriteErr(api, ctx, mapped.GetStatus(), mapped.Error())
		return
	}
	if errors.As(federationError(err), &mapped) {
		_ = huma.WriteErr(api, ctx, mapped.GetStatus(), mapped.Error())
		return
	}
	_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, federationi18n.Unavailable)
}

func intPointer(value int) *int { return &value }

func providerInputFromBody(body providerBody) (ProviderInput, error) {
	defaultRoleID, err := parseOptionalFederationID(body.DefaultRoleID)
	if err != nil {
		return ProviderInput{}, err
	}
	return ProviderInput{
		Name: body.Name, ProviderType: body.ProviderType, Issuer: body.Issuer, ClientID: body.ClientID,
		ClientSecret: body.ClientSecret, Scopes: body.Scopes, AutoProvision: body.AutoProvision,
		DefaultRoleID: defaultRoleID, Status: body.Status,
	}, nil
}

func parseOptionalFederationID(value string) (guid.ID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return parseFederationID(value)
}

// requestCallbackURL reconstructs the externally visible callback URL from the
// request so the redirect_uri sent to the IdP always matches this deployment.
func requestCallbackURL(ctx huma.Context, providerID guid.ID) string {
	scheme := "http"
	if proto := strings.TrimSpace(ctx.Header("X-Forwarded-Proto")); proto == "https" {
		scheme = "https"
	} else if ctx.TLS() != nil {
		scheme = "https"
	}
	return scheme + "://" + ctx.Host() + callbackURL(providerID)
}

func federationError(err error) error {
	switch {
	case errors.Is(err, authnext.ErrInvalidCredentials), errors.Is(err, authnext.ErrInvalidSession):
		return huma.Error401Unauthorized(federationi18n.IdentityUnavailable)
	case errors.Is(err, authnext.ErrUnavailable):
		return huma.Error503ServiceUnavailable(federationi18n.Unavailable)
	case errors.Is(err, ErrIdentityLinkConflict):
		return huma.Error409Conflict(federationi18n.IdentityLinkConflict)
	case errors.Is(err, ErrLastLoginMethod):
		return huma.Error409Conflict(federationi18n.LastLoginMethod)
	case errors.Is(err, errReplayUnavailable):
		return huma.Error503ServiceUnavailable(federationi18n.Unavailable)
	case errors.Is(err, ErrInvalidProvider), errors.Is(err, ErrOIDCState):
		return huma.Error400BadRequest(federationi18n.InvalidRequest)
	case errors.Is(err, ErrProviderNotFound):
		return huma.Error404NotFound(federationi18n.ProviderNotFound)
	case errors.Is(err, ErrProviderIssuerExists):
		return huma.Error409Conflict(federationi18n.ProviderIssuerExists)
	case errors.Is(err, ErrProviderRoleMissing):
		return huma.Error409Conflict(federationi18n.ProviderRoleMissing)
	case errors.Is(err, ErrProviderDisabled):
		return huma.Error403Forbidden(federationi18n.ProviderDisabled)
	case errors.Is(err, ErrProvisioningDisabled):
		return huma.Error403Forbidden(federationi18n.ProvisioningDisabled)
	case errors.Is(err, ErrPrincipalInactive):
		return huma.Error403Forbidden(federationi18n.IdentityUnavailable)
	case errors.Is(err, ErrProvisioningUnverified):
		return huma.Error400BadRequest(federationi18n.EmailUnverified)
	case errors.Is(err, ErrOIDCDiscovery), errors.Is(err, ErrOIDCToken), errors.Is(err, ErrOIDCTokenInvalid):
		return huma.Error400BadRequest(federationi18n.OIDCFailed)
	case errors.Is(err, ErrSAMLResponse):
		return huma.Error400BadRequest(federationi18n.SAMLInvalidRequest)
	default:
		return huma.Error500InternalServerError(federationi18n.Unavailable)
	}
}
