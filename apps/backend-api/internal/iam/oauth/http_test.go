package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const oauthRouteManifest = `
oauth-authorize|GET|/oauth/authorize
oauth-consent-decision|POST|/oauth/consent
oauth-consent-page|GET|/oauth/consent
oauth-consent-request|GET|/oauth/consent/request
oauth-create-client|POST|/oauth-clients
oauth-delete-client|DELETE|/oauth-clients/{client_id}
oauth-discovery|GET|/.well-known/openid-configuration
oauth-introspect|POST|/oauth/introspect
oauth-jwks|GET|/.well-known/jwks.json
oauth-list-clients|GET|/oauth-clients
oauth-list-consents|GET|/me/oauth-consents
oauth-revoke-consent|DELETE|/me/oauth-consents/{client_id}
oauth-revoke|POST|/oauth/revoke
oauth-rotate-client-secret|POST|/oauth-clients/{client_id}/credentials
oauth-token|POST|/oauth/token
oauth-update-client|PUT|/oauth-clients/{client_id}
oauth-userinfo|GET|/oauth/userinfo`

var oauthGuards = map[string]string{
	"oauth-list-clients":         "oauth_client_view",
	"oauth-create-client":        "oauth_client_create",
	"oauth-update-client":        "oauth_client_update",
	"oauth-rotate-client-secret": "oauth_client_update",
	"oauth-delete-client":        "oauth_client_delete",
}

type oauthHTTPHarness struct {
	*oauthHarness
	api    huma.API
	router http.Handler
}

func initializeOAuthLocales() error {
	if err := platformi18n.NewModule(nil, platformi18n.Base).Initialize(context.Background()); err != nil {
		return err
	}
	if err := respx.NewModule(nil).Initialize(context.Background()); err != nil {
		return err
	}
	return oauthi18n.Register()
}

func newOAuthHTTPHarness(t *testing.T) oauthHTTPHarness {
	t.Helper()
	require.NoError(t, initializeOAuthLocales())
	harness := newOAuthHarness(t, "sl_")
	harness.web.mu.Lock()
	harness.web.tokens["admin"] = &authnext.Claims{
		Issuer: "https://iam.example", Subject: "101", SubjectType: authnext.SubjectTypePrincipal,
		PrincipalID: 101,
	}
	harness.web.mu.Unlock()
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("oauth-test", "1.0.0"))
	RegisterHTTP(api, harness.service, oauthTestRegistrar(harness.web))
	return oauthHTTPHarness{oauthHarness: harness, api: api, router: router}
}

func (h oauthHTTPHarness) request(
	method, target, contentType string,
	body io.Reader,
	headers map[string]string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	h.router.ServeHTTP(recorder, request)
	return recorder
}

func (h oauthHTTPHarness) form(method, target string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	return h.request(method, target, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), headers)
}

func (h oauthHTTPHarness) json(method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return h.request(method, target, "application/json", bytes.NewReader(data), headers)
}

func oauthPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete, http.MethodHead: item.Head,
		http.MethodOptions: item.Options, http.MethodTrace: item.Trace,
	}
}

func oauthOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 15)
	for path, item := range spec.Paths {
		for method, operation := range oauthPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func decodeOAuthEnvelope[T any](t *testing.T, response *httptest.ResponseRecorder) respx.Envelope[T] {
	t.Helper()
	var envelope respx.Envelope[T]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope), response.Body.String())
	return envelope
}

func decodeProtocolError(t *testing.T, response *httptest.ResponseRecorder) protocolError {
	t.Helper()
	assert.Contains(t, response.Header().Get("Content-Type"), "application/json")
	var body protocolError
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	return body
}

func managementHeaders(tenant guid.ID) map[string]string {
	return map[string]string{
		"Authorization":    "Bearer admin",
		authz.TenantHeader: tenant.String(),
	}
}

func TestOAuthRouteManifestMatchesGeneratedOpenAPI(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	want := strings.Fields(oauthRouteManifest)
	got := oauthOperationManifest(harness.api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 17)
	require.NoError(t, authz.ValidateOperations(harness.api, authz.DefaultRegistry()))
}

// TestConsentSelfServiceRoutesActOnlyForTheirCaller is the HTTP half of the
// self-service contract. The route names no subject, so the session is the only
// thing that can decide whose grants these are -- and an unauthenticated caller
// must not reach the handler at all.
func TestConsentSelfServiceRoutesActOnlyForTheirCaller(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	_, err := exchangeCode(t, harness.oauthHarness, client, authorizeCode(t, harness.oauthHarness, client))
	require.NoError(t, err)
	caller := map[string]string{"Cookie": "session"}

	// No tenant header anywhere: a person's grants are not a tenant's business,
	// and requiring one would hide the grants they most need to see.
	response := harness.request(http.MethodGet, "/me/oauth-consents", "", nil, caller)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	listed := decodeOAuthEnvelope[[]GrantedConsent](t, response).Data
	require.Len(t, listed, 1)
	assert.Equal(t, client.ID, listed[0].ClientID)
	assert.Equal(t, "Browser", listed[0].ClientName)

	for name, target := range map[string]string{
		"listing":    "/me/oauth-consents",
		"revocation": "/me/oauth-consents/" + client.ID,
	} {
		t.Run("an anonymous caller is refused before "+name, func(t *testing.T) {
			method := http.MethodGet
			if strings.Contains(target, client.ID) {
				method = http.MethodDelete
			}
			refused := harness.request(method, target, "", nil, nil)
			assert.Equal(t, http.StatusUnauthorized, refused.Code, refused.Body.String())
		})

		t.Run("a bearer caller is refused before "+name, func(t *testing.T) {
			// A self-service operation is registered session-only, so a bearer
			// credential is not a second way to be the person whose grants
			// these are. Without this the two routes would be the only
			// self-service operations accepting one.
			method := http.MethodGet
			if strings.Contains(target, client.ID) {
				method = http.MethodDelete
			}
			refused := harness.request(method, target, "", nil, map[string]string{"Authorization": "Bearer admin"})
			assert.Equal(t, http.StatusUnauthorized, refused.Code, refused.Body.String())
		})
	}

	revoked := harness.request(http.MethodDelete, "/me/oauth-consents/"+client.ID, "", nil, caller)
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	assert.True(t, decodeOAuthEnvelope[consentRevokeData](t, revoked).Data.Revoked)

	// The second one is what a double-click produces, and 404 is the honest
	// answer: there is no grant by that name any more.
	again := harness.request(http.MethodDelete, "/me/oauth-consents/"+client.ID, "", nil, caller)
	assert.Equal(t, http.StatusNotFound, again.Code, again.Body.String())

	empty := harness.request(http.MethodGet, "/me/oauth-consents", "", nil, caller)
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	assert.Contains(t, empty.Body.String(), `"data":[]`,
		"a caller with no grants reads an empty array, not null")
}

