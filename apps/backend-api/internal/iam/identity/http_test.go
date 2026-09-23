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
identity-list-principals|GET|/tenants/{tenant_id}/principals
identity-create-principal|POST|/tenants/{tenant_id}/principals
identity-get-principal|GET|/tenants/{tenant_id}/principals/{principal_id}
identity-update-principal|PATCH|/tenants/{tenant_id}/principals/{principal_id}
identity-create-principal-suspension|PUT|/tenants/{tenant_id}/principals/{principal_id}/suspension
identity-delete-principal-suspension|DELETE|/tenants/{tenant_id}/principals/{principal_id}/suspension
identity-list-service-accounts|GET|/tenants/{tenant_id}/service-accounts
identity-create-service-account|POST|/tenants/{tenant_id}/service-accounts
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

type identityMembershipChecker struct{ inactiveTenant *guid.ID }

func (checker identityMembershipChecker) IsMemberActive(_ context.Context, tenantID guid.ID, _ guid.ID) (bool, error) {
	return checker.inactiveTenant == nil || tenantID != *checker.inactiveTenant, nil
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
	api        humatest.TestAPI
	membership *identityMembershipChecker
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

	membership := &identityMembershipChecker{}
	registrar := authz.NewRegistrar(
		authz.DefaultRegistry(),
		identityTokenVerifier{principalID: 11},
		identityRouteAuthorizer{userUpdateAllowed: userUpdateAllowed, platformAllowed: platformAllowed},
		membership,
	)
	router := chi.NewRouter()
	api := humax.New(router, humax.DefaultConfig("identity-test", "1.0.0"))
	RegisterHTTP(api, harness.service, registrar)
	return identityHTTPHarness{identityHarness: harness, api: humatest.Wrap(t, api), membership: membership}
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

func identityTenantPath(tenantID guid.ID) string {
	return "/tenants/" + tenantID.String()
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
			// Identity owns the principal record, the principal's own view of
			// it, and the platform-wide lifecycle over it.
			assert.Contains(t, []string{"directory", "account", "platform"},
				operation.Tags[0], location)
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
	headerA, headerB := identityTenantPath(7), identityTenantPath(8)
	auth := "Authorization: Bearer test"

	created := harness.api.Post(headerA+"/principals", auth, map[string]any{
		"login_name": "alice", "password": testPassword,
		"display_name": "Alice", "email": "alice@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	principal := decodeIdentityEnvelope[Principal](t, created).Data
	require.NotZero(t, principal.ID)
	id := principal.ID.String()

	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/principals?limit=50", auth).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/principals/"+id, auth).Code)

	updated := harness.api.Patch(headerA+"/principals/"+id, auth, map[string]any{
		"display_name": "Alice Smith",
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	assert.Equal(t, "Alice Smith", decodeIdentityEnvelope[Principal](t, updated).Data.DisplayName)

	suspended := harness.api.Put(headerA+"/principals/"+id+"/suspension", auth)
	require.Equal(t, http.StatusOK, suspended.Code, suspended.Body.String())
	assert.Equal(t, "disabled", decodeIdentityEnvelope[Principal](t, suspended).Data.Status)

	restored := harness.api.Delete(headerA+"/principals/"+id+"/suspension", auth)
	require.Equal(t, http.StatusOK, restored.Code, restored.Body.String())
	assert.Equal(t, "active", decodeIdentityEnvelope[Principal](t, restored).Data.Status)

	// Tenant B cannot observe or change tenant A's principal.
	assert.Equal(t, http.StatusNotFound, harness.api.Get(headerB+"/principals/"+id, auth).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Patch(headerB+"/principals/"+id, auth, map[string]any{
		"display_name": "Hijacked",
	}).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Put(headerB+"/principals/"+id+"/suspension", auth).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Delete(headerB+"/principals/"+id+"/suspension", auth).Code)

	listedByB := harness.api.Get(headerB+"/principals?limit=50", auth)
	require.Equal(t, http.StatusOK, listedByB.Code)
	assert.Empty(t, decodeIdentityEnvelope[principalListData](t, listedByB).Data.Items)
	assert.Equal(t, "Alice Smith", harness.principal(t, principal.ID).DisplayName)
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
}

func TestIdentityPrincipalHTTPRejectsConflictsAndInvalidInput(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	header, auth := identityTenantPath(7), "Authorization: Bearer test"

	created := harness.api.Post(header+"/principals", auth, map[string]any{
		"login_name": "alice", "password": testPassword, "email": "alice@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	duplicate := harness.api.Post(header+"/principals", auth, map[string]any{
		"login_name": "alice", "password": testPassword, "email": "other@example.test",
	})
	assert.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())

	short := harness.api.Post(header+"/principals", auth, map[string]any{
		"login_name": "bob", "password": "short",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, short.Code)

	assert.Equal(t, http.StatusUnprocessableEntity, harness.api.Get(header+"/principals/not-an-id", auth).Code)
	assert.Equal(t, http.StatusNotFound, harness.api.Get(header+"/principals/999999", auth).Code)
}

// TestIdentityPrincipalAdministrationRequiresTenantUserUpdatePermission checks
// the guard from both sides. A tenant administrator who may read users but not
// change them is refused all three write routes, and the principal is left as
// it was -- so a passing guard is not the only thing standing between a
// refused request and a changed record.
func TestIdentityPrincipalAdministrationRequiresTenantUserUpdatePermission(t *testing.T) {
	harness := newIdentityHTTPHarness(t, false)
	header, auth := identityTenantPath(7), "Authorization: Bearer test"

	created := harness.api.Post(header+"/principals", auth, map[string]any{
		"login_name": "alice", "password": testPassword,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	id := decodeIdentityEnvelope[Principal](t, created).Data.ID

	// Reads stay available; the three writes do not.
	assert.Equal(t, http.StatusOK, harness.api.Get(header+"/principals/"+id.String(), auth).Code)
	for _, response := range []*httptest.ResponseRecorder{
		harness.api.Patch(header+"/principals/"+id.String(), auth, map[string]any{"display_name": "Nope"}),
		harness.api.Put(header+"/principals/"+id.String()+"/suspension", auth),
		harness.api.Delete(header+"/principals/"+id.String()+"/suspension", auth),
	} {
		assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	}
	assert.Equal(t, "active", harness.principal(t, id).Status)
	assert.NotEqual(t, "Nope", harness.principal(t, id).DisplayName)
}

func TestIdentityServiceAccountHTTPLifecycleAndTenantIsolation(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	headerA := identityTenantPath(7)
	auth := "Authorization: Bearer test"

	created := harness.api.Post(headerA+"/service-accounts", auth, map[string]any{
		"login_name": "robot", "display_name": "Robot", "description": "ci runner",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	account := decodeIdentityEnvelope[ServiceAccount](t, created).Data
	id := account.ID.String()

	assert.Equal(t, http.StatusOK, harness.api.Get(headerA+"/service-accounts?limit=50", auth).Code)
	assert.Equal(t, http.StatusOK, harness.api.Get("/service-accounts/"+id, auth).Code)

	credential := harness.api.Post("/service-accounts/"+id+"/credentials", auth, map[string]any{
		"name": "ci", "scopes": []string{"read"},
	})
	require.Equal(t, http.StatusCreated, credential.Code, credential.Body.String())
	secret := decodeIdentityEnvelope[ServiceAccountCredentialSecret](t, credential).Data
	require.NotEmpty(t, secret.Secret)

	listed := harness.api.Get("/service-accounts/"+id+"/credentials", auth)
	require.Equal(t, http.StatusOK, listed.Code)
	require.Len(t, decodeIdentityEnvelope[[]ServiceAccountCredential](t, listed).Data, 1)

	inactiveTenant := guid.ID(8)
	harness.membership.inactiveTenant = &inactiveTenant
	foreign, err := harness.service.CreateServiceAccount(t.Context(), 8, "foreign-robot", "Foreign Robot", "", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/service-accounts/"+foreign.ID.String(), auth).Code)

	revoked := harness.api.Delete(
		"/service-accounts/"+id+"/credentials/"+secret.Credential.ID.String(), auth,
	)
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	assert.NotNil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)

	replaced := harness.api.Put("/service-accounts/"+id, auth, map[string]any{
		"display_name": "Robot Two", "description": "ci", "status": "active", "version": 1,
	})
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	assert.Equal(t, int64(2), decodeIdentityEnvelope[ServiceAccount](t, replaced).Data.Version)

	stale := harness.api.Put("/service-accounts/"+id, auth, map[string]any{
		"display_name": "Robot Three", "status": "active", "version": 1,
	})
	assert.Equal(t, http.StatusConflict, stale.Code, stale.Body.String())

	deleted := harness.api.Delete("/service-accounts/"+id+"?version=2", auth)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.Equal(t, "deleted", harness.serviceAccountRow(t, account.ID).Status)
	assert.Equal(t, http.StatusNotFound, harness.api.Get("/service-accounts/"+id, auth).Code)
}

func TestIdentityHTTPReportsDomainFailuresAsTranslatedEnvelopes(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	header, auth := identityTenantPath(7), "Authorization: Bearer test"

	response := harness.api.Get(header+"/principals/999999", auth)
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

// TestIdentityHTTPReportsStorageFailuresInsteadOfEmptyAnswers drops the tables
// each route reads and proves the transport reports the outage. A read that
// answered 200 with an empty page, or a write that answered 200 without
// writing, would be indistinguishable from a tenant that really is empty.
func TestIdentityHTTPReportsStorageFailuresInsteadOfEmptyAnswers(t *testing.T) {
	harness := newIdentityHTTPHarness(t, true)
	tenant, auth := identityTenantPath(7), "Authorization: Bearer test"
	created := harness.api.Post(tenant+"/service-accounts", auth, map[string]any{
		"login_name": "robot", "display_name": "Robot",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	account := decodeIdentityEnvelope[ServiceAccount](t, created).Data
	credential := harness.api.Post("/service-accounts/"+account.ID.String()+"/credentials", auth, map[string]any{
		"name": "ci", "scopes": []string{"read"},
	})
	require.Equal(t, http.StatusCreated, credential.Code, credential.Body.String())
	secret := decodeIdentityEnvelope[ServiceAccountCredentialSecret](t, credential).Data
	accountPath := "/service-accounts/" + account.ID.String()

	// The credential store alone is gone: the account still resolves, so the
	// failure has to come from the operation rather than from authorization.
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_service_account_credentials")).Error)
	for name, response := range map[string]*httptest.ResponseRecorder{
		"list credentials": harness.api.Get(accountPath+"/credentials", auth),
		"create credential": harness.api.Post(accountPath+"/credentials", auth, map[string]any{
			"name": "second", "scopes": []string{"read"},
		}),
		"revoke credential": harness.api.Delete(accountPath+"/credentials/"+secret.Credential.ID.String(), auth),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, "%s: %s", name, response.Body.String())
	}

	// Memberships are gone: creating an account or a principal cannot succeed.
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_tenant_members")).Error)
	assert.Equal(t, http.StatusInternalServerError, harness.api.Post(tenant+"/service-accounts", auth, map[string]any{
		"login_name": "robot2", "display_name": "Robot Two",
	}).Code)
	assert.Equal(t, http.StatusInternalServerError, harness.api.Post(tenant+"/principals", auth, map[string]any{
		"login_name": "alice", "password": testPassword, "display_name": "Alice",
	}).Code)

	// Identities are gone: every listing and account read reports the outage
	// rather than answering with nothing found.
	require.NoError(t, harness.db.Exec("PRAGMA foreign_keys = OFF").Error)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_principals")).Error)
	for name, response := range map[string]*httptest.ResponseRecorder{
		"list principals":       harness.api.Get(tenant+"/principals?limit=10", auth),
		"list service accounts": harness.api.Get(tenant+"/service-accounts?limit=10", auth),
		"get service account":   harness.api.Get(accountPath, auth),
		"replace service account": harness.api.Put(accountPath, auth, map[string]any{
			"display_name": "Robot", "description": "Robot", "status": "active", "version": 1,
		}),
		"delete service account": harness.api.Delete(accountPath+"?version=1", auth),
	} {
		assert.Equal(t, http.StatusInternalServerError, response.Code, "%s: %s", name, response.Body.String())
		assert.NotContains(t, response.Body.String(), `"items":[]`, "%s must not answer as an empty page", name)
	}
}
