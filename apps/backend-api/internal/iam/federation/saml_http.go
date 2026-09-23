package federation

import (
	"context"
	"net/http"
	"reflect"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

// samlSPListInput bounds the listing by default, like providerListInput.
type samlSPListInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type samlSPIDInput struct {
	ServiceProviderID string `path:"sp_id" maxLength:"128"`
}

type samlSPBody struct {
	Name        string `json:"name" minLength:"1" maxLength:"128"`
	EntityID    string `json:"entity_id" minLength:"1" maxLength:"255"`
	MetadataXML string `json:"metadata_xml" minLength:"1" maxLength:"65536"`
	Status      string `json:"status,omitempty" enum:"active,disabled" default:"active"`
}

type samlSPCreateInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     samlSPBody
}

type samlSPReplaceInput struct {
	ServiceProviderID string `path:"sp_id" maxLength:"128"`
	Body              samlSPBody
}

type samlKeyRotateInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
}

type samlRequestForm struct {
	SAMLRequest string `json:"SAMLRequest" maxLength:"65536"`
	RelayState  string `json:"RelayState,omitempty" maxLength:"2048"`
}

type samlResponseForm struct {
	SAMLResponse string `json:"SAMLResponse" maxLength:"65536"`
	RelayState   string `json:"RelayState,omitempty" maxLength:"2048"`
}

func registerSAMLAdminHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	list := huma.Operation{OperationID: "federation-list-saml-service-providers", Method: http.MethodGet, Path: "/saml-service-providers", Summary: "List tenant SAML service providers", Tags: []string{"applications"}}
	authz.Register(registrar, api, list, authz.Guard{Resource: "identity_provider", Verb: "view"}, func(ctx context.Context, in *samlSPListInput) (*respx.Body[[]SAMLServiceProvider], error) {
		tenantID, err := parseFederationID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListSAMLServiceProvidersPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, samlError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})

	create := huma.Operation{OperationID: "federation-create-saml-service-provider", Method: http.MethodPost, Path: "/saml-service-providers", Summary: "Register a SAML service provider from its metadata", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}
	authz.Register(registrar, api, create, authz.Guard{Resource: "identity_provider", Verb: "create"}, func(ctx context.Context, in *samlSPCreateInput) (*respx.Body[SAMLServiceProvider], error) {
		tenantID, err := parseFederationID(in.TenantID)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateSAMLServiceProvider(ctx, tenantID, samlSPInputFromBody(in.Body))
		if err != nil {
			return nil, samlError(err)
		}
		return respx.OK(ctx, item), nil
	})

	replace := huma.Operation{OperationID: "federation-update-saml-service-provider", Method: http.MethodPut, Path: "/saml-service-providers/{sp_id}", Summary: "Replace a SAML service provider", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity}}
	authz.RegisterOwned(registrar, api, replace, "sp_id", service.resolveSAMLServiceProviderOwner, authz.Guard{Resource: "identity_provider", Verb: "update"}, func(ctx context.Context, in *samlSPReplaceInput) (*respx.Body[SAMLServiceProvider], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		spID, err := parseFederationID(in.ServiceProviderID)
		if err != nil {
			return nil, err
		}
		item, err := service.UpdateSAMLServiceProvider(ctx, tenantID, spID, samlSPInputFromBody(in.Body))
		if err != nil {
			return nil, samlError(err)
		}
		return respx.OK(ctx, item), nil
	})

	remove := huma.Operation{OperationID: "federation-delete-saml-service-provider", Method: http.MethodDelete, Path: "/saml-service-providers/{sp_id}", Summary: "Delete a SAML service provider", Tags: []string{"applications"}, Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity}}
	authz.RegisterOwned(registrar, api, remove, "sp_id", service.resolveSAMLServiceProviderOwner, authz.Guard{Resource: "identity_provider", Verb: "delete"}, func(ctx context.Context, in *samlSPIDInput) (*respx.Body[map[string]bool], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		spID, err := parseFederationID(in.ServiceProviderID)
		if err != nil {
			return nil, err
		}
		if err := service.DeleteSAMLServiceProvider(ctx, tenantID, spID); err != nil {
			return nil, samlError(err)
		}
		return respx.OK(ctx, map[string]bool{"deleted": true}), nil
	})

	rotate := huma.Operation{OperationID: "federation-rotate-saml-signing-key", Method: http.MethodPost, Path: "/saml-signing-keys", Summary: "Rotate the SAML identity provider signing key", Tags: []string{"applications"}, DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict}}
	authz.Register(registrar, api, rotate, authz.Guard{Resource: "identity_provider", Verb: "update"}, func(ctx context.Context, _ *samlKeyRotateInput) (*respx.Body[SAMLKeyInfo], error) {
		item, err := service.RotateSAMLSigningKey(ctx)
		if err != nil {
			return nil, samlError(err)
		}
		return respx.OK(ctx, item), nil
	})
}