// TestConsentRevocationRefusesAnIdentifierThatIsNotOne keeps a malformed client
// out of the query rather than letting it arrive as a zero.
func TestConsentRevocationRefusesAnIdentifierThatIsNotOne(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	caller := map[string]string{"Cookie": "session"}
	for name, id := range map[string]string{"a zero": "0", "a word": "latest", "a negative": "-1"} {
		t.Run(name, func(t *testing.T) {
			response := harness.request(http.MethodDelete, "/me/oauth-consents/"+id, "", nil, caller)
			assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		})
	}
}

func TestOAuthOpenAPISecurityGuardsParametersAndForms(t *testing.T) {
	spec := newOAuthHTTPHarness(t).api.OpenAPI()
	for path, item := range spec.Paths {
		for method, operation := range oauthPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			assert.Equal(t, []string{"oauth"}, operation.Tags, location)
			if guard, guarded := oauthGuards[operation.OperationID]; guarded {
				assert.Equal(t, guard, operation.Metadata["authz.guard"], location)
				assert.Equal(t, guard, operation.Extensions[authz.GuardExtensionKey], location)
				assert.NotEmpty(t, operation.Security, location)
				assert.Contains(t, operation.Errors, http.StatusUnauthorized, location)
				assert.Contains(t, operation.Errors, http.StatusForbidden, location)
			}
		}
	}

	assert.Equal(t, []map[string][]string{{authz.SessionScheme: {}}}, spec.Paths["/oauth/authorize"].Get.Security)
	assert.Equal(t, authz.BearerSecurity(), spec.Paths["/oauth/userinfo"].Get.Security)
	assert.Equal(t, authz.ClientSecurity(), spec.Paths["/oauth/introspect"].Post.Security)
	assert.Equal(t, []map[string][]string{{authz.ClientBasicScheme: {}}, {}}, spec.Paths["/oauth/token"].Post.Security)
	revoke := spec.Paths["/oauth/revoke"].Post
	assert.Equal(t, http.StatusOK, revoke.DefaultStatus)
	assert.Contains(t, revoke.Responses, "200")
	assert.NotContains(t, revoke.Responses, "204")
	for _, operation := range []*huma.Operation{
		spec.Paths["/oauth-clients/{client_id}"].Delete,
		spec.Paths["/oauth-clients/{client_id}/credentials"].Post,
	} {
		assert.Contains(t, operation.Errors, http.StatusNotFound)
		assert.Contains(t, operation.Responses, "404")
	}

	for _, path := range []string{"/oauth/token", "/oauth/revoke", "/oauth/introspect"} {
		operation := spec.Paths[path].Post
		require.NotNil(t, operation.RequestBody, path)
		assert.True(t, operation.RequestBody.Required, path)
		media := operation.RequestBody.Content["application/x-www-form-urlencoded"]
		require.NotNil(t, media, path)
		requestSchema := resolveOAuthSchema(spec, media.Schema)
		require.NotNil(t, requestSchema, path)
		if path == "/oauth/token" {
			assert.ElementsMatch(t, []string{
				"grant_type", "client_id", "client_secret", "code", "redirect_uri",
				"code_verifier", "refresh_token", "scope",
			}, schemaPropertyNames(requestSchema), path)
			assert.Contains(t, requestSchema.Required, "grant_type", path)
			assert.Equal(t, []any{"authorization_code", "refresh_token", "client_credentials"},
				requestSchema.Properties["grant_type"].Enum, path)
		} else if path == "/oauth/revoke" {
			assert.ElementsMatch(t, []string{
				"token", "token_type_hint", "client_id", "client_secret",
			}, schemaPropertyNames(requestSchema), path)
		} else {
			assert.ElementsMatch(t, []string{"token", "token_type_hint"}, schemaPropertyNames(requestSchema), path)
		}
		if path != "/oauth/token" {
			assert.Contains(t, requestSchema.Required, "token", path)
		}
		for _, status := range []string{"400", "401", "500"} {
			response := operation.Responses[status]
			require.NotNil(t, response, "%s %s", path, status)
			assertOAuthErrorSchema(t, spec, response.Content["application/json"].Schema, path+" "+status)
		}
	}
	for path, statuses := range map[string][]string{
		"/oauth/authorize": {"400", "500"},
		"/oauth/userinfo":  {"401", "403"},
	} {
		operation := oauthPathOperations(spec.Paths[path])[map[string]string{
			"/oauth/authorize": http.MethodGet, "/oauth/userinfo": http.MethodGet,
		}[path]]
		for _, status := range statuses {
			response := operation.Responses[status]
			require.NotNil(t, response, "%s %s", path, status)
			assertOAuthErrorSchema(t, spec, response.Content["application/json"].Schema, path+" "+status)
		}
	}
	revokeSchema := resolveOAuthSchema(spec,
		spec.Paths["/oauth/revoke"].Post.RequestBody.Content["application/x-www-form-urlencoded"].Schema)
	assert.Equal(t, []any{"refresh_token"}, revokeSchema.Properties["token_type_hint"].Enum)

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		operation := oauthPathOperations(spec.Paths[map[string]string{
			http.MethodPost: "/oauth-clients", http.MethodPut: "/oauth-clients/{client_id}",
		}[method]])[method]
		requestSchema := resolveOAuthSchema(spec, operation.RequestBody.Content["application/json"].Schema)
		require.NotNil(t, requestSchema, method)
		assert.Equal(t, []any{"authorization_code", "refresh_token", "client_credentials"},
			requestSchema.Properties["grant_types"].Items.Enum, method)
		assert.Equal(t, "uri", requestSchema.Properties["redirect_uris"].Items.Format, method)
		assert.Equal(t, `^https?://[^#]+$`, requestSchema.Properties["redirect_uris"].Items.Pattern, method)
		assert.Equal(t, `^\S+$`, requestSchema.Properties["scopes"].Items.Pattern, method)
	}

	for _, path := range []string{"/oauth-clients/{client_id}", "/oauth-clients/{client_id}/credentials"} {
		for _, operation := range oauthPathOperations(spec.Paths[path]) {
			if operation == nil {
				continue
			}
			var found bool
			for _, parameter := range operation.Parameters {
				if parameter.In == "path" && parameter.Name == "client_id" {
					found = true
					assert.True(t, parameter.Required, path)
					assert.Equal(t, `^[1-9][0-9]*$`, parameter.Schema.Pattern, path)
				}
			}
			assert.True(t, found, path)
		}
	}
}

