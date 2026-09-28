package authn

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authnRouteManifest = `
authn-create-account-view|POST|/account-views
authn-delete-account-view|DELETE|/account-views/{account_view_id}
authn-create-personal-token-preparation|POST|/principals/{principal_id}/personal-token-preparations
authn-create-personal-token|POST|/principals/{principal_id}/personal-tokens
authn-delete-personal-token|DELETE|/principals/{principal_id}/personal-tokens/{token_id}
authn-list-personal-tokens|GET|/principals/{principal_id}/personal-tokens
authn-list-personal-token-permissions|GET|/principals/{principal_id}/personal-token-permissions
authn-list-reauthentication-methods|GET|/principals/{principal_id}/reauthentication/methods
authn-create-reauthentication-challenge|POST|/principals/{principal_id}/reauthentication/challenges
authn-create-reauthentication-verification|POST|/principals/{principal_id}/reauthentication/verifications
authn-login-options|GET|/login/options
authn-create-registration-captcha|POST|/registrations/captchas
authn-create-email-verification-request|POST|/email-verifications/requests
authn-create-email-verification|POST|/email-verifications
authn-create-passkey-registration-challenge|POST|/principals/{principal_id}/passkey-registration-challenges
authn-create-passkey-login-options|POST|/login/passkey/options
authn-verify-passkey-login|POST|/login/passkey/verify
authn-create-passkey|POST|/principals/{principal_id}/passkeys
authn-create-password-recovery-request|POST|/password-recoveries/requests
authn-create-password-recovery|POST|/password-recoveries
authn-create-registration|POST|/registrations
authn-create-session-verification|POST|/login/mfa
authn-create-session|POST|/login
authn-create-step-up-challenge|POST|/sessions/current/step-up-challenges
authn-create-step-up-verification|POST|/sessions/current/step-up-verifications
authn-create-totp-enrollment|POST|/principals/{principal_id}/mfa/totp-enrollments
authn-create-totp-factor|POST|/principals/{principal_id}/mfa/totp
authn-delete-current-session|DELETE|/sessions/current
authn-delete-passkey|DELETE|/principals/{principal_id}/passkeys/{passkey_id}
authn-delete-session|DELETE|/sessions/{session_id}
authn-delete-sessions|DELETE|/principals/{principal_id}/sessions
authn-delete-totp-factor|DELETE|/principals/{principal_id}/mfa/totp
authn-get-current-session|GET|/sessions/current
authn-get-session|GET|/sessions/{session_id}
authn-get-mfa|GET|/principals/{principal_id}/mfa
authn-list-passkeys|GET|/principals/{principal_id}/passkeys
authn-list-sessions|GET|/principals/{principal_id}/sessions
authn-me|GET|/me
authn-replace-recovery-codes|PUT|/principals/{principal_id}/mfa/recovery-codes
authn-update-passkey|PATCH|/principals/{principal_id}/passkeys/{passkey_id}
authn-update-password|PATCH|/principals/{principal_id}/password`

func authnContractAPI(t *testing.T) humatest.TestAPI {
	t.Helper()
	respx.Install()
	service, _ := newLocalService(t)
	api := humatest.Wrap(t, humax.New(chi.NewRouter(), humax.DefaultConfig("authn-contract", "1")))
	RegisterHTTP(api, service, service)
	return api
}

func TestAuthnRouteManifestMatchesGeneratedOpenAPI(t *testing.T) {
	api := authnContractAPI(t)
	want := strings.Fields(authnRouteManifest)
	got := authnOperationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 41)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))
}

