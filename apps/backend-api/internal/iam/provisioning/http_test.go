package provisioning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	provisioningi18n "7link.com/platform/internal/iam/provisioning/i18n"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const provisioningRouteManifest = `
provisioning-create-scim-credential|POST|/scim-directories/{directory_id}/credentials
provisioning-create-scim-directory|POST|/scim-directories
provisioning-create-scim-target|POST|/scim-targets
provisioning-delete-scim-target|DELETE|/scim-targets/{target_id}
provisioning-deprovision-scim-resource|POST|/scim-targets/{target_id}/deprovisioning-jobs
provisioning-list-scim-credentials|GET|/scim-directories/{directory_id}/credentials
provisioning-list-scim-directories|GET|/scim-directories
provisioning-list-scim-targets|GET|/scim-targets
provisioning-push-scim-resource|POST|/scim-targets/{target_id}/provisioning-jobs
provisioning-replace-scim-directory|PUT|/scim-directories/{directory_id}
provisioning-replace-scim-target|PUT|/scim-targets/{target_id}
provisioning-retire-scim-target|PUT|/scim-targets/{target_id}/retirement
provisioning-revoke-scim-credential|DELETE|/scim-directories/{directory_id}/credentials/{credential_id}
scim-bulk|POST|/scim/v2/Bulk
scim-create-group|POST|/scim/v2/Groups
scim-create-user|POST|/scim/v2/Users
scim-delete-group|DELETE|/scim/v2/Groups/{id}
scim-delete-user|DELETE|/scim/v2/Users/{id}
scim-get-group|GET|/scim/v2/Groups/{id}
scim-get-resource-type|GET|/scim/v2/ResourceTypes/{id}
scim-get-schema|GET|/scim/v2/Schemas/{id}
scim-get-user|GET|/scim/v2/Users/{id}
scim-list-groups|GET|/scim/v2/Groups
scim-list-resource-types|GET|/scim/v2/ResourceTypes
scim-list-schemas|GET|/scim/v2/Schemas
scim-list-users|GET|/scim/v2/Users
scim-patch-group|PATCH|/scim/v2/Groups/{id}
scim-patch-user|PATCH|/scim/v2/Users/{id}
scim-replace-group|PUT|/scim/v2/Groups/{id}
scim-replace-user|PUT|/scim/v2/Users/{id}
scim-service-provider-config|GET|/scim/v2/ServiceProviderConfig`

type provisioningHTTPHarness struct {
	env    *provisioningEnvironment
	api    huma.API
	router http.Handler
}

type adminEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newProvisioningHTTPHarness(t *testing.T) provisioningHTTPHarness {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, provisioningi18n.Register())
	env := newProvisioningEnvironment(t)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("provisioning-test", "1"))
	RegisterHTTP(api, env.service, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	return provisioningHTTPHarness{env: env, api: api, router: router}
}

func provisioningPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	}
}

func provisioningOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 30)
	for path, item := range spec.Paths {
		for method, operation := range provisioningPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func TestProvisioningRouteManifestAndOpenAPIContract(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	spec := h.api.OpenAPI()
	want, got := strings.Fields(provisioningRouteManifest), provisioningOperationManifest(spec)
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)

	securedSCIM := map[string]bool{
		"scim-bulk": true, "scim-list-users": true, "scim-create-user": true,
		"scim-get-user": true, "scim-replace-user": true, "scim-patch-user": true, "scim-delete-user": true,
		"scim-list-groups": true, "scim-create-group": true, "scim-get-group": true,
		"scim-replace-group": true, "scim-patch-group": true, "scim-delete-group": true,
	}
	expectedSuccess := map[string]string{
		"scim-bulk": "200", "scim-create-user": "201", "scim-create-group": "201",
		"scim-delete-user": "204", "scim-delete-group": "204",
	}
	expectedParameters := map[string][]string{
		"scim-list-users": {"count", "filter", "startIndex"}, "scim-list-groups": {"count", "filter", "startIndex"},
		"scim-get-user": {"id"}, "scim-replace-user": {"id"}, "scim-patch-user": {"id"}, "scim-delete-user": {"id"},
		"scim-get-group": {"id"}, "scim-replace-group": {"id"}, "scim-patch-group": {"id"}, "scim-delete-group": {"id"},
		"scim-get-schema": {"id"}, "scim-get-resource-type": {"id"},
	}
	requestOperations := map[string]bool{
		"scim-bulk": true, "scim-create-user": true, "scim-replace-user": true, "scim-patch-user": true,
		"scim-create-group": true, "scim-replace-group": true, "scim-patch-group": true,
	}

	for path, item := range spec.Paths {
		for _, operation := range provisioningPathOperations(item) {
			if operation == nil {
				continue
			}
			if strings.HasPrefix(path, "/scim-directories") || strings.HasPrefix(path, "/scim-targets") {
				assert.Equal(t, "tenant_administer", operation.Extensions[authz.GuardExtensionKey], operation.OperationID)
				assert.Equal(t, []map[string][]string{{authz.BearerScheme: {}}, {authz.SessionScheme: {}}}, operation.Security, operation.OperationID)
				continue
			}
			require.True(t, strings.HasPrefix(path, "/scim/v2/"), operation.OperationID)
			if securedSCIM[operation.OperationID] {
				assert.Equal(t, []map[string][]string{{scimBearerScheme: {}}}, operation.Security, operation.OperationID)
				assert.Contains(t, operation.Responses, "401", operation.OperationID)
			} else {
				assert.Empty(t, operation.Security, operation.OperationID)
			}

			var parameterNames []string
			for _, parameter := range operation.Parameters {
				parameterNames = append(parameterNames, parameter.Name)
			}
			sort.Strings(parameterNames)
			assert.Equal(t, expectedParameters[operation.OperationID], parameterNames, operation.OperationID)
			if requestOperations[operation.OperationID] {
				require.NotNil(t, operation.RequestBody, operation.OperationID)
				require.Len(t, operation.RequestBody.Content, 1, operation.OperationID)
				assert.Contains(t, operation.RequestBody.Content, SCIMContentType, operation.OperationID)
			} else {
				assert.Nil(t, operation.RequestBody, operation.OperationID)
			}

			successStatus := expectedSuccess[operation.OperationID]
			if successStatus == "" {
				successStatus = "200"
			}
			assert.Contains(t, operation.Responses, successStatus, operation.OperationID)
			for _, response := range operation.Responses {
				if response != nil && response.Content != nil {
					require.Len(t, response.Content, 1, operation.OperationID)
					assert.Contains(t, response.Content, SCIMContentType, operation.OperationID)
				}
			}
		}
	}
}

func (h provisioningHTTPHarness) request(t *testing.T, method, path string, body []byte, tenant, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		contentType := "application/json"
		if strings.HasPrefix(path, "/scim/") {
			contentType = SCIMContentType
		}
		request.Header.Set("Content-Type", contentType)
	}
	if tenant != "" {
		request.Header.Set(authz.TenantHeader, tenant)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	h.router.ServeHTTP(recorder, request)
	return recorder
}

func decodeRecorder(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(target), recorder.Body.String())
}