func TestOAuthOpenAPISuccessSchemasAreTyped(t *testing.T) {
	spec := newOAuthHTTPHarness(t).api.OpenAPI()
	cases := map[string]struct {
		operation  *huma.Operation
		properties []string
		required   []string
	}{
		"discovery": {
			spec.Paths["/.well-known/openid-configuration"].Get,
			splitCSV("issuer,authorization_endpoint,token_endpoint,jwks_uri,userinfo_endpoint,revocation_endpoint,introspection_endpoint,response_types_supported,grant_types_supported,code_challenge_methods_supported,subject_types_supported,id_token_signing_alg_values_supported"),
			splitCSV("issuer,authorization_endpoint,token_endpoint,jwks_uri,userinfo_endpoint,revocation_endpoint,introspection_endpoint,response_types_supported,grant_types_supported,code_challenge_methods_supported,subject_types_supported,id_token_signing_alg_values_supported"),
		},
		"jwks": {
			spec.Paths["/.well-known/jwks.json"].Get,
			[]string{"keys"}, []string{"keys"},
		},
		"introspection": {
			spec.Paths["/oauth/introspect"].Post,
			splitCSV("active,sub,iss,aud,exp"), []string{"active"},
		},
		"userinfo": {
			spec.Paths["/oauth/userinfo"].Get,
			splitCSV("sub,preferred_username,email,email_verified"),
			[]string{"sub"},
		},
	}
	for name, testCase := range cases {
		response := testCase.operation.Responses["200"]
		require.NotNil(t, response, name)
		schema := resolveOAuthSchema(spec, response.Content["application/json"].Schema)
		require.NotNil(t, schema, name)
		assert.Equal(t, huma.TypeObject, schema.Type, name)
		assert.ElementsMatch(t, testCase.properties, schemaPropertyNames(schema), name)
		assert.ElementsMatch(t, testCase.required, schema.Required, name)
	}

	discoverySchema := resolveOAuthSchema(spec,
		spec.Paths["/.well-known/openid-configuration"].Get.Responses["200"].Content["application/json"].Schema)
	for _, property := range splitCSV("issuer,authorization_endpoint,token_endpoint,jwks_uri,userinfo_endpoint,revocation_endpoint,introspection_endpoint") {
		assert.Equal(t, huma.TypeString, discoverySchema.Properties[property].Type, property)
		assert.Equal(t, "uri", discoverySchema.Properties[property].Format, property)
	}
	for _, property := range splitCSV("response_types_supported,grant_types_supported,code_challenge_methods_supported,subject_types_supported,id_token_signing_alg_values_supported") {
		assert.Equal(t, huma.TypeArray, discoverySchema.Properties[property].Type, property)
		assert.Equal(t, huma.TypeString, discoverySchema.Properties[property].Items.Type, property)
	}

	jwksSchema := resolveOAuthSchema(spec,
		spec.Paths["/.well-known/jwks.json"].Get.Responses["200"].Content["application/json"].Schema)
	assert.Equal(t, huma.TypeArray, jwksSchema.Properties["keys"].Type)
	require.NotNil(t, jwksSchema.Properties["keys"].Items)
	jwkSchema := resolveOAuthSchema(spec, jwksSchema.Properties["keys"].Items)
	jwkProperties := splitCSV("kty,crv,use,alg,kid,x")
	assert.ElementsMatch(t, jwkProperties, schemaPropertyNames(jwkSchema))
	assert.ElementsMatch(t, jwkProperties, jwkSchema.Required)
	for _, property := range jwkProperties {
		assert.Equal(t, huma.TypeString, jwkSchema.Properties[property].Type, property)
	}

	introspectionSchema := resolveOAuthSchema(spec,
		spec.Paths["/oauth/introspect"].Post.Responses["200"].Content["application/json"].Schema)
	assert.Equal(t, huma.TypeBoolean, introspectionSchema.Properties["active"].Type)
	assert.Equal(t, huma.TypeString, introspectionSchema.Properties["sub"].Type)
	assert.Equal(t, huma.TypeString, introspectionSchema.Properties["iss"].Type)
	assert.Equal(t, huma.TypeArray, introspectionSchema.Properties["aud"].Type)
	assert.Equal(t, huma.TypeString, introspectionSchema.Properties["aud"].Items.Type)
	assert.Equal(t, huma.TypeInteger, introspectionSchema.Properties["exp"].Type)
	assert.Equal(t, "int64", introspectionSchema.Properties["exp"].Format)

	userInfoSchema := resolveOAuthSchema(spec,
		spec.Paths["/oauth/userinfo"].Get.Responses["200"].Content["application/json"].Schema)
	for _, property := range splitCSV("sub,preferred_username,email") {
		assert.Equal(t, huma.TypeString, userInfoSchema.Properties[property].Type, property)
	}
	assert.Equal(t, huma.TypeBoolean, userInfoSchema.Properties["email_verified"].Type)
}

func resolveOAuthSchema(spec *huma.OpenAPI, schema *huma.Schema) *huma.Schema {
	if schema != nil && schema.Ref != "" {
		return spec.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	return schema
}

func schemaPropertyNames(schema *huma.Schema) []string {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	return names
}

func splitCSV(value string) []string { return strings.Split(value, ",") }

func assertOAuthErrorSchema(t *testing.T, spec *huma.OpenAPI, schema *huma.Schema, location string) {
	t.Helper()
	resolved := resolveOAuthSchema(spec, schema)
	require.NotNil(t, resolved, location)
	assert.ElementsMatch(t, []string{"error", "error_description"}, schemaPropertyNames(resolved), location)
	assert.Equal(t, []string{"error"}, resolved.Required, location)
}

func TestOAuthDiscoveryAndJWKS(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	discovery := harness.request(http.MethodGet, "/.well-known/openid-configuration", "", nil, nil)
	require.Equal(t, http.StatusOK, discovery.Code, discovery.Body.String())
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(discovery.Body.Bytes(), &metadata))
	assert.Equal(t, "https://iam.example", metadata["issuer"])
	assert.Equal(t, "https://iam.example/oauth/token", metadata["token_endpoint"])
	assert.Equal(t, "https://iam.example/.well-known/jwks.json", metadata["jwks_uri"])

	jwks := harness.request(http.MethodGet, "/.well-known/jwks.json", "", nil, nil)
	require.Equal(t, http.StatusOK, jwks.Code, jwks.Body.String())
	assert.JSONEq(t, `{"keys":[]}`, jwks.Body.String())
}

