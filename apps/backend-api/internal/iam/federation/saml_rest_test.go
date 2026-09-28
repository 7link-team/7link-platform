package federation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSAMLAdminAPI(t *testing.T, env *federationEnvironment, registrars ...*authz.Registrar) (*http.Client, *httptest.Server) {
	t.Helper()
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, RegisterI18n())
	// respx owns the message the guard layer reports, so its bundle has to be
	// registered for an owner-resolver refusal to read as a sentence rather
	// than as a bare key.
	if err := respx.NewModule(nil).Initialize(t.Context()); err != nil {
		require.ErrorContains(t, err, "already registered")
	}
	respx.Install()
	router := chi.NewRouter()
	router.Use(respx.Timing, respx.Locale)
	config := huma.DefaultConfig("federation saml admin test", "1.0.0")
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	api := humachi.New(router, config)
	registrar := federationRuntimeRegistrar(federationAdminVerifier{})
	if len(registrars) > 0 {
		registrar = registrars[0]
	}
	RegisterSAMLAdminREST(api, env.service, registrar)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server.Client(), server
}

func TestSAMLServiceProviderHTTPWorkflow(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	client, server := newSAMLAdminAPI(t, env)
	tsp := newSAMLSPServer(t)

	createBody, err := json.Marshal(samlSPBody{Name: "portal", EntityID: tsp.entityID, MetadataXML: tsp.metadataXML()})
	require.NoError(t, err)
	response := adminRequest(t, client, http.MethodPost, server.URL+"/saml-service-providers", wireID("tenant-a"), createBody, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	var registered SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &registered))
	assert.Equal(t, "portal", registered.Name)
	assert.Equal(t, tsp.entityID, registered.EntityID)

	response = adminRequest(t, client, http.MethodGet, server.URL+"/saml-service-providers", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	var items []SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &items))
	require.Len(t, items, 1)

	response = adminRequest(t, client, http.MethodPost, server.URL+"/saml-service-providers", wireID("tenant-a"), createBody, map[string]string{"Accept-Language": "zh-CN"})
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("zh-CN", "federation.saml_sp_entity_id_exists"), envelope.Message)

	invalidBody := `{"name":"bad","entity_id":"https://bad.example/metadata","metadata_xml":"<not-xml"}`
	response = adminRequest(t, client, http.MethodPost, server.URL+"/saml-service-providers", wireID("tenant-a"), []byte(invalidBody), nil)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_invalid_sp"), envelope.Message)

	other := newSAMLSPServer(t)
	updateBody, err := json.Marshal(samlSPBody{Name: "portal-v2", EntityID: other.entityID, MetadataXML: other.metadataXML(), Status: ProviderDisabled})
	require.NoError(t, err)
	response = adminRequest(t, client, http.MethodPut, server.URL+"/saml-service-providers/"+registered.ID.String(), "", updateBody, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	var updated SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &updated))
	assert.Equal(t, "portal-v2", updated.Name)
	assert.Equal(t, ProviderDisabled, updated.Status)

	response = adminRequest(t, client, http.MethodDelete, server.URL+"/saml-service-providers/"+registered.ID.String(), "", nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, response.Body.Close())
	// The second delete is answered by the owner resolver, which runs ahead of
	// the handler and reports an absent globally addressed resource itself.
	// The message still has to reach the caller as localized prose: the guard
	// layer reports respx's key, not a bare code that would surface raw.
	response = adminRequest(t, client, http.MethodDelete, server.URL+"/saml-service-providers/"+registered.ID.String(), "", nil, nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", respx.NotFound), envelope.Message)
	assert.NotEqual(t, respx.NotFound, envelope.Message, "the key must resolve, not pass through")
}

func TestSAMLSigningKeyRotationHTTP(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	client, server := newSAMLAdminAPI(t, env)
	firstCert := env.service.samlCert.Raw

	response := adminRequest(t, client, http.MethodPost, server.URL+"/system/saml-signing-keys", "", nil, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	var info SAMLKeyInfo
	require.NoError(t, json.Unmarshal(envelope.Data, &info))
	assert.NotEmpty(t, info.KeyID)
	assert.NotEqual(t, firstCert, env.service.samlCert.Raw)
	event := env.events[len(env.events)-1]
	assert.Zero(t, event.TenantID)
	assert.Equal(t, guid.ID(1), event.PrincipalID)
	assert.Equal(t, "federation_saml_signing_key_rotated", event.EventType)
	assert.Equal(t, "platform", event.Detail["scope"])
	assert.Equal(t, info.KeyID, event.Detail["key_id"])
}

type tenantOnlySAMLPermissionChecker struct{ federationPermissionChecker }

func (tenantOnlySAMLPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}

func TestSAMLSigningKeyRotationRejectsTenantAuthority(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	firstCert := env.service.samlCert.Raw
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), federationAdminVerifier{}, tenantOnlySAMLPermissionChecker{}, federationMembershipChecker{})
	client, server := newSAMLAdminAPI(t, env, registrar)
	for _, path := range []string{"/system/saml-signing-keys", "/tenants/" + wireID("tenant-a") + "/saml-signing-keys"} {
		response := adminRequest(t, client, http.MethodPost, server.URL+path, "", nil, nil)
		if strings.HasPrefix(path, "/system/") {
			assert.Equal(t, http.StatusForbidden, response.StatusCode)
		} else {
			assert.Equal(t, http.StatusNotFound, response.StatusCode)
		}
		require.NoError(t, response.Body.Close())
	}
	assert.Equal(t, firstCert, env.service.samlCert.Raw)
	assert.Empty(t, env.events)
	var count int64
	require.NoError(t, env.db.Table("iam_saml_idp_keys").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.NoError(t, env.service.StartSAML(t.Context(), SAMLConfig{Enabled: true}))
	assert.Equal(t, firstCert, env.service.samlCert.Raw)
}

