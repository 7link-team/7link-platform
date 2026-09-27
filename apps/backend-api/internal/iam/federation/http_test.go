package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type federationEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func localized(locale, key string) string {
	return i18n.TContext(i18n.WithLocale(context.Background(), locale), key)
}

func newManagementAPI(t *testing.T, env *federationEnvironment) (*http.Client, *httptest.Server) {
	t.Helper()
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, RegisterI18n())
	respx.Install()
	router := chi.NewRouter()
	router.Use(respx.Timing, respx.Locale)
	config := huma.DefaultConfig("federation test", "1.0.0")
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	api := humachi.New(router, config)
	RegisterAdminREST(api, env.service, federationRuntimeRegistrar(federationAdminVerifier{}))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server.Client(), server
}

func TestAccountViewCannotConvertOrRevokeOperatorBrowserThroughFederation(t *testing.T) {
	env := newFederationEnvironment(t)
	_, cookie := samlSessionCookie(t, env)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("view-protocol-test", "1"))
	RegisterHTTP(api, env.service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	for _, token := range []string{"avt_read", "avt_write"} {
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/identity-providers/1/authorization"},
			{http.MethodGet, "/identity-providers/1/callback?code=code&state=state"},
			{http.MethodPost, "/identity-providers/1/callback"},
			{http.MethodGet, "/saml/7/sso"},
			{http.MethodPost, "/saml/7/sso"},
			{http.MethodGet, "/saml/7/slo"},
			{http.MethodPost, "/saml/7/slo"},
		} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(route.method, route.path, strings.NewReader("SAMLRequest=payload"))
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Cookie", cookie)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusForbidden, recorder.Code, "%s %s: %s", route.method, route.path, recorder.Body.String())
			assert.Empty(t, recorder.Header().Values("Set-Cookie"))
			assert.Empty(t, recorder.Header().Get("Location"))
		}
	}
	_, err := env.web.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err, "protocol views cannot revoke the operator's cookie session")
	assert.Len(t, env.web.sessions, 1, "protocol views cannot create an ordinary login")
}

// federationAdminVerifier authenticates the administrative HTTP tests. The
// owned-resource routes carry no tenant segment, so the registrar has to
// authenticate the caller and resolve the provider's owning tenant before the
// handler can read it from the request context.
type federationAdminVerifier struct{}

func (federationAdminVerifier) Authenticate(_ context.Context, authorization, _ string) (*authnext.Claims, error) {
	if strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer")) == "" {
		return nil, authnext.ErrInvalidToken
	}
	return &authnext.Claims{Subject: "admin", SubjectType: authnext.SubjectTypePrincipal, PrincipalID: 1, ACR: 1}, nil
}

// SessionCookieName mirrors the production verifier, which accepts the browser
// session cookie as well as a bearer token, so the harness documents the same
// operation security the composed application publishes.
func (federationAdminVerifier) SessionCookieName() string {
	return authnext.DefaultSessionCookieName
}

func adminRequest(t *testing.T, client *http.Client, method, target, tenantID string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	if tenantID != "" {
		parsed, err := url.Parse(target)
		require.NoError(t, err)
		parsed.Path = "/tenants/" + tenantID + parsed.Path
		target = parsed.String()
	}
	request, err := http.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if request.Header.Get("Authorization") == "" {
		request.Header.Set("Authorization", "Bearer admin")
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	return response
}

func decodeHTTPBody(t *testing.T, response *http.Response, target any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(response.Body).Decode(target))
	require.NoError(t, response.Body.Close())
}

