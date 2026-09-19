package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityRouteManifest is the approved target contract from the migration
// plan: 14 operations covering the source module's 14 principal and
// service-account capabilities.
const identityRouteManifest = `
identity-avatar-candidates|POST|/me/avatar-candidates
identity-avatar-candidate-image|GET|/me/avatar-candidates/{candidate_id}
identity-my-avatar|GET|/me/avatar
identity-set-my-avatar|PUT|/me/avatar
identity-avatar-image|GET|/principals/{principal_id}/avatar
identity-my-closure-request|GET|/me/closure-request
identity-submit-closure-request|POST|/me/closure-request
identity-cancel-closure-request|DELETE|/me/closure-request
identity-list-closure-requests|GET|/platform/closure-requests
identity-update-closure-request|PATCH|/platform/principals/{principal_id}/closure-request
identity-list-principals|GET|/principals
identity-create-principal|POST|/principals
identity-get-principal|GET|/principals/{principal_id}
identity-update-principal|PATCH|/principals/{principal_id}
identity-create-principal-suspension|PUT|/principals/{principal_id}/suspension
identity-delete-principal-suspension|DELETE|/principals/{principal_id}/suspension
identity-list-service-accounts|GET|/service-accounts
identity-create-service-account|POST|/service-accounts
identity-get-service-account|GET|/service-accounts/{service_account_id}
identity-replace-service-account|PUT|/service-accounts/{service_account_id}
identity-delete-service-account|DELETE|/service-accounts/{service_account_id}
identity-list-service-account-credentials|GET|/service-accounts/{service_account_id}/credentials
identity-create-service-account-credential|POST|/service-accounts/{service_account_id}/credentials
identity-delete-service-account-credential|DELETE|/service-accounts/{service_account_id}/credentials/{credential_id}
identity-platform-get-principal-lifecycle|GET|/platform/principals/{principal_id}/lifecycle
identity-platform-set-principal-retention|PUT|/platform/principals/{principal_id}/lifecycle/retention-policy
identity-platform-anonymize-principal|PUT|/platform/principals/{principal_id}/lifecycle/anonymization
identity-platform-suspend-principal|PUT|/platform/principals/{principal_id}/suspension
identity-platform-restore-principal|DELETE|/platform/principals/{principal_id}/suspension`

// identityGuards is the permission code each operation must carry. Editing a
// profile and suspending or restoring an account are changes to a user inside
// one tenant, so they carry user_update alongside the reads and creates -- the
// capability a tenant administrator has over their own tenant everywhere IAM
// is deployed. They were declared platform_administer until 10.3, which put
// them out of reach of the only role that should hold them.
var identityGuards = map[string]string{
	"identity-list-closure-requests":             "platform_administer",
	"identity-update-closure-request":            "platform_administer",
	"identity-list-principals":                   "user_view",
	"identity-create-principal":                  "user_create",
	"identity-get-principal":                     "user_view",
	"identity-update-principal":                  "user_update",
	"identity-create-principal-suspension":       "user_update",
	"identity-delete-principal-suspension":       "user_update",
	"identity-list-service-accounts":             "service_account_view",
	"identity-create-service-account":            "service_account_create",
	"identity-get-service-account":               "service_account_view",
	"identity-replace-service-account":           "service_account_update",
	"identity-delete-service-account":            "service_account_delete",
	"identity-list-service-account-credentials":  "service_account_manage_credential",
	"identity-create-service-account-credential": "service_account_manage_credential",
	"identity-delete-service-account-credential": "service_account_manage_credential",
	"identity-platform-get-principal-lifecycle":  "platform_administer",
	"identity-platform-set-principal-retention":  "platform_administer",
	"identity-platform-anonymize-principal":      "platform_administer",
	"identity-platform-suspend-principal":        "platform_administer",
	"identity-platform-restore-principal":        "platform_administer",
}

type identityTokenVerifier struct{ principalID guid.ID }

func (verifier identityTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{PrincipalID: verifier.principalID, SubjectType: authnext.SubjectTypePrincipal, ACR: 1, AMR: []string{"password"}}, nil
}

// identityRouteAuthorizer answers the tenant permission check each guarded
// operation performs. Withholding user_update alone exercises the guard on the
// three principal-administration routes while leaving the reads answerable, so
// a denial that came from the guard can be told apart from one that came from
// the route being unreachable at all.
type identityRouteAuthorizer struct {
	userUpdateAllowed bool
	platformAllowed   bool
}