func TestSAMLSigningKeyRotationAuditFailureRollsBack(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	firstCert := env.service.samlCert.Raw
	env.auditErr = errors.New("audit unavailable")
	_, err := env.service.RotateSAMLSigningKey(t.Context())
	require.ErrorContains(t, err, "audit unavailable")
	assert.Equal(t, firstCert, env.service.samlCert.Raw)
	var count int64
	require.NoError(t, env.db.Table("iam_saml_idp_keys").Count(&count).Error)
	assert.Equal(t, int64(1), count)
	require.NoError(t, env.db.Table("iam_saml_idp_keys").Where("rotated_at IS NOT NULL").Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, env.service.StartSAML(t.Context(), SAMLConfig{Enabled: true}))
	assert.Equal(t, firstCert, env.service.samlCert.Raw)
}

func TestSAMLSigningKeyRotationFileBackedConflict(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	keyFile := t.TempDir() + "/saml.key"
	certFile := t.TempDir() + "/saml.crt"
	require.NoError(t, os.WriteFile(keyFile, pemEncodeKey(t, env.service.samlKey), 0o600))
	require.NoError(t, os.WriteFile(certFile, pemEncodeCert(t, env.service.samlCert), 0o600))
	require.NoError(t, env.service.StartSAML(t.Context(), SAMLConfig{Enabled: true, SigningKeyFile: keyFile, CertificateFile: certFile}))
	client, server := newSAMLAdminAPI(t, env)

	response := adminRequest(t, client, http.MethodPost, server.URL+"/system/saml-signing-keys", "", nil, nil)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_key_managed_externally"), envelope.Message)
}

func TestSAMLRotationUnavailableWhenDisabled(t *testing.T) {
	env := newFederationEnvironment(t)
	client, server := newSAMLAdminAPI(t, env)
	response := adminRequest(t, client, http.MethodPost, server.URL+"/system/saml-signing-keys", "", nil, nil)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_unavailable"), envelope.Message)
}

func TestSAMLHTTPRejectsInvalidTenantAndServiceProviderIDs(t *testing.T) {
	env := newFederationEnvironment(t)
	client, server := newSAMLAdminAPI(t, env)
	tsp := newSAMLSPServer(t)
	body, err := json.Marshal(samlSPBody{Name: "portal", EntityID: tsp.entityID, MetadataXML: tsp.metadataXML()})
	require.NoError(t, err)
	for _, test := range []struct {
		method   string
		path     string
		tenantID string
		status   int
		body     []byte
	}{
		// The collection keeps its tenant segment, so the registrar rejects an
		// unparseable tenant as forbidden before the handler runs. Service
		// providers are globally addressed, so their rejection is the
		// unparseable service provider ID reported by the owner resolver.
		{http.MethodGet, "/saml-service-providers", "invalid", http.StatusForbidden, nil},
		{http.MethodPost, "/saml-service-providers", "invalid", http.StatusForbidden, body},
		{http.MethodPut, "/saml-service-providers/invalid", "", http.StatusUnprocessableEntity, body},
		{http.MethodDelete, "/saml-service-providers/invalid", "", http.StatusUnprocessableEntity, nil},
	} {
		response := adminRequest(t, client, test.method, server.URL+test.path, test.tenantID, test.body, nil)
		assert.Equal(t, test.status, response.StatusCode, "%s %s", test.method, test.path)
		_ = response.Body.Close()
	}

	browser := newSAMLServer(t, env)
	for _, path := range []string{
		samlSSORoute, samlSLORoute, samlMetadataRoute,
	} {
		path = strings.Replace(path, "{tenant_id}", "invalid", 1)
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if path == strings.Replace(samlMetadataRoute, "{tenant_id}", "invalid", 1) && method == http.MethodPost {
				continue
			}
			response := samlBrowserRequest(t, browser, method, browser.URL+path, "", "SAMLRequest=x")
			assert.Equal(t, http.StatusUnprocessableEntity, response.StatusCode, "%s %s", method, path)
			_ = response.Body.Close()
		}
	}
}

func TestSAMLErrorMapping(t *testing.T) {
	for err, status := range map[error]int{
		ErrSAMLSPNotFound:           http.StatusNotFound,
		ErrSAMLSPEntityIDExists:     http.StatusConflict,
		ErrSAMLKeyManagedExternally: http.StatusConflict,
		ErrSAMLUnavailable:          http.StatusConflict,
		ErrSAMLSPDisabled:           http.StatusConflict,
		ErrInvalidSAMLSP:            http.StatusBadRequest,
		ErrSAMLInvalidRequest:       http.StatusBadRequest,
		assert.AnError:              http.StatusInternalServerError,
	} {
		mapped, ok := samlError(err).(huma.StatusError)
		require.True(t, ok)
		assert.Equal(t, status, mapped.GetStatus())
	}
}
