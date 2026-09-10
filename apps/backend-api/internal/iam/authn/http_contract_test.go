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
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authnRouteManifest = `
authn-create-personal-token-preparation|POST|/authn/personal-token-preparations
authn-create-personal-token|POST|/authn/personal-tokens
authn-delete-personal-token|DELETE|/authn/personal-tokens/{id}
authn-list-personal-tokens|GET|/authn/personal-tokens
authn-list-personal-token-permissions|GET|/authn/personal-token-permissions
authn-list-reauthentication-methods|GET|/authn/reauthentication-methods
authn-create-reauthentication-challenge|POST|/authn/reauthentication-challenges
authn-create-reauthentication-verification|POST|/authn/reauthentication-verifications
authn-capabilities|GET|/authn/capabilities
authn-create-captcha|POST|/authn/captchas
authn-create-email-verification-request|POST|/authn/email-verification-requests
authn-create-email-verification|POST|/authn/email-verifications
authn-create-passkey-registration-challenge|POST|/authn/passkey-registration-challenges
authn-create-passkey-session-challenge|POST|/authn/passkey-session-challenges
authn-create-passkey-session-verification|POST|/authn/passkey-session-verifications
authn-create-passkey|POST|/authn/passkeys
authn-create-password-recovery-request|POST|/authn/password-recovery-requests
authn-create-password-recovery|POST|/authn/password-recoveries
authn-create-registration|POST|/authn/registrations
authn-create-session-verification|POST|/authn/session-verifications
authn-create-session|POST|/authn/sessions
authn-create-step-up-challenge|POST|/authn/step-up-challenges
authn-create-step-up-verification|POST|/authn/step-up-verifications
authn-create-totp-enrollment|POST|/authn/mfa/totp-enrollments
authn-create-totp-factor|POST|/authn/mfa/totp
authn-delete-current-session|DELETE|/authn/sessions/current
authn-delete-passkey|DELETE|/authn/passkeys/{id}
authn-delete-session|DELETE|/authn/sessions/{id}
authn-delete-sessions|DELETE|/authn/sessions
authn-delete-totp-factor|DELETE|/authn/mfa/totp
authn-get-current-session|GET|/authn/sessions/current
authn-get-mfa|GET|/authn/mfa
authn-list-passkeys|GET|/authn/passkeys
authn-list-sessions|GET|/authn/sessions
authn-me|GET|/authn/me
authn-replace-recovery-codes|PUT|/authn/mfa/recovery-codes
authn-update-passkey|PATCH|/authn/passkeys/{id}
authn-update-password|PATCH|/authn/password`

func authnContractAPI(t *testing.T) humatest.TestAPI {
	t.Helper()
	respx.Install()
	service, _ := newLocalService(t)
	_, api := humatest.New(t)
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
	require.Len(t, got, 38)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))
}

func TestAuthnOpenAPIPathsParametersAndSecurityAreConsistent(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	operationIDs := make(map[string]string, 30)
	pathParameter := regexp.MustCompile(`\{([^}]+)\}`)
	secured := map[string]bool{
		"authn-me": true, "authn-get-current-session": true,
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
		"authn-create-reauthentication-challenge": true, "authn-create-reauthentication-verification": true,
		"authn-list-reauthentication-methods": true, "authn-create-personal-token-preparation": true,
		"authn-create-personal-token": true, "authn-delete-personal-token": true,
		"authn-list-personal-tokens": true, "authn-list-personal-token-permissions": true,
	}

	for path, item := range spec.Paths {
		assert.Equal(t, strings.ToLower(path), path, "paths use lowercase resource names")
		assert.False(t, strings.HasSuffix(path, "/"), "paths have no trailing slash")
		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			assert.NotContains(t, []string{"login", "logout", "start", "complete", "options", "verify", "confirm", "regenerate", "enroll", "change"}, segment, path)
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
			assert.Equal(t, []string{"authn"}, operation.Tags, location)
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
	require.Len(t, operationIDs, 38)
	assert.Equal(t, "session", spec.Components.SecuritySchemes[authz.SessionScheme].Name)
	for _, path := range []string{"/authn/sessions/{id}", "/authn/passkeys/{id}"} {
		parameter := authnPathOperations(spec.Paths[path])[http.MethodDelete].Parameters[0]
		assert.Equal(t, "^[a-f0-9]{64}$", parameter.Schema.Pattern, path)
		assert.Equal(t, 64, *parameter.Schema.MinLength, path)
		assert.Equal(t, 64, *parameter.Schema.MaxLength, path)
	}
}

func TestAuthnOpenAPIUsesTypedWebAuthnSchemas(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	for name, operation := range map[string]*huma.Operation{
		"assertion": spec.Paths["/authn/passkey-session-verifications"].Post,
		"creation":  spec.Paths["/authn/passkeys"].Post,
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
		"assertion options": spec.Paths["/authn/passkey-session-challenges"].Post,
		"creation options":  spec.Paths["/authn/passkey-registration-challenges"].Post,
	} {
		response := resolveAuthnSchema(spec, operation.Responses["200"].Content["application/json"].Schema)
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
	operation := spec.Paths["/authn/email-verifications"].Post
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
		api.Post("/authn/email-verifications", map[string]any{}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/authn/email-verifications", map[string]any{
		"token": "cev1_token", "email": "person@example.test", "code": "123456",
	}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/authn/email-verifications", map[string]any{
		"email": "person@example.test", "code": "123456",
	}, "Origin: https://app.example.test").Code)
}

func TestAuthnOpenAPIDeclaresRuntimeErrorsWithOneEnvelope(t *testing.T) {
	spec := authnContractAPI(t).OpenAPI()
	expected := map[string][]string{
		"authn-capabilities":                          {},
		"authn-me":                                    {"401", "403", "503"},
		"authn-create-captcha":                        {"500"},
		"authn-create-registration":                   {"403", "422", "500", "503"},
		"authn-create-session":                        {"401", "403", "409", "422", "500"},
		"authn-create-session-verification":           {"401", "403", "500"},
		"authn-create-passkey-session-challenge":      {"403", "422", "500", "503"},
		"authn-create-passkey-session-verification":   {"401", "403", "500", "503"},
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
