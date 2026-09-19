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
governance-create-access-request|POST|/access-requests
governance-create-access-review|POST|/access-reviews
governance-decide-access-review-item|PUT|/access-reviews/{review_id}/items/{item_id}/decision
governance-get-access-review|GET|/access-reviews/{review_id}
governance-list-access-requests|GET|/access-requests
governance-list-access-reviews|GET|/access-reviews
governance-list-my-access-requests|GET|/me/access-requests
governance-list-requestable-roles|GET|/requestable-roles
governance-set-access-request-decision|PUT|/access-requests/{request_id}/decision
governance-revoke-access-grant|PUT|/access-requests/{request_id}/grant-revocation
governance-update-access-review|PATCH|/access-reviews/{review_id}
governance-withdraw-access-request|PUT|/access-requests/{request_id}/withdrawal`

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
			assert.Equal(t, []string{"governance"}, operation.Tags, location)
			assert.NotEmpty(t, operation.Security, location)
			var tenantHeaders int
			for _, parameter := range operation.Parameters {
				if parameter.Name == authz.TenantHeader && parameter.In == "header" {
					tenantHeaders++
					assert.True(t, parameter.Required, location)
				}
			}
			assert.Equal(t, 1, tenantHeaders, location)
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
	tenant := authz.TenantHeader + ": 1"
	requester := "Authorization: Bearer 10"
	reviewer := "Authorization: Bearer 12"
	future := governanceTestNow.Add(24 * time.Hour).Format(time.RFC3339Nano)

	assert.Equal(t, http.StatusOK, api.Get("/requestable-roles", requester, tenant).Code)
	created := api.Post("/access-requests", requester, tenant, map[string]any{
		"role_id": "20", "reason": "temporary operations access", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	request := responseData[AccessRequest](t, created.Body.Bytes())
	assert.Equal(t, http.StatusOK, api.Get("/me/access-requests", requester, tenant).Code)
	assert.Equal(t, http.StatusOK, api.Get("/access-requests", reviewer, tenant).Code)
	approved := api.Put("/access-requests/"+request.ID.String()+"/decision", reviewer, tenant, map[string]any{"decision": "approved", "note": "approved"})
	require.Equal(t, http.StatusOK, approved.Code, approved.Body.String())
	assert.Equal(t, http.StatusOK, api.Put("/access-requests/"+request.ID.String()+"/grant-revocation", reviewer, tenant, map[string]any{"reason": "finished"}).Code)

	withdraw := api.Post("/access-requests", requester, tenant, map[string]any{
		"role_id": "20", "reason": "withdraw this request", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, withdraw.Code, withdraw.Body.String())
	withdrawID := responseData[AccessRequest](t, withdraw.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Put("/access-requests/"+withdrawID.String()+"/withdrawal", requester, tenant, map[string]any{"reason": "changed"}).Code)

	reject := api.Post("/access-requests", requester, tenant, map[string]any{
		"role_id": "20", "reason": "reject this request", "access_expires_at": future,
	})
	require.Equal(t, http.StatusCreated, reject.Code, reject.Body.String())
	rejectID := responseData[AccessRequest](t, reject.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Put("/access-requests/"+rejectID.String()+"/decision", reviewer, tenant, map[string]any{"decision": "rejected", "note": "rejected"}).Code)

	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 11, CreatedAt: governanceTestNow.Add(-time.Hour),
	}).Error)
	reviewResponse := api.Post("/access-reviews", requester, tenant, map[string]any{
		"name": "Quarterly access review", "due_at": future,
	})
	require.Equal(t, http.StatusCreated, reviewResponse.Code, reviewResponse.Body.String())
	review := responseData[AccessReview](t, reviewResponse.Body.Bytes())
	assert.Equal(t, http.StatusOK, api.Get("/access-reviews", reviewer, tenant).Code)
	assert.Equal(t, http.StatusOK, api.Get("/access-reviews/"+review.ID.String(), reviewer, tenant).Code)
	for _, item := range review.Items {
		response := api.Put(fmt.Sprintf("/access-reviews/%s/items/%s/decision", review.ID, item.ID), reviewer, tenant, map[string]any{
			"decision": "keep", "note": "certified",
		})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	assert.Equal(t, http.StatusOK, api.Patch("/access-reviews/"+review.ID.String(), reviewer, tenant, map[string]any{"status": "completed"}).Code)

	cancelResponse := api.Post("/access-reviews", requester, tenant, map[string]any{
		"name": "Canceled access review", "due_at": future,
	})
	require.Equal(t, http.StatusCreated, cancelResponse.Code, cancelResponse.Body.String())
	cancelID := responseData[AccessReview](t, cancelResponse.Body.Bytes()).ID
	assert.Equal(t, http.StatusOK, api.Patch("/access-reviews/"+cancelID.String(), reviewer, tenant, map[string]any{"status": "cancelled"}).Code) //nolint:misspell // Frozen API value.
}

func TestGovernanceHTTPValidationHumanPrincipalAndErrorMappings(t *testing.T) {
	_, api := newGovernanceHTTPHarness(t)
	tenant := authz.TenantHeader + ": 1"
	service := "Authorization: Bearer service"
	assert.Equal(t, http.StatusForbidden, api.Post("/access-requests", service, tenant, map[string]any{
		"role_id": "20", "reason": "service request", "access_expires_at": governanceTestNow.Add(time.Hour),
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Get("/access-reviews/bad", "Authorization: Bearer 12", tenant).Code)
	assert.Equal(t, http.StatusForbidden, api.Get("/access-requests", "Authorization: Bearer 12", authz.TenantHeader+": bad").Code)

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
	badTenant := authz.TenantHeader + ": bad"
	goodTenant := authz.TenantHeader + ": 1"
	for _, response := range []*httptest.ResponseRecorder{
		declaration.Get("/requestable-roles", badTenant),
		declaration.Get("/access-requests", badTenant),
		declaration.Get("/access-reviews", badTenant),
		declaration.Get("/access-reviews/bad", goodTenant),
		declaration.Put("/access-requests/bad/decision", goodTenant, map[string]any{"decision": "approved"}),
		declaration.Put("/access-requests/bad/grant-revocation", goodTenant, map[string]any{}),
		declaration.Put("/access-requests/bad/withdrawal", goodTenant, map[string]any{}),
		declaration.Patch("/access-reviews/bad", goodTenant, map[string]any{"status": "completed"}),
	} {
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}

	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedRole(t, 1, 20, "Operator")
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), governanceHTTPVerifier{}, governanceDenyPermissions{}, governanceHTTPMembership{})
	_, deniedAPI := humatest.New(t)
	RegisterHTTP(deniedAPI, h.service, registrar)
	response := deniedAPI.Put("/access-requests/1/decision", "Authorization: Bearer 10", goodTenant, map[string]any{"decision": "approved"})
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = deniedAPI.Put("/access-reviews/1/items/1/decision", "Authorization: Bearer 10", goodTenant, map[string]any{"decision": "keep"})
	assert.Equal(t, http.StatusForbidden, response.Code)
}

// TestGovernanceHTTPReportsStorageFaultsOnEveryOperation drops the tables the
// service reads from and then walks every route. A broken database has to come
// back as 500 from all of them: if one handler let a storage fault fall through
// as 404 or 409 instead, a caller would retry against a "missing" record that
// actually exists.
func TestGovernanceHTTPReportsStorageFaultsOnEveryOperation(t *testing.T) {
	h, api := newGovernanceHTTPHarness(t)
	tenant := authz.TenantHeader + ": 1"
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
		response *httptest.ResponseRecorder
	}{
		{"requestable roles", api.Get("/requestable-roles", requester, tenant)},
		{"create request", api.Post("/access-requests", requester, tenant, map[string]any{
			"role_id": "20", "reason": "temporary operations access", "access_expires_at": future,
		})},
		{"my requests", api.Get("/me/access-requests", requester, tenant)},
		{"list requests", api.Get("/access-requests", reviewer, tenant)},
		{"approve", api.Put("/access-requests/1001/decision", reviewer, tenant, map[string]any{"decision": "approved", "note": "ok"})},
		{"reject", api.Put("/access-requests/1001/decision", reviewer, tenant, map[string]any{"decision": "rejected", "note": "no"})},
		{"revoke", api.Put("/access-requests/1001/grant-revocation", reviewer, tenant, map[string]any{"reason": "done"})},
		{"withdraw", api.Put("/access-requests/1001/withdrawal", requester, tenant, map[string]any{"reason": "changed"})},
		{"list reviews", api.Get("/access-reviews", reviewer, tenant)},
		{"create review", api.Post("/access-reviews", requester, tenant, map[string]any{
			"name": "Quarterly access review", "due_at": future,
		})},
		{"get review", api.Get("/access-reviews/1001", reviewer, tenant)},
		{"decide item", api.Put("/access-reviews/1001/items/1002/decision", reviewer, tenant, map[string]any{
			"decision": "keep", "note": "certified",
		})},
		{"complete review", api.Patch("/access-reviews/1001", reviewer, tenant, map[string]any{"status": "completed"})},
		{"cancel review", api.Patch("/access-reviews/1001", reviewer, tenant, map[string]any{"status": "cancelled"})}, //nolint:misspell // Frozen API value.
	} {
		assert.Equal(t, http.StatusInternalServerError, broken.response.Code, "%s: %s", broken.name, broken.response.Body.String())
	}
}