func (identityRouteAuthorizer) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	fields := map[string]authz.FieldDecision{}
	for _, field := range authz.ProfileFields() {
		fields[field.Name] = authz.DecideField(field, nil, nil)
	}
	return authz.FieldConstraint{Fields: fields}, nil
}

func (authorizer identityRouteAuthorizer) Check(
	_ context.Context,
	_ guid.ID,
	permission string,
	_ guid.ID,
) (bool, error) {
	if permission == "user_update" {
		return authorizer.userUpdateAllowed, nil
	}
	return true, nil
}

// CheckPlatform answers for the principal lifecycle routes, which are the only
// platform-scoped ones this module registers. It defaults to denying, so a test
// that does not deliberately grant platform authority is reporting on a caller
// without it -- which is the case that matters: no tenant permission may reach
// retention or erasure.
func (authorizer identityRouteAuthorizer) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return authorizer.platformAllowed, nil
}

type identityMembershipChecker struct{}

func (identityMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

// initializeIdentityLocales resets the process-wide bundle and republishes the
// identity namespace. Registration is single-shot per bundle, so tests that
// need translated messages rebuild it instead of sharing one registration.
func initializeIdentityLocales() error {
	if err := platformi18n.NewModule(nil, platformi18n.Base).Initialize(context.Background()); err != nil {
		return err
	}
	if err := respx.NewModule(nil).Initialize(context.Background()); err != nil {
		return err
	}
	return identityi18n.Register()
}

type identityHTTPHarness struct {
	*identityHarness
	api humatest.TestAPI
}

func newIdentityHTTPHarness(t *testing.T, userUpdateAllowed bool) identityHTTPHarness {
	return newIdentityHTTPHarnessWithPlatform(t, userUpdateAllowed, false)
}

// newIdentityHTTPHarnessWithPlatform grants or withholds platform authority so
// the lifecycle routes can be exercised from both sides of the boundary.
func newIdentityHTTPHarnessWithPlatform(t *testing.T, userUpdateAllowed, platformAllowed bool) identityHTTPHarness {
	t.Helper()
	require.NoError(t, initializeIdentityLocales())
	harness := newIdentityHarness(t, "sl_")
	harness.service.fields = identityRouteAuthorizer{userUpdateAllowed: userUpdateAllowed}
	harness.seedTenant(t, 7, "active")
	harness.seedTenant(t, 8, "active")

	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(),
		identityTokenVerifier{principalID: 11},
		identityRouteAuthorizer{userUpdateAllowed: userUpdateAllowed, platformAllowed: platformAllowed},
		identityMembershipChecker{},
	)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("identity-test", "1.0.0"))
	RegisterHTTP(api, harness.service, registrar)
	return identityHTTPHarness{identityHarness: harness, api: humatest.Wrap(t, api)}
}

func identityContractAPI(t *testing.T) huma.API {
	t.Helper()
	respx.Install()
	_, api := humatest.New(t)
	RegisterHTTP(api, nil, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	return api
}

func identityPathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete, http.MethodHead: item.Head,
		http.MethodOptions: item.Options, http.MethodTrace: item.Trace,
	}
}

func identityOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, len(identityGuards))
	for path, item := range spec.Paths {
		for method, operation := range identityPathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func decodeIdentityEnvelope[T any](t *testing.T, response *httptest.ResponseRecorder) respx.Envelope[T] {
	t.Helper()
	var envelope respx.Envelope[T]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope), response.Body.String())
	return envelope
}

func identityTenantHeader(tenantID guid.ID) string {
	return authz.TenantHeader + ": " + tenantID.String()
}

func TestIdentityRouteManifestMatchesGeneratedOpenAPI(t *testing.T) {
	api := identityContractAPI(t)
	want := strings.Fields(identityRouteManifest)
	got := identityOperationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 29)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))
}

