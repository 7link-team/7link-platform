package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	coremodel "7link.com/platform/internal/iam/core/model"
	governancei18n "7link.com/platform/internal/iam/governance/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const governanceRouteManifest = `
governance-create-access-request|POST|/tenants/{tenant_id}/role-access-requests
governance-create-access-review|POST|/tenants/{tenant_id}/role-access-reviews
governance-decide-access-review-item|PUT|/role-access-reviews/{review_id}/items/{item_id}/decision
governance-get-access-review|GET|/role-access-reviews/{review_id}
governance-list-access-requests|GET|/tenants/{tenant_id}/role-access-requests
governance-list-access-reviews|GET|/tenants/{tenant_id}/role-access-reviews
governance-list-my-access-requests|GET|/tenants/{tenant_id}/principals/{principal_id}/role-access-requests
governance-list-requestable-roles|GET|/tenants/{tenant_id}/requestable-roles
governance-set-access-request-decision|PUT|/role-access-requests/{request_id}/decision
governance-revoke-access-grant|PUT|/role-access-requests/{request_id}/grant-revocation
governance-update-access-review|PATCH|/role-access-reviews/{review_id}
governance-withdraw-access-request|PUT|/role-access-requests/{request_id}/withdrawal`

func governancePathOperations(item *huma.PathItem) map[string]*huma.Operation {
	if item == nil {
		return nil
	}
	return map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	}
}

func governanceOperationManifest(spec *huma.OpenAPI) []string {
	manifest := make([]string, 0, 12)
	for path, item := range spec.Paths {
		for method, operation := range governancePathOperations(item) {
			if operation != nil {
				manifest = append(manifest, operation.OperationID+"|"+method+"|"+path)
			}
		}
	}
	return manifest
}

func TestGovernanceRouteManifestAndOpenAPIContract(t *testing.T) {
	respx.Install()
	_, api := humatest.New(t)
	RegisterHTTP(api, nil, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	want, got := strings.Fields(governanceRouteManifest), governanceOperationManifest(api.OpenAPI())
	sort.Strings(want)
	sort.Strings(got)
	require.Equal(t, want, got)
	require.Len(t, got, 12)
	require.NoError(t, authz.ValidateOperations(api, authz.DefaultRegistry()))

	memberOperations := map[string]bool{
		"governance-list-requestable-roles":  true,
		"governance-create-access-request":   true,
		"governance-list-my-access-requests": true,
		"governance-withdraw-access-request": true,
	}
	seen := map[string]bool{}
	for path, item := range api.OpenAPI().Paths {
		for method, operation := range governancePathOperations(item) {
			if operation == nil {
				continue
			}
			location := method + " " + path
			assert.False(t, seen[operation.OperationID], location)
			seen[operation.OperationID] = true
			// Requests and reviews are both ways of changing who holds a role,
			// so the module publishes one section whichever owner the route
			// hangs off.
			assert.Equal(t, []string{"access-control"}, operation.Tags, location)
			assert.NotEmpty(t, operation.Security, location)
			var tenantPaths int
			for _, parameter := range operation.Parameters {
				if parameter.Name == "tenant_id" && parameter.In == "path" {
					tenantPaths++
					assert.True(t, parameter.Required, location)
				}
			}
			if strings.HasPrefix(path, "/tenants/") {
				assert.Equal(t, 1, tenantPaths, location)
			} else {
				assert.Zero(t, tenantPaths, location)
				ownerParam := "request_id"
				if strings.HasPrefix(path, "/role-access-reviews/") {
					ownerParam = "review_id"
				}
				assert.Condition(t, func() bool {
					for _, parameter := range operation.Parameters {
						if parameter.Name == ownerParam && parameter.In == "path" && parameter.Required {
							return true
						}
					}
					return false
				}, location)
			}
			if memberOperations[operation.OperationID] {
				assert.True(t, authz.IsTenantMemberOperation(operation), location)
				assert.NotContains(t, operation.Extensions, authz.GuardExtensionKey, location)
			} else {
				assert.NotEmpty(t, operation.Extensions[authz.GuardExtensionKey], location)
			}
		}
	}
	decisionSchema := api.OpenAPI().Components.Schemas.Map()["ReviewItemInputBody"]
	if decisionSchema != nil {
		assert.Equal(t, []any{"keep", "revoke"}, decisionSchema.Properties["decision"].Enum)
	}
}

type governanceHTTPVerifier struct{}

func (governanceHTTPVerifier) Authenticate(_ context.Context, authorization, _ string) (*authnext.Claims, error) {
	value := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer"))
	if value == "service" {
		return &authnext.Claims{Subject: "service", SubjectType: authnext.SubjectTypeServiceAccount, PrincipalID: 10}, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return nil, authnext.ErrInvalidToken
	}
	return &authnext.Claims{Subject: value, SubjectType: authnext.SubjectTypePrincipal, PrincipalID: guid.ID(id), ACR: 1}, nil
}

type governanceHTTPPermissions struct{}

func (governanceHTTPPermissions) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (governanceHTTPPermissions) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type governanceDenyPermissions struct{ governanceHTTPPermissions }

func (governanceDenyPermissions) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return false, nil
}

type governanceHTTPMembership struct{}

func (governanceHTTPMembership) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func newGovernanceHTTPHarness(t *testing.T) (*governanceHarness, humatest.TestAPI) {
	t.Helper()
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, respx.NewModule(nil).Initialize(t.Context()))
	require.NoError(t, governancei18n.Register())
	h := newGovernanceHarness(t, "sl_")
	h.seedTenant(t, 1)
	for id, name := range map[guid.ID]string{10: "Requester", 11: "Subject", 12: "Reviewer"} {
		h.seedMember(t, 1, id, name)
	}
	h.seedRole(t, 1, 20, "Operator")
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), governanceHTTPVerifier{}, governanceHTTPPermissions{}, governanceHTTPMembership{})
	_, api := humatest.New(t)
	RegisterHTTP(api, h.service, registrar)
	return h, api
}

func responseData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Data
}

func TestGovernanceHTTPExercisesEveryOperation(t *testing.T) {
	h, api := newGovernanceHTTPHarness(t)
	tenant := "/tenants/1"
	requester := "Authorization: Bearer 10"
	reviewer := "Authorization: Bearer 12"
	future := governanceTestNow.Add(24 * time.Hour).Format(time.RFC3339Nano)

	assert.Equal(t, http.StatusOK, api.Get(tenant+"/requestable-roles", requester).Code)
	created := api.Post(tenant+"/role-access-requests", requester, map[string]any{
		"role_id": "20", "reason": "temporary operations access", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	request := responseData[AccessRequest](t, created.Body.Bytes())
	assert.Equal(t, http.StatusOK, api.Get(tenant+"/principals/"+"10"+"/role-access-requests", requester).Code)
	assert.Equal(t, http.StatusOK, api.Get(tenant+"/role-access-requests", reviewer).Code)
	approved := api.Put("/role-access-requests/"+request.ID.String()+"/decision", reviewer, map[string]any{"decision": "approved", "note": "approved"})
	require.Equal(t, http.StatusOK, approved.Code, approved.Body.String())
	assert.Equal(t, http.StatusOK, api.Put("/role-access-requests/"+request.ID.String()+"/grant-revocation", reviewer, map[string]any{"reason": "finished"}).Code)

	withdraw := api.Post(tenant+"/role-access-requests", requester, map[string]any{
		"role_id": "20", "reason": "withdraw this request", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, withdraw.Code, withdraw.Body.String())
	withdrawID := responseData[AccessRequest](t, withdraw.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Put("/role-access-requests/"+withdrawID.String()+"/withdrawal", requester, map[string]any{"reason": "changed"}).Code)

	reject := api.Post(tenant+"/role-access-requests", requester, map[string]any{
		"role_id": "20", "reason": "reject this request", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, reject.Code, reject.Body.String())
	rejectID := responseData[AccessRequest](t, reject.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Put("/role-access-requests/"+rejectID.String()+"/decision", reviewer, map[string]any{"decision": "rejected", "note": "rejected"}).Code)

	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	reviewResponse := api.Post(tenant+"/role-access-reviews", requester, map[string]any{
		"name": "Quarterly access review", "due_at": future,
	})
	require.Equal(t, http.StatusCreated, reviewResponse.Code, reviewResponse.Body.String())
	review := responseData[AccessReview](t, reviewResponse.Body.Bytes())
	assert.Equal(t, http.StatusOK, api.Get(tenant+"/role-access-reviews", reviewer).Code)
	assert.Equal(t, http.StatusOK, api.Get("/role-access-reviews/"+review.ID.String(), reviewer).Code)
	for _, item := range review.Items {
		response := api.Put(fmt.Sprintf("/role-access-reviews/%s/items/%s/decision", review.ID, item.ID), reviewer, map[string]any{
			"decision": "keep", "note": "certified",
		})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	assert.Equal(t, http.StatusOK, api.Patch("/role-access-reviews/"+review.ID.String(), reviewer, map[string]any{"status": "completed"}).Code)

	cancelResponse := api.Post(tenant+"/role-access-reviews", requester, map[string]any{
		"name": "Canceled access review", "due_at": future,
	})
	require.Equal(t, http.StatusCreated, cancelResponse.Code, cancelResponse.Body.String())
	cancelID := responseData[AccessReview](t, cancelResponse.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Patch("/role-access-reviews/"+cancelID.String(), reviewer, map[string]any{"status": "cancelled"}).Code) //nolint:misspell // Frozen API value.
}

func TestGovernanceHTTPValidationHumanPrincipalAndErrorMappings(t *testing.T) {
	_, api := newGovernanceHTTPHarness(t)
	tenant := "/tenants/1"
	service := "Authorization: Bearer service"
	assert.Equal(t, http.StatusForbidden, api.Post(tenant+"/role-access-requests", service, map[string]any{
		"role_id": "20", "reason": "service request", "access_expires_at": governanceTestNow.Add(time.Hour),
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/role-access-reviews/bad", "Authorization: Bearer 12").Code)
	assert.Equal(t, http.StatusForbidden, api.Get("/tenants/bad/role-access-requests", "Authorization: Bearer 12").Code)

	tests := []struct {
		err    error
		status int
		key    string
	}{
		{ErrNotFound, 404, governancei18n.RequestNotFound},
		{ErrRoleNotFound, 404, governancei18n.RoleNotFound},
		{ErrExpired, 410, governancei18n.RequestExpired},
		{ErrRequesterInactive, 409, governancei18n.RequesterInactive},
		{ErrAlreadyGranted, 409, governancei18n.AlreadyGranted},
		{ErrStateConflict, 409, governancei18n.RequestStateConflict},
		{ErrSelfApproval, 409, governancei18n.SelfApproval},
		{ErrRequesterOnly, 403, governancei18n.RequesterOnly},
		{ErrReviewNotFound, 404, governancei18n.ReviewNotFound},
		{ErrReviewItemNotFound, 404, governancei18n.ReviewItemNotFound},
		{ErrReviewExpired, 410, governancei18n.ReviewExpired},
		{ErrReviewEmpty, 409, governancei18n.ReviewEmpty},
		{ErrReviewStateConflict, 409, governancei18n.ReviewStateConflict},
		{ErrReviewIncomplete, 409, governancei18n.ReviewIncomplete},
		{ErrReviewSelfDecision, 409, governancei18n.ReviewSelfDecision},
		{core.ErrLastTenantAdministrator, 409, governancei18n.ReviewLastAdministrator},
		{ErrReviewDynamicDerived, 422, governancei18n.ReviewDerivedNotRevocable},
		{ErrInvalidReview, 422, governancei18n.InvalidReview},
		{ErrInvalid, 422, governancei18n.InvalidRequest},
		{errors.New("storage"), 500, governancei18n.Unavailable},
	}
	for _, test := range tests {
		var status huma.StatusError
		require.ErrorAs(t, governanceError(fmt.Errorf("wrapped: %w", test.err)), &status)
		assert.Equal(t, test.status, status.GetStatus())
		assert.Contains(t, status.Error(), test.key)
	}
	id, err := parseGovernanceID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)
	_, err = parseGovernanceID("bad")
	assert.Error(t, err)
}

func TestGovernanceHTTPRejectsInvalidReferencesAndUnauthorizedApprovers(t *testing.T) {
	respx.Install()
	_, declaration := humatest.New(t)
	RegisterHTTP(declaration, nil, authz.NewDeclarationOnlyRegistrar(authz.DefaultRegistry()))
	badTenant := "/tenants/bad"
	for _, response := range []*httptest.ResponseRecorder{
		declaration.Get(badTenant + "/requestable-roles"),
		declaration.Get(badTenant + "/role-access-requests"),
		declaration.Get(badTenant + "/role-access-reviews"),
		declaration.Get("/role-access-reviews/bad"),
		declaration.Put("/role-access-requests/bad/decision", map[string]any{"decision": "approved"}),
		declaration.Put("/role-access-requests/bad/grant-revocation", map[string]any{}),
		declaration.Put("/role-access-requests/bad/withdrawal", map[string]any{}),
		declaration.Patch("/role-access-reviews/bad", map[string]any{"status": "completed"}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}

	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedMember(t, 1, 11, "Subject")
	h.seedRole(t, 1, 20, "Operator")
	// The owner resolver runs before the permission check, so an absent resource
	// answers 404 and would hide the denial these assertions are about. Both
	// targets therefore have to exist before the denying registrar is used.
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	deniedRequest, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "denied approver check", AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	deniedReview, err := h.service.CreateReview(t.Context(), 1, 10, CreateAccessReview{
		Name: "Denied approver review", DueAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	require.NotEmpty(t, deniedReview.Items)
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), governanceHTTPVerifier{}, governanceDenyPermissions{}, governanceHTTPMembership{})
	_, deniedAPI := humatest.New(t)
	RegisterHTTP(deniedAPI, h.service, registrar)
	response := deniedAPI.Put("/role-access-requests/"+deniedRequest.ID.String()+"/decision", "Authorization: Bearer 10", map[string]any{"decision": "approved"})
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	response = deniedAPI.Put(fmt.Sprintf("/role-access-reviews/%s/items/%s/decision", deniedReview.ID, deniedReview.Items[0].ID), "Authorization: Bearer 10", map[string]any{"decision": "keep"})
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
}

// TestGovernanceHTTPReportsStorageFaultsOnEveryOperation drops the tables the
// service reads from and then walks every route. No handler may let a storage
// fault fall through as 404 or 409: a caller would then retry against a
// "missing" record that actually exists. Which server fault is correct depends
// on where the read failed. A tenant-scoped route reaches the handler and
// answers 500, while a globally addressed route fails in the owner resolver
// that runs ahead of the handler, so authz reports the authorization
// dependency as 503 exactly as it does for a failed membership or permission
// lookup.
func TestGovernanceHTTPReportsStorageFaultsOnEveryOperation(t *testing.T) {
	h, api := newGovernanceHTTPHarness(t)
	tenant := "/tenants/1"
	requester := "Authorization: Bearer 10"
	reviewer := "Authorization: Bearer 12"
	future := governanceTestNow.Add(24 * time.Hour).Format(time.RFC3339Nano)
	for _, name := range []string{
		"iam_access_requests", "iam_approval_steps", "iam_access_reviews", "iam_access_review_items",
		"iam_roles", "iam_role_members", "iam_tenant_members", "iam_temporary_role_grants",
	} {
		require.NoError(t, h.db.Exec("DROP TABLE "+h.table(name)).Error, name)
	}

	for _, broken := range []struct {
		name     string
		want     int
		response *httptest.ResponseRecorder
	}{
		{"requestable roles", http.StatusInternalServerError, api.Get(tenant+"/requestable-roles", requester)},
		{"create request", http.StatusInternalServerError, api.Post(tenant+"/role-access-requests", requester, map[string]any{
			"role_id": "20", "reason": "temporary operations access", "access_expires_at": future,
		})},
		{"my requests", http.StatusInternalServerError, api.Get(tenant+"/principals/"+"10"+"/role-access-requests", requester)},
		{"list requests", http.StatusInternalServerError, api.Get(tenant+"/role-access-requests", reviewer)},
		{"approve", http.StatusServiceUnavailable, api.Put("/role-access-requests/1001/decision", reviewer, map[string]any{"decision": "approved", "note": "ok"})},
		{"reject", http.StatusServiceUnavailable, api.Put("/role-access-requests/1001/decision", reviewer, map[string]any{"decision": "rejected", "note": "no"})},
		{"revoke", http.StatusServiceUnavailable, api.Put("/role-access-requests/1001/grant-revocation", reviewer, map[string]any{"reason": "done"})},
		{"withdraw", http.StatusServiceUnavailable, api.Put("/role-access-requests/1001/withdrawal", requester, map[string]any{"reason": "changed"})},
		{"list reviews", http.StatusInternalServerError, api.Get(tenant+"/role-access-reviews", reviewer)},
		{"create review", http.StatusInternalServerError, api.Post(tenant+"/role-access-reviews", requester, map[string]any{
			"name": "Quarterly access review", "due_at": future,
		})},
		{"get review", http.StatusServiceUnavailable, api.Get("/role-access-reviews/1001", reviewer)},
		{"decide item", http.StatusServiceUnavailable, api.Put("/role-access-reviews/1001/items/1002/decision", reviewer, map[string]any{
			"decision": "keep", "note": "certified",
		})},
		{"complete review", http.StatusServiceUnavailable, api.Patch("/role-access-reviews/1001", reviewer, map[string]any{"status": "completed"})},
		{"cancel review", http.StatusServiceUnavailable, api.Patch("/role-access-reviews/1001", reviewer, map[string]any{"status": "cancelled"})}, //nolint:misspell // Frozen API value.
	} {
		assert.Equal(t, broken.want, broken.response.Code, "%s: %s", broken.name, broken.response.Body.String())
		// The status must stay a server fault whatever the route class: a
		// storage outage reported as missing or conflicting would send the
		// caller into a retry against a record that does exist.
		assert.NotContains(t, []int{http.StatusNotFound, http.StatusConflict}, broken.response.Code, broken.name)
	}
}