func TestOAuthAuthorizationCodeHTTPLifecycle(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	query := url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"scope": {"openid profile"}, "state": {"state"}, "code_challenge": {oauthChallenge()},
		"code_challenge_method": {"S256"}, "nonce": {"nonce"},
	}
	authorized := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, authorized.Code, authorized.Body.String())
	location, err := url.Parse(authorized.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "state", location.Query().Get("state"))
	code := location.Query().Get("code")
	require.NotEmpty(t, code)

	token := harness.form(http.MethodPost, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "code": {code},
		"redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
	}, nil)
	require.Equal(t, http.StatusOK, token.Code, token.Body.String())
	var tokens TokenResponse
	require.NoError(t, json.Unmarshal(token.Body.Bytes(), &tokens))
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	require.NotEmpty(t, tokens.IDToken)
	assert.Equal(t, "openid profile", harness.web.tokens[tokens.IDToken].Raw["scope"], "ID token issuance receives the consented scope")

	userinfo := harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer " + tokens.AccessToken})
	require.Equal(t, http.StatusOK, userinfo.Code, userinfo.Body.String())
	var claims map[string]any
	require.NoError(t, json.Unmarshal(userinfo.Body.Bytes(), &claims))
	assert.Equal(t, "101", claims["sub"])

	replay := harness.form(http.MethodPost, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "code": {code},
		"redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
	}, nil)
	require.Equal(t, http.StatusBadRequest, replay.Code, replay.Body.String())
	assert.Equal(t, "invalid_grant", decodeProtocolError(t, replay).Code)

	revoked := harness.form(http.MethodPost, "/oauth/revoke", url.Values{
		"client_id": {client.ID}, "token": {tokens.RefreshToken}, "token_type_hint": {"refresh_token"},
	}, nil)
	assert.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
}

func TestOAuthIntrospectionPreservesAuthenticationFailures(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client, secret, err := harness.service.CreateClient(t.Context(), 7, "Introspection", nil,
		[]string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit)
	require.NoError(t, err)
	headers := map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(client.ID+":"+secret))}
	for _, failure := range []error{
		authnext.ErrUnavailable, context.Canceled, context.DeadlineExceeded,
		errors.New("grant storage unavailable"),
		errors.Join(authnext.ErrInvalidToken, authnext.ErrUnavailable),
	} {
		harness.web.authenticateError = failure
		response := harness.form(http.MethodPost, "/oauth/introspect", url.Values{"token": {"probe"}}, headers)
		assert.Equal(t, http.StatusInternalServerError, response.Code)
		if response.Code == http.StatusInternalServerError {
			assert.Equal(t, "server_error", decodeProtocolError(t, response).Code)
		}
		assert.NotContains(t, response.Body.String(), failure.Error())
	}
	for _, invalid := range []error{
		authnext.ErrInvalidToken, authnext.ErrExpiredToken, authnext.ErrUnknownKey,
		authnext.ErrInvalidIssuer, authnext.ErrInvalidAud, authnext.ErrInvalidCredentials,
		authnext.ErrMissingBearer, authnext.ErrInvalidBearer, authnext.ErrInvalidSession,
	} {
		harness.web.authenticateError = invalid
		response := harness.form(http.MethodPost, "/oauth/introspect", url.Values{"token": {"probe"}}, headers)
		require.Equal(t, http.StatusOK, response.Code)
		var result map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		assert.Equal(t, map[string]any{"active": false}, result)
	}
}

func TestOAuthIntrospectionAndProtocolErrors(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(client.ID+":"+secret))
	token := harness.form(http.MethodPost, "/oauth/token", url.Values{
		"grant_type": {"client_credentials"}, "scope": {"jobs.read"},
	}, map[string]string{"Authorization": basic})
	require.Equal(t, http.StatusOK, token.Code, token.Body.String())
	var issued TokenResponse
	require.NoError(t, json.Unmarshal(token.Body.Bytes(), &issued))

	introspection := harness.form(http.MethodPost, "/oauth/introspect", url.Values{
		"token": {issued.AccessToken},
	}, map[string]string{"Authorization": basic})
	require.Equal(t, http.StatusOK, introspection.Code, introspection.Body.String())
	var result map[string]any
	require.NoError(t, json.Unmarshal(introspection.Body.Bytes(), &result))
	assert.Equal(t, true, result["active"])
	assert.Equal(t, "client:"+client.ID, result["sub"])
	bodyCredentials := harness.form(http.MethodPost, "/oauth/introspect", url.Values{
		"client_id": {client.ID}, "client_secret": {secret}, "token": {issued.AccessToken},
	}, nil)
	require.Equal(t, http.StatusUnauthorized, bodyCredentials.Code, bodyCredentials.Body.String())
	assert.Equal(t, "invalid_client", decodeProtocolError(t, bodyCredentials).Code)
	invalidBasic := harness.form(http.MethodPost, "/oauth/introspect", url.Values{
		"token": {issued.AccessToken},
	}, map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(client.ID+":wrong"))})
	require.Equal(t, http.StatusUnauthorized, invalidBasic.Code, invalidBasic.Body.String())
	assert.Equal(t, "invalid_client", decodeProtocolError(t, invalidBasic).Code)

	wrongSecret := harness.form(http.MethodPost, "/oauth/token", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {client.ID}, "client_secret": {"wrong"},
	}, nil)
	require.Equal(t, http.StatusUnauthorized, wrongSecret.Code, wrongSecret.Body.String())
	assert.Equal(t, "invalid_client", decodeProtocolError(t, wrongSecret).Code)
	missingGrant := harness.form(http.MethodPost, "/oauth/token", url.Values{"client_id": {client.ID}}, nil)
	require.Equal(t, http.StatusBadRequest, missingGrant.Code, missingGrant.Body.String())
	assert.Equal(t, "invalid_request", decodeProtocolError(t, missingGrant).Code)

	for name, response := range map[string]*httptest.ResponseRecorder{
		"revoke":     harness.form(http.MethodPost, "/oauth/revoke", url.Values{"token": {"x"}}, nil),
		"introspect": harness.form(http.MethodPost, "/oauth/introspect", url.Values{"token": {"x"}}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
			body := decodeProtocolError(t, response)
			assert.Equal(t, "invalid_client", body.Code)
			assert.NotEmpty(t, body.Description)
			assert.NotContains(t, response.Body.String(), `"code":`)
			assert.NotContains(t, response.Body.String(), `"message":`)
		})
	}
}