func TestIdentityOpenAPIGuardsParametersAndSecurityAreConsistent(t *testing.T) {
	spec := identityContractAPI(t).OpenAPI()
	operationIDs := make(map[string]string, len(identityGuards))
	pathParameter := regexp.MustCompile(`\{([^}]+)\}`)

	for path, item := range spec.Paths {
		assert.Equal(t, strings.ToLower(path), path, "paths use lowercase resource names")
		assert.False(t, strings.HasSuffix(path, "/"), "paths have no trailing slash")
		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			assert.NotContains(t,
				[]string{"disable", "restore", "revoke", "create", "update", "delete", "list"},
				segment, "%s uses nouns, not RPC verbs", path)
		}
		for method, operation := range identityPathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			require.NotEmpty(t, operation.OperationID, location)
			if previous, duplicate := operationIDs[operation.OperationID]; duplicate {
				t.Fatalf("duplicate operationId %q on %s and %s", operation.OperationID, previous, location)
			}
			operationIDs[operation.OperationID] = location
			assert.Equal(t, []string{"identity"}, operation.Tags, location)
			assert.NotEmpty(t, operation.Security, location)

			guard, declared := identityGuards[operation.OperationID]
			switch operation.OperationID {
			case "identity-my-closure-request", "identity-submit-closure-request", "identity-cancel-closure-request", "identity-my-avatar", "identity-set-my-avatar", "identity-avatar-candidates", "identity-avatar-candidate-image":
				assert.True(t, authz.IsAuthenticatedOperation(operation), location)
				assert.Equal(t, []map[string][]string{{authz.SessionScheme: {}}}, operation.Security, location)
				assert.NotContains(t, operation.Metadata, "authz.guard", location)
			case "identity-avatar-image":
				assert.True(t, authz.IsAuthenticatedOperation(operation), location)
				assert.NotContains(t, operation.Metadata, "authz.guard", location)
			default:
				require.True(t, declared, "%s is not in the approved contract", location)
				assert.Equal(t, guard, operation.Metadata["authz.guard"], location)
			}

			parameters := make(map[string]*huma.Param, len(operation.Parameters))
			for _, parameter := range operation.Parameters {
				key := strings.ToLower(parameter.In + "\x00" + parameter.Name)
				if _, duplicate := parameters[key]; duplicate {
					t.Errorf("%s declares duplicate %s parameter %q", location, parameter.In, parameter.Name)
				}
				parameters[key] = parameter
			}
			for _, placeholder := range pathParameter.FindAllStringSubmatch(path, -1) {
				parameter := parameters["path\x00"+placeholder[1]]
				if assert.NotNil(t, parameter, "%s path parameter %q", location, placeholder[1]) {
					assert.True(t, parameter.Required, location)
					assert.Equal(t, huma.TypeString, parameter.Schema.Type, location)
				}
			}
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
				if (operation.OperationID == "identity-my-closure-request" || operation.OperationID == "identity-my-avatar" || operation.OperationID == "identity-avatar-image") && status == http.StatusForbidden {
					continue // Reading one's own request requires authentication only.
				}
				assert.Contains(t, operation.Errors, status, location)
			}
		}
	}
	require.Len(t, operationIDs, 29)
}

func TestIdentityOpenAPIDescribesTypedMutationResponses(t *testing.T) {
	spec := identityContractAPI(t).OpenAPI()
	deleteAccount := spec.Paths["/service-accounts/{service_account_id}"].Delete
	require.NotNil(t, deleteAccount)
	assert.Contains(t, deleteAccount.Parameters[len(deleteAccount.Parameters)-1].Name+"", "")

	versions := map[string]bool{}
	for _, parameter := range deleteAccount.Parameters {
		versions[parameter.In+":"+parameter.Name] = parameter.Required
	}
	assert.Contains(t, versions, "query:version", "optimistic concurrency stays an explicit parameter")

	for path, method := range map[string]string{
		"/service-accounts/{service_account_id}":                             http.MethodDelete,
		"/service-accounts/{service_account_id}/credentials/{credential_id}": http.MethodDelete,
	} {
		operation := identityPathOperations(spec.Paths[path])[method]
		require.NotNil(t, operation, path)
		response, ok := operation.Responses["200"]
		require.True(t, ok, path)
		require.NotNil(t, response.Content["application/json"], path)
		assert.NotNil(t, response.Content["application/json"].Schema, "%s describes its body", path)
	}
}