// RegisterSAMLBrowserREST mounts the public SAML endpoints. The raw adapter
// pattern gives the handlers direct access to request metadata, cookies, and
// the response body so protocol responses render as HTML.
func registerSAMLBrowserHTTP(api huma.API, service *Service) {
	sso := huma.Operation{
		OperationID: "federation-saml-sso", Method: http.MethodGet, Path: samlSSORoute,
		Summary: "Process a SAML AuthnRequest and return a signed response", Tags: []string{"saml"},
		Parameters: []*huma.Param{
			{Name: "tenant_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
			{Name: "SAMLRequest", In: "query", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(65536)}},
			{Name: "RelayState", In: "query", Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(2048)}},
		},
	}
	authz.Public(&sso)
	sso.Responses = samlProtocolResponses(api, map[string]*huma.MediaType{
		"text/html": {Schema: &huma.Schema{Type: huma.TypeString}},
	}, true)
	api.OpenAPI().AddOperation(&sso)
	api.Adapter().Handle(&sso, func(ctx huma.Context) {
		tenantID, err := parseFederationID(ctx.Param("tenant_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		service.ServeSAMLSSO(api, ctx, tenantID)
	})

	ssoPost := huma.Operation{
		OperationID: "federation-saml-sso-post", Method: http.MethodPost, Path: samlSSORoute,
		Summary: "Process a SAML AuthnRequest (HTTP-POST binding)", Tags: []string{"saml"},
		Parameters: []*huma.Param{
			{Name: "tenant_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
		},
	}
	authz.Public(&ssoPost)
	ssoPost.RequestBody = samlFormRequestBody[samlRequestForm](api)
	ssoPost.Responses = samlProtocolResponses(api, map[string]*huma.MediaType{
		"text/html": {Schema: &huma.Schema{Type: huma.TypeString}},
	}, true)
	api.OpenAPI().AddOperation(&ssoPost)
	api.Adapter().Handle(&ssoPost, func(ctx huma.Context) {
		tenantID, err := parseFederationID(ctx.Param("tenant_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		service.ServeSAMLSSO(api, ctx, tenantID)
	})

	slo := huma.Operation{
		OperationID: "federation-saml-slo", Method: http.MethodGet, Path: samlSLORoute,
		Summary: "Process a SAML LogoutRequest and revoke the browser session", Tags: []string{"saml"},
		Parameters: []*huma.Param{
			{Name: "tenant_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
			{Name: "SAMLRequest", In: "query", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(65536)}},
			{Name: "RelayState", In: "query", Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(2048)}},
		},
	}
	authz.Public(&slo)
	slo.Responses = samlProtocolResponses(api, map[string]*huma.MediaType{
		"text/html":  {Schema: &huma.Schema{Type: huma.TypeString}},
		"text/plain": {Schema: &huma.Schema{Type: huma.TypeString}},
	}, false)
	api.OpenAPI().AddOperation(&slo)
	api.Adapter().Handle(&slo, func(ctx huma.Context) {
		tenantID, err := parseFederationID(ctx.Param("tenant_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		service.ServeSAMLSLO(api, ctx, tenantID)
	})

	sloPost := huma.Operation{
		OperationID: "federation-saml-slo-post", Method: http.MethodPost, Path: samlSLORoute,
		Summary: "Process a SAML LogoutRequest (HTTP-POST binding)", Tags: []string{"saml"},
		Parameters: []*huma.Param{
			{Name: "tenant_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
		},
	}
	authz.Public(&sloPost)
	sloPost.RequestBody = samlFormRequestBody[samlRequestForm](api)
	sloPost.Responses = samlProtocolResponses(api, map[string]*huma.MediaType{
		"text/html":  {Schema: &huma.Schema{Type: huma.TypeString}},
		"text/plain": {Schema: &huma.Schema{Type: huma.TypeString}},
	}, false)
	api.OpenAPI().AddOperation(&sloPost)
	api.Adapter().Handle(&sloPost, func(ctx huma.Context) {
		tenantID, err := parseFederationID(ctx.Param("tenant_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		service.ServeSAMLSLO(api, ctx, tenantID)
	})

	metadata := huma.Operation{
		OperationID: "federation-saml-metadata", Method: http.MethodGet, Path: samlMetadataRoute,
		Summary: "Return the tenant SAML identity provider metadata", Tags: []string{"saml"},
		Parameters: []*huma.Param{
			{Name: "tenant_id", In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, MaxLength: intPointer(128)}},
		},
	}
	authz.Public(&metadata)
	metadata.Responses = samlProtocolResponses(api, map[string]*huma.MediaType{
		"application/samlmetadata+xml": {Schema: &huma.Schema{Type: huma.TypeString}},
	}, false)
	api.OpenAPI().AddOperation(&metadata)
	api.Adapter().Handle(&metadata, func(ctx huma.Context) {
		tenantID, err := parseFederationID(ctx.Param("tenant_id"))
		if err != nil {
			writeFederationError(api, ctx, err)
			return
		}
		service.ServeSAMLMetadata(api, ctx, tenantID)
	})
}

func samlFormRequestBody[T any](api huma.API) *huma.RequestBody {
	return &huma.RequestBody{
		Required: true,
		Content: map[string]*huma.MediaType{
			"application/x-www-form-urlencoded": {
				Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), false, ""),
			},
		},
	}
}

func samlProtocolResponses(api huma.API, success map[string]*huma.MediaType, redirect bool) map[string]*huma.Response {
	errorSchema := errorResponseSchema(api.OpenAPI().Components.Schemas)
	responses := map[string]*huma.Response{
		"200": {Description: http.StatusText(http.StatusOK), Content: success},
		"400": {Description: http.StatusText(http.StatusBadRequest), Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"422": {Description: http.StatusText(http.StatusUnprocessableEntity), Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"500": {Description: http.StatusText(http.StatusInternalServerError), Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
		"503": {Description: http.StatusText(http.StatusServiceUnavailable), Content: map[string]*huma.MediaType{"application/json": {Schema: errorSchema}}},
	}
	if redirect {
		responses["302"] = &huma.Response{
			Description: http.StatusText(http.StatusFound),
			Headers: map[string]*huma.Param{
				"Location": {Name: "Location", In: "header", Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
			},
		}
	}
	return responses
}

func samlSPInputFromBody(body samlSPBody) SAMLServiceProviderInput {
	return SAMLServiceProviderInput(body)
}
