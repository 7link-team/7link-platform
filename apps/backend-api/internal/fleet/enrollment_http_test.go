package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestEnrollmentHTTPFailureReasonsNeverExposeCredentials(t *testing.T) {
	for _, fault := range []string{"pending", "expired", "denied", "wrong_proof", "decision_wrong_code", "overflow_decision", "overflow_exchange", "invalid_tenant", "iam_pending", "iam_expired", "iam_denied", "iam_invalid", "iam_missing"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, fake, _ := enrollmentHarness(t)
			started := startTestEnrollment(t, m, ctx)
			router := fleetBusinessRouter(t, m)
			method, path := http.MethodPost, "/machine-enrollments/"+started.ID.String()+"/credential"
			body := fmt.Sprintf(`{"tenant_id":"7","device_code":%q}`, started.Device.DeviceCode)
			status, reason := http.StatusConflict, "AUTHORIZATION_PENDING"
			switch fault {
			case "expired":
				require.NoError(t, db.Table(m.names.Enrollments.String()).Where("id = ?", started.ID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error)
				reason = "RESTART_ENROLLMENT"
			case "denied":
				require.NoError(t, m.ConfirmEnrollment(ctx, 7, started.ID, "ABCD-EFGH", false))
				reason = "RESTART_ENROLLMENT"
			case "wrong_proof":
				body = `{"tenant_id":"7","device_code":"wrong"}`
				status, reason = http.StatusNotFound, ""
			case "decision_wrong_code":
				method, path, body = http.MethodPut, "/tenants/7/machine-enrollments/"+started.ID.String()+"/decision", `{"user_code":"WRONG-CODE","approve":true}`
				status, reason = http.StatusNotFound, ""
			case "overflow_decision":
				method, path, body = http.MethodPut, "/tenants/7/machine-enrollments/9999999999999999999/decision", `{"user_code":"ABCD-EFGH","approve":true}`
				status, reason = http.StatusUnprocessableEntity, ""
			case "overflow_exchange":
				path, status, reason = "/machine-enrollments/9999999999999999999/credential", http.StatusUnprocessableEntity, ""
			case "invalid_tenant":
				body, status, reason = `{"tenant_id":"0","device_code":"test-device-proof"}`, http.StatusUnprocessableEntity, ""
			case "iam_pending", "iam_expired", "iam_denied", "iam_invalid", "iam_missing":
				failures := map[string]error{"iam_pending": oauth.ErrAuthorizationPending, "iam_expired": oauth.ErrExpiredToken, "iam_denied": oauth.ErrAccessDenied, "iam_invalid": oauth.ErrInvalidRequest, "iam_missing": oauth.ErrDeviceRequestNotFound}
				m.enrollmentIAM.StartDevice = func(context.Context, string, string) (oauth.DeviceAuthorization, error) {
					return oauth.DeviceAuthorization{}, fmt.Errorf("private IAM context: %w", failures[fault])
				}
				path, body = "/machine-enrollments", `{"tenant_id":"7","client_id":"20","name":"runner","os":"LINUX"}`
				reason = "RESTART_ENROLLMENT"
				if fault == "iam_pending" {
					reason = "AUTHORIZATION_PENDING"
				}
				if fault == "iam_invalid" {
					status, reason = http.StatusUnprocessableEntity, ""
				}
			}
			response := fleetHTTPRequest(router, method, path, body, nil)
			require.Equal(t, status, response.Code, response.Body.String())
			if reason != "" {
				require.Contains(t, response.Body.String(), reason)
			}
			require.NotContains(t, response.Body.String(), "test-one-time-secret")
			require.NotContains(t, response.Body.String(), "private IAM context")
			require.Zero(t, fake.issued)
		})
	}
}

type enrollmentHTTPAuthority struct{}