func TestOAuthUserInfoRequiresPrincipalAccessTokenWithOpenIDScope(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	for token, claims := range map[string]*authnext.Claims{
		"valid":     {Subject: "101", SubjectType: authnext.SubjectTypePrincipal, Raw: map[string]any{"scope": "openid profile"}},
		"client":    {Subject: "client:7", SubjectType: authnext.SubjectTypeOAuthClient, Raw: map[string]any{"scope": "openid"}},
		"no-openid": {Subject: "101", SubjectType: authnext.SubjectTypePrincipal, Raw: map[string]any{"scope": "profile"}},
	} {
		harness.web.mu.Lock()
		harness.web.tokens[token] = claims
		harness.web.mu.Unlock()
	}
	assert.Equal(t, http.StatusOK, harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer valid"}).Code)
	for _, token := range []string{"client", "missing"} {
		response := harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer " + token})
		assert.Equal(t, http.StatusUnauthorized, response.Code, token+": "+response.Body.String())
		assert.Equal(t, "invalid_token", decodeProtocolError(t, response).Code)
	}
	noOpenID := harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer no-openid"})
	require.Equal(t, http.StatusForbidden, noOpenID.Code, noOpenID.Body.String())
	assert.Equal(t, "insufficient_scope", decodeProtocolError(t, noOpenID).Code)
}

func TestOAuthUserInfoProjectsOnlyConsentedFieldsAndReportsPolicyFailures(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	for _, scope := range []string{"openid", "openid profile", "openid email", "openid profile email"} {
		harness.web.tokens["profile-test"] = &authnext.Claims{
			Subject: "101", PrincipalID: 101, OrganizationID: "7", SubjectType: authnext.SubjectTypePrincipal,
			PreferredUsername: "alice", Email: "alice@example.test", EmailVerified: true,
			Raw: map[string]any{"scope": scope},
		}
		response := harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer profile-test"})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var document map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &document))
		assert.Equal(t, "101", document["sub"])
		_, username := document["preferred_username"]
		_, email := document["email"]
		_, verified := document["email_verified"]
		assert.Equal(t, containsWord(scope, "profile"), username)
		assert.Equal(t, containsWord(scope, "email"), email)
		assert.Equal(t, email, verified)
		assert.Equal(t, scope, harness.web.profileScope)
	}
	for _, failure := range []struct {
		err    error
		status int
		code   string
	}{
		{authz.ErrFieldDenied, http.StatusForbidden, "access_denied"},
		{authz.ErrFieldsUnavailable, http.StatusServiceUnavailable, "server_error"},
		{authnext.ErrInvalidSession, http.StatusUnauthorized, "invalid_token"},
	} {
		harness.web.profileError = failure.err
		response := harness.request(http.MethodGet, "/oauth/userinfo", "", nil, map[string]string{"Authorization": "Bearer profile-test"})
		require.Equal(t, failure.status, response.Code, response.Body.String())
		assert.Equal(t, failure.code, decodeProtocolError(t, response).Code)
		assert.NotContains(t, response.Body.String(), "alice@example.test")
	}
}

func TestOAuthProtocolRequiresTokenAndRejectsUnsupportedRevocationHint(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	basic := map[string]string{
		"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(client.ID+":"+secret)),
	}
	for _, endpoint := range []string{"revoke", "introspect"} {
		response := harness.form(http.MethodPost, "/oauth/"+endpoint, url.Values{
			"token_type_hint": {"refresh_token"},
		}, basic)
		require.Equal(t, http.StatusBadRequest, response.Code, endpoint+": "+response.Body.String())
		assert.Equal(t, "invalid_request", decodeProtocolError(t, response).Code)
	}
	response := harness.form(http.MethodPost, "/oauth/revoke", url.Values{
		"token": {"access-token"}, "token_type_hint": {"access_token"},
	}, basic)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	assert.Equal(t, "unsupported_token_type", decodeProtocolError(t, response).Code)
}

func TestOAuthAuthorizationPreservesAuthenticationFailures(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	query := url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"scope": {"openid"}, "code_challenge": {oauthChallenge()}, "code_challenge_method": {"S256"},
	}
	for _, failure := range []error{
		errors.Join(authnext.ErrUnavailable, errors.New("session writer unavailable")),
		context.Canceled,
		errors.New("principal claims lookup failed"),
	} {
		for _, prompt := range []string{"none", ""} {
			harness.web.authenticateError = failure
			query.Set("prompt", prompt)
			response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.Empty(t, response.Header().Get("Location"))
			assert.Equal(t, "server_error", decodeProtocolError(t, response).Code)
			assert.NotContains(t, response.Body.String(), failure.Error())
		}
	}
	for _, credentialError := range []error{authnext.ErrInvalidSession, authnext.ErrMissingBearer, authnext.ErrInvalidCredentials} {
		harness.web.authenticateError = credentialError
		query.Set("prompt", "none")
		response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, nil)
		require.Equal(t, http.StatusFound, response.Code, response.Body.String())
		location, err := url.Parse(response.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "login_required", location.Query().Get("error"))
	}
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))
	assert.Zero(t, harness.count(t, "iam_oauth_consent_requests"))
}

func TestOAuthSilentAuthorizationRedirectSafety(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	query := url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"scope": {"openid"}, "code_challenge": {oauthChallenge()}, "code_challenge_method": {"S256"},
		"prompt": {"none"}, "state": {"state with & delimiters"},
	}
	for _, cookie := range []string{"", "expired-session"} {
		response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": cookie})
		require.Equal(t, http.StatusFound, response.Code, response.Body.String())
		location, err := url.Parse(response.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "login_required", location.Query().Get("error"))
		assert.Equal(t, query.Get("state"), location.Query().Get("state"))
		location.RawQuery = ""
		assert.Equal(t, client.RedirectURIs[0], location.String())
	}
	assert.Zero(t, harness.count(t, "iam_oauth_consent_requests"))
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))
	query.Set("redirect_uri", "https://attacker.example/callback")
	response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, nil)
	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Header().Get("Location"))
	assert.Equal(t, "invalid_request", decodeProtocolError(t, response).Code)
	query.Set("redirect_uri", client.RedirectURIs[0])
	query.Set("client_id", "1234567890123456789")
	response = harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, nil)
	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, response.Header().Get("Location"))
}

