package federation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSAMLAdminAPI(t *testing.T, env *federationEnvironment) (*http.Client, *httptest.Server) {
	t.Helper()
	require.NoError(t, i18n.NewModule(nil, i18n.Base).Initialize(t.Context()))
	require.NoError(t, RegisterI18n())
	respx.Install()
	router := chi.NewRouter()
	router.Use(respx.Timing, respx.Locale)
	config := huma.DefaultConfig("federation saml admin test", "1.0.0")
	config.Transformers = append(config.Transformers, respx.LocalizeMessage)
	api := humachi.New(router, config)
	RegisterSAMLAdminREST(api, env.service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
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
	response := adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/service-providers", wireID("tenant-a"), createBody, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	var registered SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &registered))
	assert.Equal(t, "portal", registered.Name)
	assert.Equal(t, tsp.entityID, registered.EntityID)

	response = adminRequest(t, client, http.MethodGet, server.URL+"/iam/saml/service-providers", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	var items []SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &items))
	require.Len(t, items, 1)

	response = adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/service-providers", wireID("tenant-a"), createBody, map[string]string{"Accept-Language": "zh-CN"})
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("zh-CN", "federation.saml_sp_entity_id_exists"), envelope.Message)

	invalidBody := `{"name":"bad","entity_id":"https://bad.example/metadata","metadata_xml":"<not-xml"}`
	response = adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/service-providers", wireID("tenant-a"), []byte(invalidBody), nil)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_invalid_sp"), envelope.Message)

	other := newSAMLSPServer(t)
	updateBody, err := json.Marshal(samlSPBody{Name: "portal-v2", EntityID: other.entityID, MetadataXML: other.metadataXML(), Status: ProviderDisabled})
	require.NoError(t, err)
	response = adminRequest(t, client, http.MethodPut, server.URL+"/iam/saml/service-providers/"+registered.ID.String(), wireID("tenant-a"), updateBody, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	var updated SAMLServiceProvider
	require.NoError(t, json.Unmarshal(envelope.Data, &updated))
	assert.Equal(t, "portal-v2", updated.Name)
	assert.Equal(t, ProviderDisabled, updated.Status)

	response = adminRequest(t, client, http.MethodDelete, server.URL+"/iam/saml/service-providers/"+registered.ID.String(), wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, response.Body.Close())
	response = adminRequest(t, client, http.MethodDelete, server.URL+"/iam/saml/service-providers/"+registered.ID.String(), wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_sp_not_found"), envelope.Message)
}

func TestSAMLSigningKeyRotationHTTP(t *testing.T) {
	env := newFederationEnvironment(t)
	startSAML(t, env, SAMLConfig{Enabled: true})
	client, server := newSAMLAdminAPI(t, env)
	firstCert := env.service.samlCert.Raw

	response := adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/signing-key/rotate", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	var info SAMLKeyInfo
	require.NoError(t, json.Unmarshal(envelope.Data, &info))
	assert.NotEmpty(t, info.KeyID)
	assert.NotEqual(t, firstCert, env.service.samlCert.Raw)
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

	response := adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/signing-key/rotate", wireID("tenant-a"), nil, nil)
	assert.Equal(t, http.StatusConflict, response.StatusCode)
	var envelope federationEnvelope
	decodeHTTPBody(t, response, &envelope)
	assert.Equal(t, localized("en-US", "federation.saml_key_managed_externally"), envelope.Message)
}

func TestSAMLRotationUnavailableWhenDisabled(t *testing.T) {
	env := newFederationEnvironment(t)
	client, server := newSAMLAdminAPI(t, env)
	response := adminRequest(t, client, http.MethodPost, server.URL+"/iam/saml/signing-key/rotate", wireID("tenant-a"), nil, nil)
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
		body     []byte
	}{
		{http.MethodGet, "/iam/saml/service-providers", "invalid", nil},
		{http.MethodPost, "/iam/saml/service-providers", "invalid", body},
		{http.MethodPut, "/iam/saml/service-providers/" + wireID("missing"), "invalid", body},
		{http.MethodPut, "/iam/saml/service-providers/invalid", wireID("tenant-a"), body},
		{http.MethodDelete, "/iam/saml/service-providers/" + wireID("missing"), "invalid", nil},
		{http.MethodDelete, "/iam/saml/service-providers/invalid", wireID("tenant-a"), nil},
	} {
		response := adminRequest(t, client, test.method, server.URL+test.path, test.tenantID, test.body, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, response.StatusCode, "%s %s", test.method, test.path)
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