func TestAuthnOpenAPIPathsParametersAndSecurityAreConsistent(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	operationIDs := make(map[string]string, 30)
	pathParameter := regexp.MustCompile(`\{([^}]+)\}`)
	secured := map[string]bool{
		"authn-me": true, "authn-get-session": true,
		"authn-list-sessions": true, "authn-delete-session": true, "authn-delete-sessions": true,
		"authn-update-password": true, "authn-create-email-verification-request": true,
		"authn-get-mfa": true, "authn-create-totp-enrollment": true,
		"authn-create-totp-factor": true, "authn-replace-recovery-codes": true,
		"authn-delete-totp-factor": true, "authn-create-step-up-challenge": true,
		"authn-create-step-up-verification": true, "authn-list-passkeys": true,
		"authn-create-passkey-registration-challenge": true,
		"authn-create-passkey":                        true, "authn-update-passkey": true,
		"authn-delete-passkey": true,
	}
	sessionOnly := map[string]bool{
		"authn-create-account-view": true, "authn-delete-account-view": true,
		"authn-get-current-session":               true,
		"authn-create-reauthentication-challenge": true, "authn-create-reauthentication-verification": true,
		"authn-list-reauthentication-methods": true, "authn-create-personal-token-preparation": true,
		"authn-create-personal-token": true, "authn-delete-personal-token": true,
		"authn-list-personal-tokens": true, "authn-list-personal-token-permissions": true,
	}

	for path, item := range spec.Paths {
		assert.Equal(t, strings.ToLower(path), path, "paths use lowercase resource names")
		assert.False(t, strings.HasSuffix(path, "/"), "paths have no trailing slash")
		segments := strings.Split(strings.Trim(path, "/"), "/")
		if segments[0] != "login" {
			for _, segment := range segments {
				assert.NotContains(t, []string{"login", "logout", "start", "complete", "options", "verify", "confirm", "regenerate", "enroll", "change"}, segment, path)
			}
		}
		for method, operation := range authnPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			require.NotEmpty(t, operation.OperationID, location)
			if previous, duplicate := operationIDs[operation.OperationID]; duplicate {
				t.Fatalf("duplicate operationId %q on %s and %s", operation.OperationID, previous, location)
			}
			operationIDs[operation.OperationID] = location
			// This module either establishes a session or manages the account
			// behind one. Publishing anywhere else would mean a route landed
			// in the wrong module.
			assert.Equal(t, []string{segments[0]}, operation.Tags, location)
			if sessionOnly[operation.OperationID] {
				assert.Equal(t, []map[string][]string{{authz.SessionScheme: {}}}, operation.Security, location)
			} else if secured[operation.OperationID] {
				assert.Equal(t, authz.UserSecurity(), operation.Security, location)
			} else {
				assert.Empty(t, operation.Security, location)
			}

			declared := map[string]*huma.Param{}
			for _, parameter := range operation.Parameters {
				key := parameter.In + "\x00" + parameter.Name
				if _, duplicate := declared[key]; duplicate {
					t.Errorf("%s declares duplicate %s parameter %q", location, parameter.In, parameter.Name)
				}
				declared[key] = parameter
			}
			assert.NotContains(t, declared, "header\x00Authorization", location)
			placeholders := pathParameter.FindAllStringSubmatch(path, -1)
			for _, placeholder := range placeholders {
				parameter := declared["path\x00"+placeholder[1]]
				if assert.NotNil(t, parameter, "%s path parameter %q", location, placeholder[1]) {
					assert.True(t, parameter.Required, location)
					assert.Equal(t, huma.TypeString, parameter.Schema.Type, location)
				}
			}
		}
	}
	require.Len(t, operationIDs, 41)
	assert.Equal(t, "session", spec.Components.SecuritySchemes[authz.SessionScheme].Name)
	for _, path := range []string{"/sessions/{session_id}", "/principals/{principal_id}/passkeys/{passkey_id}"} {
		name := "session_id"
		if strings.Contains(path, "passkeys") {
			name = "passkey_id"
		}
		found := false
		for _, parameter := range authnPathOperations(spec.Paths[path])[http.MethodDelete].Parameters {
			if parameter.Name != name {
				continue
			}
			found = true
			assert.Equal(t, "^[a-f0-9]{64}$", parameter.Schema.Pattern, path)
			assert.Equal(t, 64, *parameter.Schema.MinLength, path)
			assert.Equal(t, 64, *parameter.Schema.MaxLength, path)
		}
		require.True(t, found, path)
	}
}

func TestAuthnOpenAPIUsesTypedWebAuthnSchemas(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	for name, operation := range map[string]*huma.Operation{
		"assertion": spec.Paths["/login/passkey/verify"].Post,
		"creation":  spec.Paths["/principals/{principal_id}/passkeys"].Post,
	} {
		request := resolveAuthnSchema(spec, operation.RequestBody.Content["application/json"].Schema)
		credential := resolveAuthnSchema(spec, request.Properties["credential"])
		if assert.NotNil(t, credential, name) {
			assert.Contains(t, credential.Required, "id", name)
			assert.Contains(t, credential.Required, "rawId", name)
			assert.Contains(t, credential.Required, "response", name)
			assert.Contains(t, credential.Properties, "response", name)
		}
	}
	for name, operation := range map[string]*huma.Operation{
		"assertion options": spec.Paths["/login/passkey/options"].Post,
		"creation options":  spec.Paths["/principals/{principal_id}/passkey-registration-challenges"].Post,
	} {
		response := resolveAuthnSchema(spec, operation.Responses["201"].Content["application/json"].Schema)
		data := resolveAuthnSchema(spec, response.Properties["data"])
		options := resolveAuthnSchema(spec, data.Properties["options"])
		if assert.NotNil(t, options, name) {
			assert.Contains(t, options.Required, "publicKey", name)
			assert.Contains(t, options.Properties, "publicKey", name)
		}
	}
}