func TestOAuthPromptDirectsTheBrowserToTheConsentScreen(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	query := url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"scope": {"openid"}, "code_challenge": {oauthChallenge()}, "code_challenge_method": {"S256"},
		"prompt": {"consent"},
	}
	response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())
	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "/oauth/consent", location.Path)
	challenge := location.Query().Get("consent_challenge")
	assert.Len(t, challenge, 43)
	// The challenge travels in the redirect but is stored only as a hash, so
	// reading the table cannot reproduce the value the browser holds.
	var stored string
	require.NoError(t, harness.db.Table(harness.table("iam_oauth_consent_requests")).
		Pluck("challenge_hash", &stored).Error)
	assert.NotEqual(t, challenge, stored)

	// prompt=none forbids interaction, so it still reports the OIDC error code
	// instead of redirecting to a screen nobody asked to see.
	query.Set("prompt", "none")
	silent := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, silent.Code, silent.Body.String())
	silentURL, err := url.Parse(silent.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "consent_required", silentURL.Query().Get("error"))

	// A well-formed request that the service still rejects keeps reporting
	// invalid_request, so adding the screen did not widen consent_required.
	query.Del("prompt")
	query.Set("client_id", "1234567890123456789")
	unknown := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusBadRequest, unknown.Code, unknown.Body.String())
	assert.Equal(t, "invalid_request", decodeProtocolError(t, unknown).Code)
}

// startConsentOverHTTP runs an authorization that needs consent and returns the
// challenge the browser would carry to the screen.
func startConsentOverHTTP(t *testing.T, harness oauthHTTPHarness, client Client) string {
	t.Helper()
	query := url.Values{
		"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
		"scope": {"openid profile"}, "code_challenge": {oauthChallenge()}, "code_challenge_method": {"S256"},
		"state": {"xyz"}, "prompt": {"consent"},
	}
	response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())
	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)
	return location.Query().Get("consent_challenge")
}

func TestConsentScreenRendersTheRequestAndGrantsOnApproval(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	challenge := startConsentOverHTTP(t, harness, client)

	page := harness.request(http.MethodGet, "/oauth/consent?consent_challenge="+challenge, "", nil,
		map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Header().Get("Content-Type"), "text/html")
	// The screen names one subject and one client, so a shared cache must not
	// be allowed to hand it to anyone else.
	assert.Equal(t, "no-store", page.Header().Get("Cache-Control"))
	body := page.Body.String()
	assert.Contains(t, body, "Browser")
	assert.Contains(t, body, challenge)
	assert.Contains(t, body, "Confirm who you are", "a translated scope is explained, not just named")
	assert.Contains(t, body, "Read your basic profile")

	// The same request is also readable as JSON, which is what a consent
	// application hosted by the operator renders from.
	document := harness.request(http.MethodGet, "/oauth/consent/request?consent_challenge="+challenge, "", nil,
		map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusOK, document.Code, document.Body.String())
	var request ConsentRequest
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &request), document.Body.String())
	assert.Equal(t, challenge, request.Challenge)
	assert.Equal(t, "Browser", request.ClientName)
	assert.Equal(t, []string{"openid", "profile"}, request.Scopes)

	decision := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"allow"},
	}, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, decision.Code, decision.Body.String())
	redirect, err := url.Parse(decision.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://app.example/callback", redirect.Scheme+"://"+redirect.Host+redirect.Path)
	assert.NotEmpty(t, redirect.Query().Get("code"))
	assert.Equal(t, "xyz", redirect.Query().Get("state"), "state must survive the detour through the screen")
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consents"))
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_codes"))

	// The challenge is single-use: replaying the approval must not mint a
	// second authorization code for the same parked request.
	replay := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"allow"},
	}, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusBadRequest, replay.Code, replay.Body.String())
	assert.Equal(t, "invalid_request", decodeProtocolError(t, replay).Code)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_codes"))
}

func TestConsentDenialReturnsAccessDeniedToTheClient(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	challenge := startConsentOverHTTP(t, harness, client)

	decision := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"deny"},
	}, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, decision.Code, decision.Body.String())
	redirect, err := url.Parse(decision.Header().Get("Location"))
	require.NoError(t, err)
	// RFC 6749 4.1.2.1: a refusal is reported to the client at its redirect
	// URI, not left as an error page the client never learns about.
	assert.Equal(t, "access_denied", redirect.Query().Get("error"))
	assert.Equal(t, "xyz", redirect.Query().Get("state"))
	assert.Empty(t, redirect.Query().Get("code"))
	assert.Zero(t, harness.count(t, "iam_oauth_consents"))
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))
	assert.Equal(t, []string{"oauth_client_created", "oauth_consent_denied"}, harness.eventTypes(t))
}

func TestConsentChallengeIsRefusedOutsideItsOwnSessionAndLifetime(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	challenge := startConsentOverHTTP(t, harness, client)

	for _, refused := range []struct {
		name      string
		challenge string
		headers   map[string]string
		status    int
	}{
		{name: "no session", challenge: challenge, headers: map[string]string{}, status: http.StatusBadRequest},
		{
			name: "unknown session", challenge: challenge,
			headers: map[string]string{"Cookie": "other"}, status: http.StatusBadRequest,
		},
		{
			name: "unknown challenge", challenge: strings.Repeat("z", 43),
			headers: map[string]string{"Cookie": "session"}, status: http.StatusBadRequest,
		},
		// A wrong-length value never reaches the service: the declared schema
		// rejects it, which is why it reports a validation failure instead.
		{
			name: "malformed challenge", challenge: "short",
			headers: map[string]string{"Cookie": "session"}, status: http.StatusUnprocessableEntity,
		},
	} {
		page := harness.request(http.MethodGet, "/oauth/consent?consent_challenge="+refused.challenge, "", nil, refused.headers)
		assert.Equal(t, refused.status, page.Code, "%s: %s", refused.name, page.Body.String())
	}

	// A challenge belongs to the subject who started it. Another signed-in
	// subject holding the same value must not be able to answer for them.
	harness.web.setPrincipal(202)
	other := harness.request(http.MethodGet, "/oauth/consent?consent_challenge="+challenge, "", nil,
		map[string]string{"Cookie": "session"})
	assert.Equal(t, http.StatusBadRequest, other.Code, other.Body.String())
	harness.web.setPrincipal(101)

	// An expired request is refused even by the subject who started it.
	require.NoError(t, harness.db.Table(harness.table("iam_oauth_consent_requests")).
		Where("1 = 1").Update("expires_at", oauthTestNow.Add(-time.Minute)).Error)
	expired := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"allow"},
	}, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusBadRequest, expired.Code, expired.Body.String())
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))
}