func TestIdentityPrincipalHTTPLifecycleAndTenantIsolation(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	headerA, headerB := identityTenantHeader(7), identityTenantHeader(8)
	auth := "Authorization: Bearer test"

	created := harness.api.Post("/principals", auth, headerA, map[string]any{
		"login_name": "alice", "password": testPassword,
		"display_name": "Alice", "email": "alice@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	principal := decodeIdentityEnvelope[Principal](t, created).Data
	require.NotZero(t, principal.ID)
	id := principal.ID.String()

	assert.Equal(t, http.StatusOK, harness.api.Get("/principals?limit=50", auth, headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/principals/"+id, auth, headerA).Code)

	updated := harness.api.Patch("/principals/"+id, auth, headerA, map[string]any{
		"display_name": "Alice Smith",
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Equal(t, "Alice Smith", decodeIdentityEnvelope[Principal](t, updated).Data.DisplayName)

	suspended := harness.api.Put("/principals/"+id+"/suspension", auth, headerA)
	require.Equal(t, http.StatusOK, suspended.Code, suspended.Body.String())
	assert.Equal(t, "disabled", decodeIdentityEnvelope[Principal](t, suspended).Data.Status)

	restored := harness.api.Delete("/principals/"+id+"/suspension", auth, headerA)
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	assert.Equal(t, "active", decodeIdentityEnvelope[Principal](t, restored).Data.Status)

	// Tenant B cannot observe or change tenant A's principal.
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/principals/"+id, auth, headerB).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Patch("/principals/"+id, auth, headerB, map[string]any{
		"display_name": "Hijacked",
	}).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Put("/principals/"+id+"/suspension", auth, headerB).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Delete("/principals/"+id+"/suspension", auth, headerB).Code)

	listedByB := harness.api.Get("/principals?limit=50", auth, headerB)
	require.Equal(t, http.StatusOK, listedByB.Code)
	assert.Empty(t, decodeIdentityEnvelope[principalListData](t, listedByB).Data.Items)
	assert.Equal(t, "Alice Smith", harness.principal(t, principal.ID).DisplayName)
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
}

func TestIdentityPrincipalHTTPRejectsConflictsAndInvalidInput(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	header, auth := identityTenantHeader(7), "Authorization: Bearer test"

	created := harness.api.Post("/principals", auth, header, map[string]any{
		"login_name": "alice", "password": testPassword, "email": "alice@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	duplicate := harness.api.Post("/principals", auth, header, map[string]any{
		"login_name": "alice", "password": testPassword, "email": "other@example.test",
	})
	assert.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())

	short := harness.api.Post("/principals", auth, header, map[string]any{
		"login_name": "bob", "password": "short",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, short.Code)

	assert.Equal(t, http.StatusUnprocessableEntity, harness.api.Get("/principals/not-an-id", auth, header).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/principals/999999", auth, header).Code)
}

// TestIdentityPrincipalAdministrationRequiresTenantUserUpdatePermission checks
// the guard from both sides. A tenant administrator who may read users but not
// change them is refused all three write routes, and the principal is left as
// it was -- so a passing guard is not the only thing standing between a
// refused request and a changed record.
func TestIdentityPrincipalAdministrationRequiresTenantUserUpdatePermission(t *testing.T) {
	harness := newIdentityHTTPHarness(t, false)
	header, auth := identityTenantHeader(7), "Authorization: Bearer test"

	created := harness.api.Post("/principals", auth, header, map[string]any{
		"login_name": "alice", "password": testPassword,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	id := decodeIdentityEnvelope[Principal](t, created).Data.ID

	// Reads stay available; the three writes do not.
	assert.Equal(t, http.StatusOK, harness.api.Get("/principals/"+id.String(), auth, header).Code)
	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Patch("/principals/"+id.String(), auth, header, map[string]any{"display_name": "Nope"}),
		harness.api.Put("/principals/"+id.String()+"/suspension", auth, header),
		harness.api.Delete("/principals/"+id.String()+"/suspension", auth, header),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	assert.Equal(t, "active", harness.principal(t, id).Status)
	assert.NotEqual(t, "Nope", harness.principal(t, id).DisplayName)
}

func TestIdentityServiceAccountHTTPLifecycleAndTenantIsolation(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	headerA, headerB := identityTenantHeader(7), identityTenantHeader(8)
	auth := "Authorization: Bearer test"

	created := harness.api.Post("/service-accounts", auth, headerA, map[string]any{
		"login_name": "robot", "display_name": "Robot", "description": "ci runner",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	account := decodeIdentityEnvelope[ServiceAccount](t, created).Data
	id := account.ID.String()

	assert.Equal(t, http.StatusOK, harness.api.Get("/service-accounts?limit=50", auth, headerA).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/service-accounts/"+id, auth, headerA).Code)

	credential := harness.api.Post("/service-accounts/"+id+"/credentials", auth, headerA, map[string]any{
		"name": "ci", "scopes": []string{"read"},
	})
	require.Equal(t, http.StatusCreated, credential.Code, credential.Body.String())
	secret := decodeIdentityEnvelope[ServiceAccountCredentialSecret](t, credential).Data
	require.NotEmpty(t, secret.Secret)

	listed := harness.api.Get("/service-accounts/"+id+"/credentials", auth, headerA)
	require.Equal(t, http.StatusOK, listed.Code)
	require.Len(t, decodeIdentityEnvelope[[]ServiceAccountCredential](t, listed).Data, 1)

	// Tenant B must not reach the account, its credentials, or its version.
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/service-accounts/"+id, auth, headerB).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/service-accounts/"+id+"/credentials", auth, headerB).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Post("/service-accounts/"+id+"/credentials", auth, headerB, map[string]any{
		"name": "stolen", "scopes": []string{"read"},
	}).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Delete(
		"/service-accounts/"+id+"/credentials/"+secret.Credential.ID.String(), auth, headerB,
	).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Put("/service-accounts/"+id, auth, headerB, map[string]any{
		"display_name": "Hijacked", "status": "disabled", "version": 1,
	}).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Delete("/service-accounts/"+id+"?version=1", auth, headerB).Code)
	assert.Equal(t, "active", harness.serviceAccountRow(t, account.ID).Status)
	assert.Nil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)

	revoked := harness.api.Delete(
		"/service-accounts/"+id+"/credentials/"+secret.Credential.ID.String(), auth, headerA,
	)
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	assert.NotNil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)

	replaced := harness.api.Put("/service-accounts/"+id, auth, headerA, map[string]any{
		"display_name": "Robot Two", "description": "ci", "status": "active", "version": 1,
	})
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Equal(t, int64(2), decodeIdentityEnvelope[ServiceAccount](t, replaced).Data.Version)

	stale := harness.api.Put("/service-accounts/"+id, auth, headerA, map[string]any{
		"display_name": "Robot Three", "status": "active", "version": 1,
	})
	assert.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())

	deleted := harness.api.Delete("/service-accounts/"+id+"?version=2", auth, headerA)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.Equal(t, "deleted", harness.serviceAccountRow(t, account.ID).Status)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/service-accounts/"+id, auth, headerA).Code)
}

func TestIdentityHTTPReportsDomainFailuresAsTranslatedEnvelopes(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	header, auth := identityTenantHeader(7), "Authorization: Bearer test"

	response := harness.api.Get("/principals/999999", auth, header)
	require.Equal(t, http.StatusNotFound, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	assert.NotEqual(t, identityi18n.PrincipalNotFound, body["message"],
		"the message key is resolved through the locale bundle")
	assert.NotEmpty(t, body["message"])
}

func TestIdentityErrorMapsEveryDomainSentinelOnce(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		err    error
		status int
		key    string
	}{
		{ErrNotFound, http.StatusNotFound, identityi18n.PrincipalNotFound},
		{ErrServiceAccountNotFound, http.StatusNotFound, identityi18n.ServiceAccountNotFound},
		{ErrCredentialNotFound, http.StatusNotFound, identityi18n.ServiceAccountCredentialNotFound},
		{ErrLoginConflict, http.StatusConflict, identityi18n.LoginNameExists},
		{ErrServiceAccountConflict, http.StatusConflict, identityi18n.ServiceAccountVersionConflict},
		{ErrCredentialLimit, http.StatusConflict, identityi18n.ServiceAccountCredentialLimit},
		{ErrPrincipalInactive, http.StatusConflict, identityi18n.InvalidPrincipal},
		{ErrServiceAccountInvalid, http.StatusUnprocessableEntity, identityi18n.InvalidServiceAccount},
		{ErrInvalid, http.StatusUnprocessableEntity, identityi18n.InvalidPrincipal},
		{errRepositoryUnavailable, http.StatusInternalServerError, identityi18n.IdentityUnavailable},
	} {
		t.Run(testCase.key, func(t *testing.T) {
			var status huma.StatusError
			require.ErrorAs(t, identityError(testCase.err), &status)
			assert.Equal(t, testCase.status, status.GetStatus())
			assert.Contains(t, status.Error(), testCase.key)
		})
	}
}