func TestFederationManagementHTTPWorkflow(t *testing.T) {
	env := newFederationEnvironment(t)
	insertRole(t, env.db, "tenant-a", "role-a")
	client, server := newManagementAPI(t, env)

	create := []byte("{\"name\":\"GitLab\",\"provider_type\":\"oidc\",\"issuer\":\"https://gitlab.example\",\"client_id\":\"app\",\"client_secret\":\"s3cret\",\"auto_provision\":true,\"default_role_id\":\"" + wireID("role-a") + "\",\"status\":\"active\"}")
	response := adminRequest(t, client, http.MethodPost, server.URL+"/identity-providers", wireID("tenant-a"), create, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	t.Logf("CREATE RESPONSE code=%d message=%q data=%s", envelope.Code, envelope.Message, string(envelope.Data))
	assert.Zero(t, envelope.Code)
	var provider Provider
	require.NoError(t, json.Unmarshal(envelope.Data, &provider))
	assert.Equal(t, "GitLab", provider.Name)
	assert.Equal(t, "https://gitlab.example", provider.Issuer)
	assert.True(t, provider.ClientSecretSet)

	response = adminRequest(t, client, http.MethodGet, server.URL+"/identity-providers", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	var providers []Provider
	require.NoError(t, json.Unmarshal(envelope.Data, &providers))
	require.Len(t, providers, 1)

	response = adminRequest(t, client, http.MethodPost, server.URL+"/identity-providers", wireID("tenant-a"), create, map[string]string{"Accept-Language": "zh-CN"})
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("zh-CN", "federation.provider_issuer_exists"), envelope.Message)
	assert.NotEqual(t, "federation_provider_issuer_exists", envelope.Message)

	response = adminRequest(t, client, http.MethodPost, server.URL+"/identity-providers", wireID("tenant-a"), []byte("{\"name\":\"Broken\",\"issuer\":\"https://broken.example\",\"client_id\":\"app\",\"auto_provision\":true,\"default_role_id\":\""+wireID("missing")+"\",\"status\":\"active\"}"), nil)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.provider_role_missing"), envelope.Message)

	response = adminRequest(t, client, http.MethodPost, server.URL+"/identity-providers", wireID("tenant-a"), []byte(`{"name":"Bad","issuer":"not a url","client_id":"app","auto_provision":true,"status":"active"}`), nil)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.invalid_request"), envelope.Message)

	response = adminRequest(t, client, http.MethodPut, server.URL+"/identity-providers/"+provider.ID.String(), "", []byte("{\"name\":\"GitLab Renamed\",\"provider_type\":\"oidc\",\"issuer\":\"https://gitlab.example\",\"client_id\":\"app\",\"auto_provision\":true,\"default_role_id\":\""+wireID("role-a")+"\",\"status\":\"active\"}"), nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	require.NoError(t, json.Unmarshal(envelope.Data, &provider))
	assert.Equal(t, "GitLab Renamed", provider.Name)

	response = adminRequest(t, client, http.MethodPut, server.URL+"/identity-providers/"+wireID("missing"), "", []byte(`{"name":"X","issuer":"https://other.example","client_id":"app","auto_provision":true,"status":"active"}`), nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())

	response = adminRequest(t, client, http.MethodDelete, server.URL+"/identity-providers/"+provider.ID.String(), "", nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Contains(t, string(envelope.Data), `"deleted":true`)

	response = adminRequest(t, client, http.MethodGet, server.URL+"/identity-providers", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	require.NoError(t, json.Unmarshal(envelope.Data, &providers))
	assert.Empty(t, providers)

	response = adminRequest(t, client, http.MethodDelete, server.URL+"/identity-providers/"+provider.ID.String(), "", nil, nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func TestFederationBrowserLoginHTTPFlow(t *testing.T) {
	env := newFederationEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("GitLab", env.idp.issuer))
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, RegisterI18n())
	respx.Install()
	router := chi.NewRouter()
	config := huma.DefaultConfig("browser test", "1.0.0")
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	api := humachi.New(router, config)
	RegisterBrowserREST(api, env.service)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	startURL := server.URL + "/identity-providers/" + provider.ID.String() + "/authorization?return_url=" + url.QueryEscape("https://app.example/")
	response, err := client.Get(startURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, response.StatusCode)
	location := response.Header.Get("Location")
	parsedLocation, err := url.Parse(location)
	require.NoError(t, err)
	assert.Equal(t, env.idp.issuer+"/authorize", parsedLocation.Scheme+"://"+parsedLocation.Host+parsedLocation.Path)
	assert.Equal(t, server.URL+"/identity-providers/"+provider.ID.String()+"/callback", parsedLocation.Query().Get("redirect_uri"))
	var stateCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == stateCookieName {
			stateCookie = cookie
		}
	}
	require.NotNil(t, stateCookie)
	require.NoError(t, response.Body.Close())

	state, err := openState(env.keys, stateCookie.Value)
	require.NoError(t, err)
	assert.Equal(t, "https://app.example/", state.ReturnURL)

	// A reverse-proxy HTTPS prefix flows through to the IdP redirect_uri.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, startURL, nil)
	require.NoError(t, err)
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err = client.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, response.StatusCode)
	parsedLocation, err = url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(parsedLocation.Query().Get("redirect_uri"), "https://"+server.Listener.Addr().String()))
	require.NoError(t, response.Body.Close())

	code, returnedState := env.idp.authorize(t, client, provider.Issuer, state.CodeVerifier, state.Nonce)
	require.Equal(t, state.Nonce, returnedState)
	callbackURL := server.URL + "/identity-providers/" + provider.ID.String() + "/callback?code=" + code + "&state=" + state.Nonce
	request, err = http.NewRequestWithContext(t.Context(), http.MethodGet, callbackURL, nil)
	require.NoError(t, err)
	request.Header.Set("Cookie", stateCookieName+"="+stateCookie.Value)
	response, err = client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusFound, response.StatusCode)
	assert.Equal(t, "https://app.example/", response.Header.Get("Location"))
	var session *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == env.web.SessionCookieName() {
			session = cookie
		}
	}
	require.NotNil(t, session)
	claims, err := env.web.Authenticate(t.Context(), "", env.web.SessionCookie(session.Value))
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", claims.Email)

	// Replayed or forged state is rejected with a localized 400.
	response, err = client.Get(server.URL + "/identity-providers/" + provider.ID.String() + "/callback?code=garbage&state=wrong")
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
}