func (enrollmentHTTPAuthority) Authenticate(_ context.Context, authorization, _ string) (*authn.Claims, error) {
	if authorization != "Bearer test" {
		return nil, errors.New("missing test authorization")
	}
	return &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypePrincipal}, nil
}
func (enrollmentHTTPAuthority) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}
func (enrollmentHTTPAuthority) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return false, nil
}
func (enrollmentHTTPAuthority) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func TestEnrollmentHTTPContractAndCredentialFlow(t *testing.T) {
	m, _, _, fake, _ := enrollmentHarness(t)
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, registry.Contribute([]authz.Action{{Resource: "machine", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
	authority := enrollmentHTTPAuthority{}
	m.registrar = authz.NewRegistrar(registry, authority, authority, authority)
	router := chi.NewRouter()
	api := humax.New(router, humax.Config{Name: "test", Version: "1"})
	require.NoError(t, m.registerEnrollmentHTTP(api), "all OPTIONS contracts must target the actual registered paths")

	start := api.OpenAPI().Paths["/machine-enrollments"].Post
	decision := api.OpenAPI().Paths["/tenants/{tenant_id}/machine-enrollments/{enrollment_id}/decision"].Put
	exchange := api.OpenAPI().Paths["/machine-enrollments/{enrollment_id}/credential"].Post
	lookup := api.OpenAPI().Paths["/tenants/{tenant_id}/machine-enrollments"].Get
	require.NotNil(t, lookup)
	require.Equal(t, authn.RateLimitOAuth, lookup.Metadata[authn.RateLimitScopeMetadata])
	require.NotEmpty(t, lookup.Security)
	lookupGuard, ok := authz.OperationGuard(lookup)
	require.True(t, ok)
	require.Equal(t, "machine_manage", lookupGuard)
	require.True(t, authz.IsPublicOperation(start))
	require.Empty(t, start.Security)
	require.True(t, authz.IsPublicOperation(exchange))
	require.Empty(t, exchange.Security)
	require.NotEmpty(t, decision.Security)
	permission, ok := authz.OperationGuard(decision)
	require.True(t, ok)
	require.Equal(t, "machine_manage", permission)
	for _, path := range []string{"/machine-enrollments", "/tenants/{tenant_id}/machine-enrollments/{enrollment_id}/decision", "/machine-enrollments/{enrollment_id}/credential"} {
		require.NotNil(t, api.OpenAPI().Paths[path].Options)
	}
	for _, op := range []*huma.Operation{decision, exchange} {
		var selector *huma.Param
		for _, parameter := range op.Parameters {
			if parameter.In == "path" && parameter.Name == "enrollment_id" {
				selector = parameter
			}
		}
		require.NotNil(t, selector)
		require.True(t, selector.Required)
		require.Equal(t, "string", selector.Schema.Type)
		require.Equal(t, `^[1-9][0-9]*$`, selector.Schema.Pattern)
		require.NotNil(t, selector.Schema.MaxLength)
		require.Equal(t, 19, *selector.Schema.MaxLength)
	}

	request := func(method, path, body string, authorized bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if authorized {
			r.Header.Set("Authorization", "Bearer test")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/machine-enrollments", `{}`},
		{http.MethodPost, "/machine-enrollments", `{"tenant_id":"7","client_id":"20","name":"runner","os":"INVALID"}`},
		{http.MethodPost, "/machine-enrollments/invalid/credential", `{"tenant_id":"7","device_code":"proof"}`},
		{http.MethodPost, "/machine-enrollments/1/credential", `{}`},
		{http.MethodPut, "/tenants/7/machine-enrollments/invalid/decision", `{"user_code":"ABCD-EFGH","approve":true}`},
		{http.MethodPut, "/tenants/7/machine-enrollments/1/decision", `{}`},
	} {
		response := request(tc.method, tc.path, tc.body, true)
		require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
	}
	require.Zero(t, fake.created)
	require.Zero(t, fake.issued)
	response := request(http.MethodPost, "/machine-enrollments", `{"tenant_id":"7","client_id":"20","name":"runner","os":"LINUX"}`, false)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var started struct {
		Data EnrollmentStarted `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &started))
	require.Positive(t, int64(started.Data.ID))
	lookupPath := "/tenants/7/machine-enrollments?user_code=ABCD-EFGH"
	response = request(http.MethodGet, lookupPath, "", false)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	response = request(http.MethodGet, lookupPath, "", true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Body.String(), started.Data.ID.String())
	require.NotContains(t, response.Body.String(), started.Data.Device.DeviceCode)
	response = request(http.MethodGet, "/tenants/7/machine-enrollments?user_code=WRONG-CODE", "", true)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	decisionPath := "/tenants/7/machine-enrollments/" + started.Data.ID.String() + "/decision"
	response = request(http.MethodPut, decisionPath, `{"user_code":"ABCD-EFGH","approve":true}`, false)
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	response = request(http.MethodPut, decisionPath, `{"user_code":"ABCD-EFGH","approve":true,"name":"approved-name"}`, true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = request(http.MethodGet, lookupPath, "", true)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	exchangePath := "/machine-enrollments/" + started.Data.ID.String() + "/credential"
	exchangeBody := fmt.Sprintf(`{"tenant_id":"7","device_code":%q}`, started.Data.Device.DeviceCode)
	response = request(http.MethodPost, exchangePath, exchangeBody, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Body.String(), "test-one-time-secret")
	response = request(http.MethodPost, exchangePath, exchangeBody, false)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "test-one-time-secret")
	require.Equal(t, 1, fake.issued)
}

func TestEnrollmentHTTPPersistentSlowDown(t *testing.T) {
	m, _, ctx, _, _ := enrollmentHarness(t)
	started := startTestEnrollment(t, m, ctx)
	router := fleetBusinessRouter(t, m)
	path := "/machine-enrollments/" + started.ID.String() + "/credential"
	body := fmt.Sprintf(`{"tenant_id":"7","device_code":%q}`, started.Device.DeviceCode)
	first := fleetHTTPRequest(router, http.MethodPost, path, body, nil)
	require.Equal(t, 409, first.Code)
	require.Contains(t, first.Body.String(), "AUTHORIZATION_PENDING")
	second := fleetHTTPRequest(router, http.MethodPost, path, body, nil)
	require.Equal(t, 409, second.Code)
	require.Contains(t, second.Body.String(), "SLOW_DOWN")
	require.NotContains(t, second.Body.String(), started.Device.DeviceCode)
}