func TestConsentDecisionRejectsUnknownAnswersAndFailedCSRF(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	challenge := startConsentOverHTTP(t, harness, client)

	for _, decision := range []string{"", "Allow", "maybe"} {
		response := harness.form(http.MethodPost, "/oauth/consent", url.Values{
			"consent_challenge": {challenge}, "decision": {decision},
		}, map[string]string{"Cookie": "session"})
		require.Equal(t, http.StatusBadRequest, response.Code, "decision=%q: %s", decision, response.Body.String())
	}
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))

	// The decision is a cookie-authenticated state change, which is exactly
	// what CSRF targets, so a cross-origin post must be refused.
	harness.web.setCSRF(assert.AnError)
	blocked := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"allow"},
	}, map[string]string{"Cookie": "session", "Origin": "https://evil.example"})
	require.Equal(t, http.StatusForbidden, blocked.Code, blocked.Body.String())
	harness.web.setCSRF(nil)

	// Refusing that post must leave the request answerable rather than
	// consuming the challenge on the way out.
	allowed := harness.form(http.MethodPost, "/oauth/consent", url.Values{
		"consent_challenge": {challenge}, "decision": {"allow"},
	}, map[string]string{"Cookie": "session"})
	require.Equal(t, http.StatusFound, allowed.Code, allowed.Body.String())
}

func TestOAuthClientManagementHTTPLifecycleAndTenantIsolation(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	headersA, headersB := managementHeaders(7), managementHeaders(8)
	created := harness.json(http.MethodPost, "/oauth-clients", map[string]any{
		"name": "Worker", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false,
	}, headersA)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	createdData := decodeOAuthEnvelope[clientCreateData](t, created).Data
	require.NotEmpty(t, createdData.Client.ID)
	require.NotEmpty(t, createdData.ClientSecret)
	id := createdData.Client.ID

	listedA := harness.request(http.MethodGet, "/oauth-clients", "", nil, headersA)
	require.Equal(t, http.StatusOK, listedA.Code, listedA.Body.String())
	assert.Len(t, decodeOAuthEnvelope[[]Client](t, listedA).Data, 1)
	listedB := harness.request(http.MethodGet, "/oauth-clients", "", nil, headersB)
	require.Equal(t, http.StatusOK, listedB.Code, listedB.Body.String())
	assert.Empty(t, decodeOAuthEnvelope[[]Client](t, listedB).Data)

	replacement := map[string]any{
		"name": "Worker Two", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false, "status": "active",
	}
	assert.Equal(t, http.StatusUnprocessableEntity, harness.json(http.MethodPut, "/oauth-clients/"+id, replacement, headersB).Code)
	assert.Equal(t, http.StatusNotFound, harness.request(http.MethodPost, "/oauth-clients/"+id+"/credentials", "", nil, headersB).Code)
	assert.Equal(t, http.StatusNotFound, harness.request(http.MethodDelete, "/oauth-clients/"+id, "", nil, headersB).Code)

	updated := harness.json(http.MethodPut, "/oauth-clients/"+id, replacement, headersA)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Equal(t, "Worker Two", decodeOAuthEnvelope[Client](t, updated).Data.Name)
	rotated := harness.request(http.MethodPost, "/oauth-clients/"+id+"/credentials", "", nil, headersA)
	require.Equal(t, http.StatusOK, rotated.Code, rotated.Body.String())
	assert.NotEmpty(t, decodeOAuthEnvelope[clientSecretData](t, rotated).Data.ClientSecret)
	deleted := harness.request(http.MethodDelete, "/oauth-clients/"+id, "", nil, headersA)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.True(t, decodeOAuthEnvelope[clientDeleteData](t, deleted).Data.Deleted)
	assert.Zero(t, harness.count(t, "iam_oauth_clients"))
}

func TestOAuthHTTPRejectsMissingAuthAndInvalidSelectors(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	assert.Equal(t, http.StatusUnauthorized, harness.request(http.MethodGet, "/oauth-clients", "", nil, map[string]string{authz.TenantHeader: "7"}).Code)
	assert.Equal(t, http.StatusForbidden, harness.request(http.MethodGet, "/oauth-clients", "", nil, map[string]string{
		"Authorization": "Bearer admin", authz.TenantHeader: "not-an-id",
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, harness.request(http.MethodDelete, "/oauth-clients/not-an-id", "", nil, managementHeaders(7)).Code)
}

func TestOAuthHTTPUsesLocalizedProtocolAndManagementErrors(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	protocol := harness.form(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"nope"}}, map[string]string{"X-Lang": "zh-CN"})
	require.Equal(t, http.StatusBadRequest, protocol.Code, protocol.Body.String())
	protocolBody := decodeProtocolError(t, protocol)
	assert.Equal(t, "invalid_request", protocolBody.Code)
	assert.NotEqual(t, oauthi18n.InvalidRequest, protocolBody.Description)
	assert.Contains(t, protocolBody.Description, "请求")

	managementHeaders := managementHeaders(7)
	managementHeaders["X-Lang"] = "ms-MY"
	management := harness.request(http.MethodDelete, "/oauth-clients/999999", "", nil, managementHeaders)
	require.Equal(t, http.StatusNotFound, management.Code, management.Body.String())
	message := decodeOAuthEnvelope[any](t, management).Message
	assert.NotEqual(t, oauthi18n.ClientNotFound, message)
	assert.Contains(t, message, "tidak ditemui")
}