func TestSCIMHTTPUserGroupAndBulkWorkflow(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	userBody, err := json.Marshal(activeUserInput("http-user", "http-user", "http@example.test"))
	require.NoError(t, err)

	unauthorized := h.request(t, http.MethodGet, "/scim/v2/Users", nil, "", "", nil)
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.Equal(t, SCIMContentType, unauthorized.Header().Get("Content-Type"))

	created := h.request(t, http.MethodPost, "/scim/v2/Users", userBody, "", h.env.token, nil)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var user UserResource
	decodeRecorder(t, created, &user)
	assert.Equal(t, weakETag(1), created.Header().Get("ETag"))

	for _, target := range []string{"/scim/v2/Users", "/scim/v2/Users/" + user.ID} {
		response := h.request(t, http.MethodGet, target, nil, "", h.env.token, nil)
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	updatedInput := activeUserInput("http-user", "http-user", "updated@example.test")
	updatedInput.DisplayName = "HTTP Updated"
	updatedBody, err := json.Marshal(updatedInput)
	require.NoError(t, err)
	response := h.request(t, http.MethodPut, "/scim/v2/Users/"+user.ID, updatedBody, "", h.env.token, map[string]string{"If-Match": weakETag(1)})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	patchBody := []byte(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"replace","path":"displayName","value":"HTTP Patched"}]}`)
	response = h.request(t, http.MethodPatch, "/scim/v2/Users/"+user.ID, patchBody, "", h.env.token, map[string]string{"If-Match": weakETag(2)})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	groupBody, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, DisplayName: "HTTP Group", Members: []SCIMGroupMember{{Value: user.ID}}})
	require.NoError(t, err)
	response = h.request(t, http.MethodPost, "/scim/v2/Groups", groupBody, "", h.env.token, nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var group GroupResource
	decodeRecorder(t, response, &group)
	for _, target := range []string{"/scim/v2/Groups", "/scim/v2/Groups/" + group.ID} {
		response = h.request(t, http.MethodGet, target, nil, "", h.env.token, nil)
		assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	groupBody, err = json.Marshal(GroupInput{Schemas: []string{GroupSchema}, DisplayName: "HTTP Group Updated", Members: []SCIMGroupMember{{Value: user.ID}}})
	require.NoError(t, err)
	response = h.request(t, http.MethodPut, "/scim/v2/Groups/"+group.ID, groupBody, "", h.env.token, map[string]string{"If-Match": weakETag(1)})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = h.request(t, http.MethodPatch, "/scim/v2/Groups/"+group.ID, patchBody, "", h.env.token, map[string]string{"If-Match": weakETag(2)})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	bulkUser, err := json.Marshal(activeUserInput("bulk-http", "bulk-http", "bulk-http@example.test"))
	require.NoError(t, err)
	bulkBody, err := json.Marshal(BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{{Method: http.MethodPost, BulkID: "bulk-user", Path: "/Users", Data: bulkUser}}})
	require.NoError(t, err)
	response = h.request(t, http.MethodPost, "/scim/v2/Bulk", bulkBody, "", h.env.token, nil)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())

	response = h.request(t, http.MethodDelete, "/scim/v2/Groups/"+group.ID, nil, "", h.env.token, map[string]string{"If-Match": weakETag(3)})
	assert.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	response = h.request(t, http.MethodDelete, "/scim/v2/Users/"+user.ID, nil, "", h.env.token, map[string]string{"If-Match": weakETag(3)})
	assert.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
}

func TestSCIMDiscoveryAndProtocolErrors(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	for _, target := range []string{
		"/scim/v2/ServiceProviderConfig", "/scim/v2/Schemas", "/scim/v2/Schemas/" + UserSchema,
		"/scim/v2/ResourceTypes", "/scim/v2/ResourceTypes/User",
	} {
		response := h.request(t, http.MethodGet, target, nil, "", "", nil)
		assert.Equal(t, http.StatusOK, response.Code, target+" "+response.Body.String())
		assert.Equal(t, SCIMContentType, response.Header().Get("Content-Type"))
	}
	for _, target := range []string{"/scim/v2/Schemas/missing", "/scim/v2/ResourceTypes/missing"} {
		missing := h.request(t, http.MethodGet, target, nil, "", "", nil)
		assert.Equal(t, http.StatusNotFound, missing.Code)
	}
	for _, target := range []string{"/scim/v2/Users/not-an-id", "/scim/v2/Groups/not-an-id"} {
		invalid := h.request(t, http.MethodGet, target, nil, "", h.env.token, nil)
		assert.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
		var protocolError SCIMError
		decodeRecorder(t, invalid, &protocolError)
		assert.Equal(t, "invalidSyntax", protocolError.ScimType)
	}

	userBody, err := json.Marshal(activeUserInput("missing", "missing", "missing@example.test"))
	require.NoError(t, err)
	groupBody, err := json.Marshal(GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Missing"})
	require.NoError(t, err)
	patchBody := []byte(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"replace","path":"displayName","value":"Changed"}]}`)
	bulkBody := []byte(`{"schemas":["` + BulkSchema + `"],"Operations":[]}`)
	tests := []struct {
		method, path string
		body         []byte
		headers      map[string]string
		status       int
	}{
		{http.MethodGet, "/scim/v2/Users?startIndex=bad", nil, nil, http.StatusBadRequest},
		{http.MethodGet, "/scim/v2/Users?filter=userName%20eq", nil, nil, http.StatusBadRequest},
		{http.MethodGet, "/scim/v2/Groups?count=bad", nil, nil, http.StatusBadRequest},
		{http.MethodGet, "/scim/v2/Groups?filter=displayName%20eq", nil, nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Users", userBody, map[string]string{"Content-Type": "text/plain"}, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Users", []byte(`{"schemas":`), nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Users", []byte(`{}`), nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Groups", groupBody, map[string]string{"Content-Type": "text/plain"}, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Groups", []byte(`{"schemas":`), nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Groups", []byte(`{}`), nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Bulk", bulkBody, map[string]string{"Content-Type": "text/plain"}, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Bulk", []byte(`{`), nil, http.StatusBadRequest},
		{http.MethodPost, "/scim/v2/Bulk", []byte(`{}`), nil, http.StatusBadRequest},
		{http.MethodGet, "/scim/v2/Users/" + wireID("missing"), nil, nil, http.StatusNotFound},
		{http.MethodPut, "/scim/v2/Users/not-an-id", userBody, nil, http.StatusBadRequest},
		{http.MethodPut, "/scim/v2/Users/" + wireID("missing"), userBody, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodPut, "/scim/v2/Users/" + wireID("missing"), []byte(`{`), nil, http.StatusBadRequest},
		{http.MethodPut, "/scim/v2/Users/" + wireID("missing"), userBody, nil, http.StatusNotFound},
		{http.MethodPatch, "/scim/v2/Users/not-an-id", patchBody, nil, http.StatusBadRequest},
		{http.MethodPatch, "/scim/v2/Users/" + wireID("missing"), patchBody, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodPatch, "/scim/v2/Users/" + wireID("missing"), []byte(`{`), nil, http.StatusBadRequest},
		{http.MethodPatch, "/scim/v2/Users/" + wireID("missing"), patchBody, nil, http.StatusNotFound},
		{http.MethodDelete, "/scim/v2/Users/not-an-id", nil, nil, http.StatusBadRequest},
		{http.MethodDelete, "/scim/v2/Users/" + wireID("missing"), nil, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodDelete, "/scim/v2/Users/" + wireID("missing"), nil, nil, http.StatusNotFound},
		{http.MethodGet, "/scim/v2/Groups/" + wireID("missing"), nil, nil, http.StatusNotFound},
		{http.MethodPut, "/scim/v2/Groups/not-an-id", groupBody, nil, http.StatusBadRequest},
		{http.MethodPut, "/scim/v2/Groups/" + wireID("missing"), groupBody, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodPut, "/scim/v2/Groups/" + wireID("missing"), []byte(`{`), nil, http.StatusBadRequest},
		{http.MethodPut, "/scim/v2/Groups/" + wireID("missing"), groupBody, nil, http.StatusNotFound},
		{http.MethodPatch, "/scim/v2/Groups/not-an-id", patchBody, nil, http.StatusBadRequest},
		{http.MethodPatch, "/scim/v2/Groups/" + wireID("missing"), patchBody, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodPatch, "/scim/v2/Groups/" + wireID("missing"), []byte(`{`), nil, http.StatusBadRequest},
		{http.MethodPatch, "/scim/v2/Groups/" + wireID("missing"), patchBody, nil, http.StatusNotFound},
		{http.MethodDelete, "/scim/v2/Groups/not-an-id", nil, nil, http.StatusBadRequest},
		{http.MethodDelete, "/scim/v2/Groups/" + wireID("missing"), nil, map[string]string{"If-Match": "1"}, http.StatusPreconditionFailed},
		{http.MethodDelete, "/scim/v2/Groups/" + wireID("missing"), nil, nil, http.StatusNotFound},
	}
	for _, test := range tests {
		response := h.request(t, test.method, test.path, test.body, "", h.env.token, test.headers)
		assert.Equal(t, test.status, response.Code, test.method+" "+test.path+" "+response.Body.String())
		var protocolError SCIMError
		decodeRecorder(t, response, &protocolError)
		assert.Equal(t, fmt.Sprint(test.status), protocolError.Status)
	}
}