func TestFederationErrorMapping(t *testing.T) {
	for err, status := range map[error]int{
		ErrInvalidProvider:        http.StatusBadRequest,
		ErrProviderNotFound:       http.StatusNotFound,
		ErrProviderIssuerExists:   http.StatusConflict,
		ErrProviderRoleMissing:    http.StatusConflict,
		ErrProviderDisabled:       http.StatusForbidden,
		ErrProvisioningDisabled:   http.StatusForbidden,
		ErrPrincipalInactive:      http.StatusForbidden,
		ErrProvisioningUnverified: http.StatusBadRequest,
		ErrOIDCDiscovery:          http.StatusBadRequest,
		ErrOIDCToken:              http.StatusBadRequest,
		ErrOIDCTokenInvalid:       http.StatusBadRequest,
		ErrOIDCState:              http.StatusBadRequest,
		errReplayUnavailable:      http.StatusServiceUnavailable,
		ErrSAMLResponse:           http.StatusBadRequest,
	} {
		mapped := federationError(err)
		var statusError huma.StatusError
		require.ErrorAs(t, mapped, &statusError)
		assert.Equal(t, status, statusError.GetStatus())
	}
	var statusError huma.StatusError
	require.ErrorAs(t, federationError(assert.AnError), &statusError)
	assert.Equal(t, http.StatusInternalServerError, statusError.GetStatus())
}

type callbackReturnURLWeb struct {
	authnextWebService
	base string
	err  error
}

func (w callbackReturnURLWeb) ResolveReturnURL(string) (string, error) {
	return w.base, w.err
}

func TestFederationCallbackOwnershipRecoveryRedirect(t *testing.T) {
	env := newFederationEnvironment(t)
	for _, test := range []struct {
		name   string
		accept string
		base   string
		err    error
		status int
		target string
	}{
		{"browser", "text/html,application/xhtml+xml", "https://app.example/", nil, http.StatusSeeOther, "https://app.example/login?federation_error=identity_unavailable"},
		{"deployment-prefix", "text/html", "https://app.example/admin/?return_url=https://evil.example#fragment", nil, http.StatusSeeOther, "https://app.example/admin/login?federation_error=identity_unavailable"},
		{"api", "application/json", "https://app.example/", nil, http.StatusConflict, ""},
		{"invalid-default", "text/html", "", assert.AnError, http.StatusConflict, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			env.service.authn = callbackReturnURLWeb{authnextWebService: env.web, base: test.base, err: test.err}
			router := chi.NewRouter()
			api := humachi.New(router, huma.DefaultConfig("ownership denial", "1.0.0"))
			operation := huma.Operation{OperationID: "denial", Method: http.MethodGet, Path: "/denial"}
			api.Adapter().Handle(&operation, func(ctx huma.Context) {
				ctx.AppendHeader("Set-Cookie", env.service.StateClearCookie())
				writeLoginCallbackError(api, ctx, env.service, ErrIdentityOwnershipRequired)
			})
			request := httptest.NewRequest(http.MethodGet, "/denial?return_url=https://evil.example", nil)
			request.Header.Set("Accept", test.accept)
			request.Header.Set("Referer", "https://evil.example")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code)
			assert.Equal(t, test.target, response.Header().Get("Location"))
			assert.Contains(t, response.Header().Get("Set-Cookie"), "Max-Age=0")
			assert.NotContains(t, response.Header().Get("Set-Cookie"), env.web.SessionCookieName()+"=")
		})
	}
}