func TestAuthnWebAuthnCredentialsPreserveRawJSON(t *testing.T) {
	for _, test := range []struct {
		name  string
		value json.Marshaler
		want  string
	}{
		{"assertion", webAuthnAssertion(`{"id":"assertion"}`), `{"id":"assertion"}`},
		{"creation", webAuthnCreation(`{"id":"creation"}`), `{"id":"creation"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := test.value.MarshalJSON()
			require.NoError(t, err)
			assert.JSONEq(t, test.want, string(raw))
		})
	}

	var assertion webAuthnAssertion
	require.NoError(t, assertion.UnmarshalJSON([]byte(`{`)))
	assert.Equal(t, webAuthnAssertion(`{`), assertion)
	var creation webAuthnCreation
	require.NoError(t, creation.UnmarshalJSON([]byte(`{`)))
	assert.Equal(t, webAuthnCreation(`{`), creation)
}

func TestAuthenticationErrorsMapToStableHTTPStatuses(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{authnext.ErrInvalidCredentials, http.StatusUnauthorized},
		{authnext.ErrSessionNotFound, http.StatusNotFound},
		{authnext.ErrInvalidPassword, http.StatusUnprocessableEntity},
		{authnext.ErrPasswordReused, http.StatusUnprocessableEntity},
		{authnext.ErrInvalidMFA, http.StatusUnprocessableEntity},
		{authnext.ErrMFAEnrollment, http.StatusUnprocessableEntity},
		{authnext.ErrMFAAlreadyOn, http.StatusConflict},
		{authnext.ErrMFANotEnabled, http.StatusConflict},
		{authnext.ErrRecoveryCooldown, http.StatusConflict},
		{authnext.ErrPasskeyChallenge, http.StatusUnprocessableEntity},
		{authnext.ErrPasskeyCredential, http.StatusUnprocessableEntity},
		{authnext.ErrPasskeyAttestation, http.StatusUnprocessableEntity},
		{authnext.ErrPasskeyLimit, http.StatusConflict},
		{authnext.ErrPasskeyNotFound, http.StatusNotFound},
		{authnext.ErrPasskeyDisabled, http.StatusServiceUnavailable},
		{assert.AnError, http.StatusInternalServerError},
	}
	for _, test := range tests {
		var statusError huma.StatusError
		require.ErrorAs(t, authenticationError(test.err), &statusError)
		assert.Equal(t, test.status, statusError.GetStatus(), "for %v", test.err)
	}
}

func resolveAuthnSchema(spec *huma.OpenAPI, schema *huma.Schema) *huma.Schema {
	if schema != nil && schema.Ref != "" {
		return spec.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	return schema
}

func TestAuthnOpenAPIEmailVerificationRequiresOneCredentialForm(t *testing.T) {
	api := authnContractAPI(t)
	spec := api.OpenAPI()
	operation := spec.Paths["/email-verifications"].Post
	require.NotNil(t, operation)
	schema := operation.RequestBody.Content["application/json"].Schema
	if schema.Ref != "" {
		schema = spec.Components.Schemas.SchemaFromRef(schema.Ref)
	}
	require.NotNil(t, schema)
	require.Len(t, schema.OneOf, 2)
	assert.Equal(t, []string{"token"}, schema.OneOf[0].Required)
	assert.Equal(t, []string{"email", "code"}, schema.OneOf[1].Required)
	assert.Equal(t, "email", schema.Properties["email"].Format)
	assert.Equal(t, "^[0-9]{6}$", schema.Properties["code"].Pattern)
	assert.False(t, schema.AdditionalProperties.(bool))

	assert.Equal(t, http.StatusUnprocessableEntity,
		api.Post("/email-verifications", map[string]any{}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/email-verifications", map[string]any{
		"token": "cev1_token", "email": "person@example.test", "code": "123456",
	}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/email-verifications", map[string]any{
		"email": "person@example.test", "code": "123456",
	}, "Origin: https://app.example.test").Code)
	assert.Equal(t, 1024, *schema.Properties["password"].MaxLength)
	for _, accepted := range []map[string]any{
		{"token": "cev1_token", "password": "registration password"},
		{"email": "person@example.test", "code": "123456", "password": "registration password"},
	} {
		assert.Equal(t, http.StatusServiceUnavailable, api.Post("/email-verifications", accepted,
			"Origin: https://app.example.test").Code, "either form may carry the registration password")
	}
	for _, refused := range []map[string]any{
		{"password": "registration password"},
		{"token": "cev1_token", "email": "person@example.test"},
		{"token": "cev1_token", "code": "123456", "password": "registration password"},
		{"email": "person@example.test", "password": "registration password"},
		{"token": "cev1_token", "password": ""},
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/email-verifications", refused,
			"Origin: https://app.example.test").Code, "%v", refused)
	}
}

func TestAuthnOpenAPIDeclaresRuntimeErrorsWithOneEnvelope(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	expected := map[string][]string{
		"authn-login-options":                         {},
		"authn-me":                                    {"401", "403", "503"},
		"authn-create-registration-captcha":           {"500"},
		"authn-create-registration":                   {"403", "422", "500", "503"},
		"authn-create-session":                        {"401", "403", "409", "422", "500"},
		"authn-create-session-verification":           {"401", "403", "500"},
		"authn-create-passkey-login-options":          {"403", "422", "500", "503"},
		"authn-verify-passkey-login":                  {"401", "403", "500", "503"},
		"authn-get-current-session":                   {"401", "403", "503"},
		"authn-delete-current-session":                {"403", "503"},
		"authn-list-sessions":                         {"401", "403", "500", "503"},
		"authn-delete-session":                        {"401", "403", "404", "500", "503"},
		"authn-delete-sessions":                       {"401", "403", "500", "503"},
		"authn-update-password":                       {"401", "403", "422", "500", "503"},
		"authn-create-password-recovery-request":      {"403", "422", "503"},
		"authn-create-password-recovery":              {"400", "403", "409", "503"},
		"authn-create-email-verification-request":     {"401", "403", "409", "503"},
		"authn-create-email-verification":             {"400", "403", "503"},
		"authn-get-mfa":                               {"401", "403", "500", "503"},
		"authn-create-totp-enrollment":                {"401", "403", "409", "500"},
		"authn-create-totp-factor":                    {"401", "403", "409", "422", "500"},
		"authn-replace-recovery-codes":                {"401", "403", "409", "422", "500", "503"},
		"authn-delete-totp-factor":                    {"401", "403", "409", "422", "500", "503"},
		"authn-create-step-up-challenge":              {"401", "403", "409", "500", "503"},
		"authn-create-step-up-verification":           {"401", "403", "409", "422", "500", "503"},
		"authn-list-passkeys":                         {"401", "403", "500", "503"},
		"authn-create-passkey-registration-challenge": {"401", "403", "409", "500", "503"},
		"authn-create-passkey":                        {"401", "403", "409", "422", "500", "503"},
		"authn-update-passkey":                        {"401", "403", "404", "422", "500", "503"},
		"authn-delete-passkey":                        {"401", "403", "404", "500", "503"},
	}
	for path, item := range spec.Paths {
		for method, operation := range authnPathOperations(item) {
			if operation == nil {
				continue
			}
			for status, response := range operation.Responses {
				if strings.HasPrefix(status, "2") {
					continue
				}
				media := response.Content["application/json"]
				if assert.NotNil(t, media, method+" "+path+" "+status) && assert.NotNil(t, media.Schema) {
					assert.Equal(t, "#/components/schemas/ErrorEnvelope", media.Schema.Ref, method+" "+path+" "+status)
				}
			}
			for _, status := range expected[operation.OperationID] {
				assert.Contains(t, operation.Responses, status, operation.OperationID)
			}
			delete(expected, operation.OperationID)
		}
	}
	assert.Empty(t, expected, "every expected operation must exist")
}

func authnOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 30)
	for path, item := range spec.Paths {
		for method, operation := range authnPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func authnPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete, http.MethodHead: item.Head,
		http.MethodOptions: item.Options, http.MethodTrace: item.Trace,
	}
}

// TestErrorMappersKeepChosenStatus is this package's half of the set-wide rule in
// tests/architecture (domainErrorMappers): an error that already chose its
// status, headers included, leaves every mapper unchanged instead of becoming 500.
func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.ErrorWithHeaders(huma.Error422UnprocessableEntity(respx.InvalidID), http.Header{"Retry-After": {"1"}})
	for name, mapper := range map[string]func(error) error{
		"accountViewHTTPError": func(err error) error { return accountViewHTTPError(t.Context(), err) },
		"authenticationError":  authenticationError,
		"personalTokenError":   personalTokenError,
	} {
		mapped := mapper(chosen)
		var status huma.StatusError
		require.ErrorAs(t, mapped, &status, name)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(), name)
		var headers huma.HeadersError
		require.ErrorAs(t, mapped, &headers, name)
		assert.Equal(t, "1", headers.GetHeaders().Get("Retry-After"), name)
	}
}