func TestProvisioningManagementHTTPWorkflow(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	tenant := wireID("tenant-a")
	response := h.request(t, http.MethodPost, "/scim-directories", []byte(`{"name":"Secondary"}`), tenant, "", nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var envelope adminEnvelope
	decodeRecorder(t, response, &envelope)
	var directory Directory
	require.NoError(t, json.Unmarshal(envelope.Data, &directory))
	response = h.request(t, http.MethodGet, "/scim-directories", nil, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code)

	credentialPath := "/scim-directories/" + directory.ID.String() + "/credentials"
	response = h.request(t, http.MethodPost, credentialPath, []byte(`{"name":"automation"}`), tenant, "", nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	decodeRecorder(t, response, &envelope)
	var issued CredentialSecret
	require.NoError(t, json.Unmarshal(envelope.Data, &issued))
	response = h.request(t, http.MethodGet, credentialPath, nil, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code)
	response = h.request(t, http.MethodDelete, credentialPath+"/"+issued.Credential.ID.String(), nil, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code)
	response = h.request(t, http.MethodPut, "/scim-directories/"+directory.ID.String(), []byte(`{"name":"Secondary","status":"disabled","version":1}`), tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())

	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{"id":"remote-management"}`)
	createTarget := []byte(fmt.Sprintf(`{"name":"Management","base_url":%q,"bearer_token":"secret","service_account_id":%q}`, server.URL, h.env.auth.PrincipalID.String()))
	response = h.request(t, http.MethodPost, "/scim-targets", createTarget, tenant, "", nil)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	decodeRecorder(t, response, &envelope)
	var target TargetSecret
	require.NoError(t, json.Unmarshal(envelope.Data, &target))
	response = h.request(t, http.MethodGet, "/scim-targets", nil, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code)

	user, err := h.env.service.CreateUser(t.Context(), h.env.auth, activeUserInput("management", "management", "management@example.test"))
	require.NoError(t, err)
	resourceBody := []byte(fmt.Sprintf(`{"resource_type":"User","resource_id":%q}`, user.ID))
	basePath := "/scim-targets/" + target.Target.ID
	response = h.request(t, http.MethodPost, basePath+"/provisioning-jobs", resourceBody, tenant, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, http.MethodPut, provider.last(t).Method)
	response = h.request(t, http.MethodPost, basePath+"/deprovisioning-jobs", resourceBody, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	replace := []byte(fmt.Sprintf(`{"name":"Management 2","base_url":%q,"status":"active","version":1}`, server.URL))
	response = h.request(t, http.MethodPut, basePath, replace, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = h.request(t, http.MethodDelete, basePath, nil, tenant, "", nil)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestProvisioningManagementHTTPFailures(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	tenant := wireID("tenant-a")
	missing := wireID("missing")
	tests := []struct {
		method, path string
		body         []byte
		status       int
	}{
		{http.MethodPost, "/scim-directories", []byte(`{"name":"Primary IdP"}`), http.StatusConflict},
		{http.MethodPut, "/scim-directories/" + missing, []byte(`{"name":"Missing","status":"active","version":1}`), http.StatusNotFound},
		{http.MethodGet, "/scim-directories/" + missing + "/credentials", nil, http.StatusNotFound},
		{http.MethodPost, "/scim-directories/" + missing + "/credentials", []byte(`{"name":"missing"}`), http.StatusNotFound},
		{http.MethodDelete, "/scim-directories/" + h.env.auth.DirectoryID.String() + "/credentials/" + missing, nil, http.StatusNotFound},
		{http.MethodPut, "/scim-targets/" + missing, []byte(`{"name":"Missing","base_url":"https://example.test","status":"active","version":1}`), http.StatusNotFound},
		{http.MethodDelete, "/scim-targets/" + missing, nil, http.StatusNotFound},
		{http.MethodPost, "/scim-targets/" + missing + "/provisioning-jobs", []byte(`{"resource_type":"User","resource_id":"` + missing + `"}`), http.StatusNotFound},
		{http.MethodPost, "/scim-targets/" + missing + "/deprovisioning-jobs", []byte(`{"resource_type":"User","resource_id":"` + missing + `"}`), http.StatusNotFound},
	}
	for _, test := range tests {
		response := h.request(t, test.method, test.path, test.body, tenant, "", nil)
		assert.Equal(t, test.status, response.Code, test.method+" "+test.path+" "+response.Body.String())
	}
	invalid := h.request(t, http.MethodDelete, "/scim-targets/not-an-id", nil, tenant, "", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, invalid.Code)
}

func TestProvisioningHTTPBodyLimitAndErrorMapping(t *testing.T) {
	h := newProvisioningHTTPHarness(t)
	response := h.request(t, http.MethodPost, "/scim/v2/Users", []byte(strings.Repeat("x", maxBodyBytes+1)), "", h.env.token, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	for err, status := range map[error]int{
		ErrInvalidDirectory: http.StatusUnprocessableEntity, ErrDirectoryMissing: http.StatusNotFound,
		ErrDirectoryName: http.StatusConflict, ErrDirectoryVersion: http.StatusConflict,
		ErrCredentialMissing: http.StatusNotFound, ErrCredentialLimit: http.StatusConflict,
		ErrInvalidTarget: http.StatusUnprocessableEntity, ErrTargetMissing: http.StatusNotFound,
		ErrTargetName: http.StatusConflict, ErrTargetVersion: http.StatusConflict,
		ErrTargetKeyMissing: http.StatusUnprocessableEntity, ErrTargetDisabled: http.StatusConflict,
		ErrUnauthorized:       http.StatusConflict,
		ErrDeprovisionMissing: http.StatusNotFound, ErrRemoteUnavailable: http.StatusBadGateway,
		ErrRemoteResponse: http.StatusBadGateway, assert.AnError: http.StatusInternalServerError,
	} {
		var statusError huma.StatusError
		require.ErrorAs(t, provisioningError(err), &statusError)
		assert.Equal(t, status, statusError.GetStatus())
	}
}