func TestProviderInputFromBody(t *testing.T) {
	body := providerBody{Name: "X", ProviderType: ProviderOIDC, Issuer: "https://x.example", ClientID: "c", ClientSecret: "s", Scopes: "openid", AutoProvision: true, DefaultRoleID: wireID("r"), Status: ProviderActive}
	input, err := providerInputFromBody(body)
	require.NoError(t, err)
	assert.Equal(t, ProviderInput{Name: "X", ProviderType: ProviderOIDC, Issuer: "https://x.example", ClientID: "c", ClientSecret: "s", Scopes: "openid", AutoProvision: true, DefaultRoleID: testID("r"), Status: ProviderActive}, input)
}

func TestFederationHTTPRejectsInvalidIdentifiersAndMissingProviders(t *testing.T) {
	env := newFederationEnvironment(t)
	client, server := newManagementAPI(t, env)
	validBody := []byte(`{"name":"Provider","issuer":"https://issuer.example","client_id":"client","auto_provision":true,"status":"active"}`)
	invalidRoleBody := []byte(`{"name":"Provider","issuer":"https://issuer.example","client_id":"client","auto_provision":true,"default_role_id":"invalid","status":"active"}`)
	tests := []struct {
		method   string
		path     string
		tenantID string
		body     []byte
		status   int
	}{
		// Collections are tenant-nested, so an unparseable tenant never reaches
		// the handler: the registrar rejects it as forbidden. Providers are
		// addressed globally and carry no tenant segment, so their rejection is
		// the unparseable provider ID, which the owner resolver reports as 422.
		// A provider ID that parses but does not exist is a 404 and is covered
		// by the workflow test.
		{http.MethodGet, "/identity-providers", "invalid", nil, http.StatusForbidden},
		{http.MethodPost, "/identity-providers", "invalid", validBody, http.StatusForbidden},
		{http.MethodPost, "/identity-providers", wireID("tenant-a"), invalidRoleBody, http.StatusUnprocessableEntity},
		{http.MethodPut, "/identity-providers/invalid", "", validBody, http.StatusUnprocessableEntity},
		{http.MethodDelete, "/identity-providers/invalid", "", nil, http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		response := adminRequest(t, client, test.method, server.URL+test.path, test.tenantID, test.body, nil)
		assert.Equal(t, test.status, response.StatusCode, "%s %s", test.method, test.path)
		_ = response.Body.Close()
	}

	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, RegisterI18n())
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("federation invalid route test", "1.0.0"))
	RegisterBrowserREST(api, env.service)
	browser := httptest.NewServer(router)
	t.Cleanup(browser.Close)
	noRedirect := noRedirectClient()
	for _, test := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/identity-providers/invalid/authorization", "", http.StatusUnprocessableEntity},
		{http.MethodGet, "/identity-providers/invalid/callback?code=x&state=y", "", http.StatusUnprocessableEntity},
		{http.MethodPost, "/identity-providers/invalid/callback", "SAMLResponse=x", http.StatusUnprocessableEntity},
		{http.MethodGet, "/identity-providers/" + wireID("missing") + "/authorization", "", http.StatusNotFound},
		{http.MethodPost, "/identity-providers/" + wireID("missing") + "/callback", "SAMLResponse=x", http.StatusNotFound},
	} {
		request, err := http.NewRequestWithContext(t.Context(), test.method, browser.URL+test.path, strings.NewReader(test.body))
		require.NoError(t, err)
		if test.body != "" {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response, err := noRedirect.Do(request)
		require.NoError(t, err)
		assert.Equal(t, test.status, response.StatusCode, "%s %s", test.method, test.path)
		_ = response.Body.Close()
	}
}