func TestOAuthHTTPDatabaseFailuresUseCorrectResponseContracts(t *testing.T) {
	t.Run("authorize", func(t *testing.T) {
		harness := newOAuthHTTPHarness(t)
		client := createBrowserClient(t, harness.oauthHarness)
		require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_codes")).Error)
		query := url.Values{
			"client_id": {client.ID}, "redirect_uri": {client.RedirectURIs[0]}, "response_type": {"code"},
			"scope": {"openid"}, "code_challenge": {oauthChallenge()}, "code_challenge_method": {"S256"},
		}
		response := harness.request(http.MethodGet, "/oauth/authorize?"+query.Encode(), "", nil, map[string]string{"Cookie": "session"})
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, "server_error", decodeProtocolError(t, response).Code)
	})

	for _, endpoint := range []string{"token", "revoke", "introspect"} {
		t.Run(endpoint, func(t *testing.T) {
			harness := newOAuthHTTPHarness(t)
			require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_clients")).Error)
			headers := map[string]string(nil)
			if endpoint == "introspect" {
				headers = map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("7:secret"))}
			}
			response := harness.form(http.MethodPost, "/oauth/"+endpoint, url.Values{
				"grant_type": {"client_credentials"}, "client_id": {"7"}, "client_secret": {"secret"},
				"token": {"token"},
			}, headers)
			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.Equal(t, "server_error", decodeProtocolError(t, response).Code)
		})
	}

	t.Run("management", func(t *testing.T) {
		harness := newOAuthHTTPHarness(t)
		require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_clients")).Error)
		response := harness.request(http.MethodGet, "/oauth-clients", "", nil, managementHeaders(7))
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		body := decodeOAuthEnvelope[any](t, response)
		assert.Equal(t, http.StatusInternalServerError, body.Code)
		assert.NotEqual(t, oauthi18n.ServerError, body.Message)
	})

	// A self-service route reports its own failure as a server fault. Reporting
	// it as "you have no such grant" would tell somebody their access was
	// already gone when the truth is that nobody could look.
	t.Run("consent self-service", func(t *testing.T) {
		harness := newOAuthHTTPHarness(t)
		client := createBrowserClient(t, harness.oauthHarness)
		require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_consents")).Error)
		caller := map[string]string{"Cookie": "session"}
		for name, request := range map[string]struct {
			method string
			target string
		}{
			"listing":    {method: http.MethodGet, target: "/me/oauth-consents"},
			"revocation": {method: http.MethodDelete, target: "/me/oauth-consents/" + client.ID},
		} {
			t.Run(name, func(t *testing.T) {
				response := harness.request(request.method, request.target, "", nil, caller)
				require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
				body := decodeOAuthEnvelope[any](t, response)
				assert.Equal(t, http.StatusInternalServerError, body.Code)
				assert.NotEqual(t, oauthi18n.ServerError, body.Message, "the message has to be translated")
			})
		}
	})
}

func TestOAuthProtocolRejectsMalformedFormEncoding(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	missingAuthorize := harness.request(http.MethodGet, "/oauth/authorize", "", nil, nil)
	require.Equal(t, http.StatusBadRequest, missingAuthorize.Code, missingAuthorize.Body.String())
	assert.Equal(t, "invalid_request", decodeProtocolError(t, missingAuthorize).Code)
	assert.NotContains(t, missingAuthorize.Body.String(), `"message":`)

	for _, endpoint := range []string{"token", "revoke", "introspect"} {
		response := harness.request(
			http.MethodPost, "/oauth/"+endpoint, "application/x-www-form-urlencoded",
			strings.NewReader("token=a;b"), nil,
		)
		require.Equal(t, http.StatusBadRequest, response.Code, "%s: %s", endpoint, response.Body.String())
		assert.Equal(t, "invalid_request", decodeProtocolError(t, response).Code)
	}
}

func TestConsentModeDefaultsToImplicitOverHTTP(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	headers := managementHeaders(7)

	// Omitting the field must keep the pre-existing behavior. The service
	// rejects an unset mode, so this also pins that the schema default is
	// actually applied rather than leaving the Go zero value.
	created := harness.json(http.MethodPost, "/oauth-clients", map[string]any{
		"name": "Worker", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false,
	}, headers)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	assert.Equal(t, ConsentImplicit, decodeOAuthEnvelope[clientCreateData](t, created).Data.Client.ConsentMode)

	explicit := harness.json(http.MethodPost, "/oauth-clients", map[string]any{
		"name": "Third Party", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false, "consent_mode": "explicit",
	}, headers)
	require.Equal(t, http.StatusCreated, explicit.Code, explicit.Body.String())
	explicitID := decodeOAuthEnvelope[clientCreateData](t, explicit).Data.Client.ID
	assert.Equal(t, ConsentExplicit, decodeOAuthEnvelope[clientCreateData](t, explicit).Data.Client.ConsentMode)

	// An unknown value is refused by the schema, so a typo cannot silently
	// land a client in the permissive mode.
	rejected := harness.json(http.MethodPost, "/oauth-clients", map[string]any{
		"name": "Typo", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false, "consent_mode": "trusted",
	}, headers)
	assert.Equal(t, http.StatusUnprocessableEntity, rejected.Code, rejected.Body.String())

	// PUT replaces the whole client, and the update body carries the same
	// default, so omitting the mode resets an explicit client to implicit.
	// That is consistent with public_client and status, but it means a caller
	// that does not round-trip the field relaxes the client; pinned here so
	// the behavior is a decision rather than an accident.
	replaced := harness.json(http.MethodPut, "/oauth-clients/"+explicitID, map[string]any{
		"name": "Third Party", "grant_types": []string{"client_credentials"},
		"scopes": []string{"jobs.read"}, "public_client": false, "status": "active",
	}, headers)
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Equal(t, ConsentImplicit, decodeOAuthEnvelope[Client](t, replaced).Data.ConsentMode)
}

// TestConsentEndpointsReportStorageFaultsAsServerErrors keeps a broken
// database distinguishable from a bad challenge. Reporting a fault as
// invalid_request would tell an operator the request was rejected when in
// fact nothing was ever read.
func TestConsentEndpointsReportStorageFaultsAsServerErrors(t *testing.T) {
	harness := newOAuthHTTPHarness(t)
	client := createBrowserClient(t, harness.oauthHarness)
	challenge := startConsentOverHTTP(t, harness, client)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_consent_requests")).Error)

	for _, response := range []*httptest.ResponseRecorder{
		harness.request(http.MethodGet, "/oauth/consent?consent_challenge="+challenge, "", nil,
			map[string]string{"Cookie": "session"}),
		harness.request(http.MethodGet, "/oauth/consent/request?consent_challenge="+challenge, "", nil,
			map[string]string{"Cookie": "session"}),
		harness.form(http.MethodPost, "/oauth/consent", url.Values{
			"consent_challenge": {challenge}, "decision": {"allow"},
		}, map[string]string{"Cookie": "session"}),
	} {
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.Equal(t, "server_error", decodeProtocolError(t, response).Code)
	}
}
