package authn

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/mojocn/base64Captcha"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func parseGUID(value string) guid.ID {
	id, _ := guid.Parse(value)
	return id
}

func guidString(value guid.ID) string { return value.String() }

type fixedTokenVerifier struct{ claims *authnext.Claims }

func (verifier fixedTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return verifier.claims, nil
}

func TestLogoutStorageFailurePreservesSessionUntilRetry(t *testing.T) {
	db, web, principalID := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)
	token, err := web.CreateSession(t.Context(), parseGUID(principalID), time.Now().UTC(), authnext.Assurance{Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	cookie := web.SessionCookie(token)
	writeFailure := errors.New("injected session write failure")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:logout_failure", func(tx *gorm.DB) {
		if strings.HasSuffix(tx.Statement.Table, "iam_sessions") {
			_ = tx.AddError(writeFailure)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove("test:logout_failure") })
	location, err := web.Logout(t.Context(), cookie)
	require.ErrorIs(t, err, writeFailure)
	assert.Empty(t, location)
	failed := api.Delete("/sessions/current", "Cookie: "+cookie, "Origin: https://app.example")
	require.Equal(t, http.StatusServiceUnavailable, failed.Code, failed.Body.String())
	assert.Empty(t, failed.Header().Values("Set-Cookie"))
	assert.NotContains(t, failed.Body.String(), "logout_url")
	_, err = web.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err, "failed revocation must remain visible as an active session")
	require.NoError(t, db.Callback().Update().Remove("test:logout_failure"))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = web.Logout(canceled, cookie)
	require.ErrorIs(t, err, context.Canceled)
	_, err = web.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	retry := api.Delete("/sessions/current", "Cookie: "+cookie, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	assert.Contains(t, retry.Header().Get("Set-Cookie"), "Max-Age=0")
	_, err = web.Authenticate(t.Context(), "", cookie)
	require.Error(t, err)
	for _, previous := range []string{cookie, "", web.SessionCookie("unknown")} {
		_, err = web.Logout(t.Context(), previous)
		require.NoError(t, err, "logout is idempotent after confirmed revocation")
	}
	web.repo = nil
	_, err = web.Logout(t.Context(), cookie)
	require.ErrorContains(t, err, "authentication storage unavailable")
}

func TestAuthenticationHTTPFlow(t *testing.T) {
	db, web, principalID := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, cookie)

	session := api.Get("/sessions/current", "Cookie: "+cookie)
	assert.Equal(t, http.StatusOK, session.Code, session.Body.String())
	assert.Contains(t, session.Body.String(), principalID)
	assert.NotContains(t, session.Body.String(), `"email_verified"`, "an unscoped session cannot expose tenant-controlled profile fields")

	accessToken, _, err := web.IssueAccessToken(context.Background(), parseGUID(principalID), "api", "openid")
	require.NoError(t, err)
	me := api.Get("/me", "Authorization: Bearer "+accessToken)
	assert.Equal(t, http.StatusOK, me.Code, me.Body.String())

	logout := api.Delete("/sessions/current", "Cookie: "+cookie, "Origin: https://app.example")
	assert.Equal(t, http.StatusOK, logout.Code, logout.Body.String())
	assert.Contains(t, logout.Header().Get("Set-Cookie"), "Max-Age=0")
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+cookie).Code)

	assert.Equal(t, http.StatusUnauthorized, api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "wrong password",
	}, "Origin: https://app.example").Code)
	// The allowlist admits an origin at or below a path, so the login page may
	// send somebody back to wherever they were heading without every route
	// being enumerated in deployment configuration.
	assert.Equal(t, http.StatusOK, api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
		"return_url": "https://app.example/tenants/42/members?page=2",
	}, "Origin: https://app.example").Code)
	// The origin is still matched exactly, which is the half that makes this an
	// open-redirect guard rather than a formality.
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
		"return_url": "https://app.example.evil.test/home",
	}, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions/current", "Cookie: "+cookie, "Origin: https://evil.example").Code)

	err = db.WithContext(context.Background()).Table("iam_credentials").Where("principal_id = ?", principalID).Update("mfa_required", true).Error
	require.NoError(t, err)
	enrollmentLogin := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, enrollmentLogin.Code, enrollmentLogin.Body.String())
	assert.Contains(t, enrollmentLogin.Body.String(), `"status":"mfa_enrollment_required"`)
	assert.NotEmpty(t, enrollmentLogin.Header().Get("Set-Cookie"))
	assert.Equal(t, http.StatusUnauthorized, api.Get("/me", "Authorization: Bearer invalid").Code)
}

func TestSessionHTTPTenantResolutionAndImmediateRevocation(t *testing.T) {
	db, web, principalID := newAuthenticationService(t)
	web.fields = profileCheckerFunc(allowProfileFields)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)
	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := "Cookie: " + strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	check := func(code int, expected string) {
		t.Helper()
		for _, path := range []string{"/me", "/sessions/current"} {
			response := api.Get(path, cookie)
			require.Equal(t, code, response.Code, response.Body.String())
			if code == http.StatusOK {
				var body struct {
					Data meOutput `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
				assert.Equal(t, expected, body.Data.OrganizationID)
				assert.Equal(t, principalID, body.Data.Subject)
			}
		}
	}
	check(http.StatusOK, "")
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,status) VALUES (101,'active'),(102,'active'),(103,'disabled')").Error)
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (101,?,'active'),(103,?,'active')", principalID, principalID).Error)
	check(http.StatusOK, "101")
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (102,?,'active')", principalID).Error)
	check(http.StatusOK, "")
	require.NoError(t, db.Exec("UPDATE iam_tenant_members SET status = 'disabled' WHERE tenant_id = 102").Error)
	check(http.StatusOK, "101")
	require.NoError(t, db.Exec("UPDATE iam_tenants SET status = 'disabled' WHERE id = 101").Error)
	check(http.StatusOK, "")
	web.memberTenant = nil
	check(http.StatusServiceUnavailable, "")
	web.memberTenant = func(context.Context, guid.ID, guid.ID) (guid.ID, error) {
		return 0, errors.New("membership store unavailable")
	}
	check(http.StatusServiceUnavailable, "")
}

func TestSessionTenantCannotRewriteSignedTokenOrMutateVerifierClaims(t *testing.T) {
	claims := &authnext.Claims{PrincipalID: 11, OrganizationID: "101"}
	web := &Service{memberTenant: func(context.Context, guid.ID, guid.ID) (guid.ID, error) {
		return 102, nil
	}}
	resolved, err := sessionTenant(t.Context(), web, &meInput{Authorization: "Bearer token"}, claims)
	require.NoError(t, err)
	assert.Same(t, claims, resolved)
	resolved, err = sessionTenant(t.Context(), web, &meInput{}, claims)
	require.NoError(t, err)
	assert.Equal(t, "102", resolved.OrganizationID)
	assert.Equal(t, guid.ID(102), resolved.TenantID)
	assert.Equal(t, "101", claims.OrganizationID)
	assert.Zero(t, claims.TenantID)
	_, err = sessionTenant(t.Context(), nil, &meInput{}, claims)
	require.Error(t, err)
}

func TestRequiredMFAEnrollmentHTTPFlowDoesNotLockOutPrincipal(t *testing.T) {
	db, web, principalID := newAuthenticationService(t)
	require.NoError(t, db.WithContext(t.Context()).Table("iam_credentials").
		Where("principal_id = ?", principalID).Update("mfa_required", true).Error)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	assert.Contains(t, login.Body.String(), `"status":"mfa_enrollment_required"`)
	restrictedCookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, restrictedCookie)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+restrictedCookie).Code)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/me/passkeys", "Cookie: "+restrictedCookie).Code)

	enrollment := api.Post("/me/mfa/totp-enrollments", "Cookie: "+restrictedCookie,
		"Origin: https://app.example", map[string]any{"current_password": "correct horse battery staple"})
	require.Equal(t, http.StatusOK, enrollment.Code, enrollment.Body.String())
	var enrollmentEnvelope struct {
		Data authnext.MFAEnrollment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enrollment.Body.Bytes(), &enrollmentEnvelope))
	confirmation := api.Post("/me/mfa/totp", "Cookie: "+restrictedCookie,
		"Origin: https://app.example", map[string]any{"code": mustTOTPValue(t, enrollmentEnvelope.Data.Secret)})
	require.Equal(t, http.StatusOK, confirmation.Code, confirmation.Body.String())
	assert.Contains(t, confirmation.Header().Get("Set-Cookie"), "Max-Age=0")
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+restrictedCookie).Code)

	var confirmationEnvelope struct {
		Data authnext.MFAConfirmation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(confirmation.Body.Bytes(), &confirmationEnvelope))
	require.NotEmpty(t, confirmationEnvelope.Data.RecoveryCodes)
	challenge := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, challenge.Code, challenge.Body.String())
	assert.Empty(t, challenge.Header().Get("Set-Cookie"))
	var challengeEnvelope struct {
		Data authnext.LoginResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(challenge.Body.Bytes(), &challengeEnvelope))
	assert.Equal(t, "mfa_required", challengeEnvelope.Data.Status)

	verified := api.Post("/session-verifications", map[string]any{
		"challenge_id": challengeEnvelope.Data.ChallengeID,
		"code":         confirmationEnvelope.Data.RecoveryCodes[0],
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, verified.Code, verified.Body.String())
	assert.Equal(t, http.StatusOK, api.Get("/sessions/current",
		"Cookie: "+strings.Split(verified.Header().Get("Set-Cookie"), ";")[0]).Code)
}

func TestAuthenticationSecurityCenterHTTPFlow(t *testing.T) {
	_, web, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)
	seedLocalPrincipal(t, web.repo, "bob", "bob@example.com", "correct horse battery staple")
	bobLogin, err := web.BeginLogin(t.Context(), "bob", "correct horse battery staple", "")
	require.NoError(t, err)
	login := func(password string) string {
		response := api.Post("/sessions", map[string]any{
			"login_name": "alice", "password": password, "return_url": "https://app.example/",
		}, "Origin: https://app.example")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		return strings.Split(response.Header().Get("Set-Cookie"), ";")[0]
	}
	first, second := login("correct horse battery staple"), login("correct horse battery staple")
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions").Code)
	sessions := api.Get("/sessions", "Cookie: "+first)
	require.Equal(t, http.StatusOK, sessions.Code, sessions.Body.String())
	var envelope struct {
		Data []authnext.BrowserSession `json:"data"`
	}
	require.NoError(t, json.Unmarshal(sessions.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, 2)
	for _, session := range envelope.Data {
		assert.NotEqual(t, tokenHash(bobLogin.SessionID), session.ID)
	}
	assert.Equal(t, http.StatusNotFound, api.Delete("/sessions/"+tokenHash(bobLogin.SessionID),
		"Cookie: "+first, "Origin: https://app.example").Code)
	_, err = web.Authenticate(t.Context(), "", web.SessionCookie(bobLogin.SessionID))
	require.NoError(t, err, "another principal's session must remain active")
	var other string
	for _, session := range envelope.Data {
		if !session.Current {
			other = session.ID
		}
	}
	require.NotEmpty(t, other)
	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions/"+other, "Cookie: "+first, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusOK, api.Delete("/sessions/"+other, "Cookie: "+first, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+second).Code)
	assert.Equal(t, http.StatusNotFound, api.Delete("/sessions/"+strings.Repeat("a", 64), "Cookie: "+first, "Origin: https://app.example").Code)

	changed := api.Patch("/me/password", "Cookie: "+first, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple", "new_password": "new correct horse battery staple",
	})
	assert.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	assert.Equal(t, http.StatusForbidden, api.Patch("/me/password", "Cookie: "+first, "Origin: https://evil.example", map[string]any{
		"current_password": "new correct horse battery staple", "new_password": "another correct horse battery staple",
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Patch("/me/password", "Cookie: "+first, "Origin: https://app.example", map[string]any{
		"current_password": "new correct horse battery staple", "new_password": "correct horse battery staple",
	}).Code)
	assert.Equal(t, http.StatusUnauthorized, api.Patch("/me/password", "Cookie: "+first, "Origin: https://app.example", map[string]any{
		"current_password": "wrong password", "new_password": "another correct horse battery staple",
	}).Code)
	third := login("new correct horse battery staple")
	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions", "Cookie: "+third, "Origin: https://evil.example").Code)
	logoutAll := api.Delete("/sessions", "Cookie: "+third, "Origin: https://app.example")
	assert.Equal(t, http.StatusOK, logoutAll.Code, logoutAll.Body.String())
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+first).Code)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+third).Code)
}

func TestPasswordRecoveryHTTPFlow(t *testing.T) {
	deliveries := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Type        string `json:"type"`
			RecoveryURL string `json:"recovery_url"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Type == "password_recovery" {
			parsed, err := url.Parse(payload.RecoveryURL)
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			deliveries <- parsed.Query().Get("token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	_, web, _ := newRecoveryAuthenticationService(t, server.URL)
	require.NoError(t, web.StartNotificationWorker(t.Context()))
	t.Cleanup(func() { _ = web.StopNotificationWorker(context.Background()) })
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	assert.Equal(t, http.StatusForbidden, api.Post("/password-recovery-requests", map[string]any{"identifier": "alice"}, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/password-recovery-requests", map[string]any{}, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/password-recovery-requests", "Content-Type: application/json", "Origin: https://app.example", strings.NewReader(`"not-an-object"`)).Code)
	missing := api.Post("/password-recovery-requests", map[string]any{"identifier": "missing@example.com"}, "Origin: https://app.example")
	assert.Equal(t, http.StatusAccepted, missing.Code, missing.Body.String())
	started := api.Post("/password-recovery-requests", map[string]any{"identifier": "alice@example.com"}, "Origin: https://app.example")
	require.Equal(t, http.StatusAccepted, started.Code, started.Body.String())

	var token string
	select {
	case token = <-deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery webhook was not delivered")
	}
	assert.Equal(t, http.StatusBadRequest, api.Post("/password-recoveries", map[string]any{
		"token": strings.Repeat("x", 40), "new_password": "new correct horse battery staple",
	}, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusConflict, api.Post("/password-recoveries", map[string]any{
		"token": token, "new_password": "correct horse battery staple",
	}, "Origin: https://app.example").Code)
	completed := api.Post("/password-recoveries", map[string]any{
		"token": token, "new_password": "new correct horse battery staple",
	}, "Origin: https://app.example")
	assert.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	assert.Equal(t, http.StatusForbidden, api.Post("/password-recoveries", map[string]any{
		"token": token, "new_password": "another correct horse battery staple",
	}, "Origin: https://evil.example").Code)

	startOperation := api.OpenAPI().Paths["/password-recovery-requests"].Post
	completeOperation := api.OpenAPI().Paths["/password-recoveries"].Post
	require.NotNil(t, startOperation)
	require.NotNil(t, completeOperation)
	assert.Equal(t, "authn-create-password-recovery-request", startOperation.OperationID)
	assert.Equal(t, "authn-create-password-recovery", completeOperation.OperationID)
	assert.Contains(t, startOperation.Responses, "202")
}

func TestEmailVerificationHTTPFlow(t *testing.T) {
	deliveries := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload struct {
			Type            string `json:"type"`
			VerificationURL string `json:"verification_url"`
		}
		if !assert.NoError(t, json.NewDecoder(request.Body).Decode(&payload)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Type == "email_verification" {
			parsed, err := url.Parse(payload.VerificationURL)
			if !assert.NoError(t, err) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			deliveries <- parsed.Query().Get("token")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	db, web, principalID := newRecoveryAuthenticationService(t, server.URL)
	web.fields = profileCheckerFunc(allowProfileFields)
	require.NoError(t, db.Exec("INSERT INTO iam_tenants (id,status) VALUES (101,'active')").Error)
	require.NoError(t, db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (101,?,'active')", principalID).Error)
	err := db.WithContext(t.Context()).Table("iam_principals").
		Where("id = ?", principalID).Update("email_verified", false).Error
	require.NoError(t, err)
	require.NoError(t, web.StartNotificationWorker(t.Context()))
	t.Cleanup(func() { _ = web.StopNotificationWorker(context.Background()) })
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	assert.Contains(t, api.Get("/sessions/current", "Cookie: "+cookie).Body.String(), `"email_verified":false`)
	assert.Equal(t, http.StatusUnauthorized, api.Post("/email-verification-requests", "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/email-verification-requests", "Cookie: "+cookie, "Origin: https://evil.example").Code)
	started := api.Post("/email-verification-requests", "Cookie: "+cookie, "Origin: https://app.example")
	require.Equal(t, http.StatusAccepted, started.Code, started.Body.String())

	var token string
	select {
	case token = <-deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("email verification webhook was not delivered")
	}
	assert.Equal(t, http.StatusForbidden, api.Post("/email-verifications", map[string]any{"token": token}, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusBadRequest, api.Post("/email-verifications", map[string]any{"token": strings.Repeat("x", 40)}, "Origin: https://app.example").Code)
	completed := api.Post("/email-verifications", map[string]any{"token": token}, "Origin: https://app.example")
	assert.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	assert.Contains(t, api.Get("/sessions/current", "Cookie: "+cookie).Body.String(), `"email_verified":true`)

	err = db.WithContext(t.Context()).Table("iam_principals").Where("id = ?", principalID).Updates(map[string]any{"email": "", "email_verified": false}).Error
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, api.Post("/email-verification-requests", "Cookie: "+cookie, "Origin: https://app.example").Code)

	startOperation := api.OpenAPI().Paths["/email-verification-requests"].Post
	completeOperation := api.OpenAPI().Paths["/email-verifications"].Post
	require.NotNil(t, startOperation)
	require.NotNil(t, completeOperation)
	assert.Equal(t, "authn-create-email-verification-request", startOperation.OperationID)
	assert.Equal(t, "authn-create-email-verification", completeOperation.OperationID)
	assert.Contains(t, startOperation.Responses, "202")
}

func TestRegistrationHTTPFlow(t *testing.T) {
	db, web, _ := newRecoveryAuthenticationService(t, "https://notify.example.test/events", true)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	capabilities := api.Get("/authentication-capabilities")
	require.Equal(t, http.StatusOK, capabilities.Code, capabilities.Body.String())
	assert.Contains(t, capabilities.Body.String(), `"registration":true`)
	assert.Contains(t, capabilities.Body.String(), `"password_recovery":true`)

	assert.Equal(t, http.StatusForbidden, api.Post("/registrations", map[string]any{
		"email": "registered@example.com", "password": "correct registration password", "display_name": "Registered User",
		"captcha_type": "text", "captcha_id": "invalid", "captcha_answer": "invalid",
	}, "Origin: https://evil.example").Code)
	invalid := registrationRequestWithCaptcha(t, api, "not-an-email", "short", "")
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/registrations", invalid,
		"Origin: https://app.example").Code)

	for range 2 {
		request := registrationRequestWithCaptcha(t, api, "registered@example.com", "correct registration password", "Registered User")
		response := api.Post("/registrations", request, "Origin: https://app.example")
		assert.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"accepted":true`)
	}
	require.NoError(t, db.WithContext(t.Context()).Table("iam_principals").
		Where("email = ?", "registered@example.com").Update("activation_required", false).Error)
	activeDuplicate := api.Post("/registrations",
		registrationRequestWithCaptcha(t, api, "registered@example.com", "different registration password", "Different Name"),
		"Origin: https://app.example")
	assert.Equal(t, http.StatusAccepted, activeDuplicate.Code, activeDuplicate.Body.String())
	assert.Contains(t, activeDuplicate.Body.String(), `"accepted":true`)
	assert.Equal(t, 1, authnAPIRowCount(t, db, "iam_principals", "email = ?", "registered@example.com"))
	assert.Equal(t, 1, authnAPIRowCount(t, db, "iam_notification_outbox", "recipient = ?", "registered@example.com"))
	assert.Zero(t, authnAPIRowCount(t, db, "iam_tenant_members", "principal_id IN (SELECT id FROM iam_principals WHERE email = ?)", "registered@example.com"))

	registration := api.OpenAPI().Paths["/registrations"].Post
	capabilityOperation := api.OpenAPI().Paths["/authentication-capabilities"].Get
	require.NotNil(t, registration)
	require.NotNil(t, capabilityOperation)
	assert.Equal(t, "authn-create-registration", registration.OperationID)
	assert.Equal(t, "authn-capabilities", capabilityOperation.OperationID)
	assert.Empty(t, registration.Security)
	assert.Contains(t, registration.Responses, "202")
	assert.Contains(t, registration.Responses, "422")

	_, disabled, _ := newAuthenticationService(t)
	_, disabledAPI := humatest.New(t)
	RegisterHTTP(disabledAPI, disabled, disabled)
	assert.Contains(t, disabledAPI.Get("/authentication-capabilities").Body.String(), `"registration":false`)
	disabledRequest := registrationRequestWithCaptcha(t, disabledAPI, "disabled@example.com", "correct registration password", "")
	assert.Equal(t, http.StatusServiceUnavailable, disabledAPI.Post("/registrations", disabledRequest,
		"Origin: https://app.example").Code)
}

func registrationRequestWithCaptcha(t *testing.T, api humatest.TestAPI, email, password, displayName string) map[string]any {
	t.Helper()
	for range 100 {
		response := api.Post("/captchas")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var envelope struct {
			Data struct {
				Type      string `json:"type"`
				CaptchaID string `json:"captcha_id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		if envelope.Data.Type != "text" {
			continue
		}
		answer := base64Captcha.DefaultMemStore.Get(envelope.Data.CaptchaID, false)
		require.NotEmpty(t, answer)
		return map[string]any{
			"email": email, "password": password, "display_name": displayName,
			"captcha_type": envelope.Data.Type, "captcha_id": envelope.Data.CaptchaID, "captcha_answer": answer,
		}
	}
	t.Fatal("text captcha was not issued after 100 attempts")
	return nil
}

func TestDisabledPasswordRecoveryHTTPFlow(t *testing.T) {
	_, web, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	start := api.Post("/password-recovery-requests", map[string]any{"identifier": "alice@example.com"}, "Origin: https://app.example")
	assert.Equal(t, http.StatusServiceUnavailable, start.Code, start.Body.String())
	complete := api.Post("/password-recoveries", map[string]any{
		"token": "cpr1_" + strings.Repeat("a", 43), "new_password": "new correct horse battery staple",
	}, "Origin: https://app.example")
	assert.Equal(t, http.StatusServiceUnavailable, complete.Code, complete.Body.String())
}

func TestAuthenticationMFAHTTPFlow(t *testing.T) {
	_, web, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, cookie)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/me/mfa").Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/me/mfa/totp-enrollments", "Cookie: "+cookie, "Origin: https://evil.example", map[string]any{
		"current_password": "correct horse battery staple",
	}).Code)

	enroll := api.Post("/me/mfa/totp-enrollments", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple",
	})
	require.Equal(t, http.StatusOK, enroll.Code, enroll.Body.String())
	var enrollmentEnvelope struct {
		Data authnext.MFAEnrollment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enroll.Body.Bytes(), &enrollmentEnvelope))
	assert.NotEmpty(t, enrollmentEnvelope.Data.ProvisioningURI)
	code, err := totp.GenerateCode(enrollmentEnvelope.Data.Secret, time.Now())
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/me/mfa/totp", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"code": differentTOTP(code)}).Code)
	confirm := api.Post("/me/mfa/totp", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"code": code})
	require.Equal(t, http.StatusOK, confirm.Code, confirm.Body.String())
	var confirmationEnvelope struct {
		Data authnext.MFAConfirmation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(confirm.Body.Bytes(), &confirmationEnvelope))
	require.Len(t, confirmationEnvelope.Data.RecoveryCodes, 10)

	challengeResponse := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, challengeResponse.Code, challengeResponse.Body.String())
	assert.Empty(t, challengeResponse.Header().Get("Set-Cookie"))
	var challengeEnvelope struct {
		Data authnext.LoginResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(challengeResponse.Body.Bytes(), &challengeEnvelope))
	assert.Equal(t, "mfa_required", challengeEnvelope.Data.Status)
	assert.Equal(t, []string{"totp", "recovery_code"}, challengeEnvelope.Data.Methods)

	verifyBody := map[string]any{"challenge_id": challengeEnvelope.Data.ChallengeID, "code": confirmationEnvelope.Data.RecoveryCodes[0]}
	assert.Equal(t, http.StatusForbidden, api.Post("/session-verifications", verifyBody, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusUnauthorized, api.Post("/session-verifications", map[string]any{"challenge_id": challengeEnvelope.Data.ChallengeID, "code": differentTOTP(code)}, "Origin: https://app.example").Code)
	verified := api.Post("/session-verifications", verifyBody, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, verified.Code, verified.Body.String())
	mfaCookie := strings.Split(verified.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, mfaCookie)

	status := api.Get("/me/mfa", "Cookie: "+mfaCookie)
	require.Equal(t, http.StatusOK, status.Code, status.Body.String())
	var statusEnvelope struct {
		Data authnext.MFAStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(status.Body.Bytes(), &statusEnvelope))
	assert.True(t, statusEnvelope.Data.TOTPEnabled)
	assert.Equal(t, 9, statusEnvelope.Data.RecoveryCodesRemaining)
	assert.Equal(t, http.StatusConflict, api.Post("/me/mfa/totp-enrollments", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple",
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/me/mfa/totp", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"code": code,
	}).Code)
	assert.Equal(t, http.StatusForbidden, api.Put("/me/mfa/recovery-codes", "Cookie: "+mfaCookie, "Origin: https://evil.example", map[string]any{
		"current_password": "correct horse battery staple", "code": confirmationEnvelope.Data.RecoveryCodes[1],
	}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Put("/me/mfa/recovery-codes", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple", "code": "invalid-recovery-code",
	}).Code)
	regenerated := api.Put("/me/mfa/recovery-codes", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple", "code": confirmationEnvelope.Data.RecoveryCodes[1],
	})
	require.Equal(t, http.StatusOK, regenerated.Code, regenerated.Body.String())
	var regeneratedEnvelope struct {
		Data authnext.MFAConfirmation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(regenerated.Body.Bytes(), &regeneratedEnvelope))
	require.Len(t, regeneratedEnvelope.Data.RecoveryCodes, 10)
	assert.Equal(t, http.StatusForbidden, api.Delete("/me/mfa/totp", "Cookie: "+mfaCookie, "Origin: https://evil.example", map[string]any{
		"current_password": "correct horse battery staple", "code": regeneratedEnvelope.Data.RecoveryCodes[0],
	}).Code)

	disabled := api.Delete("/me/mfa/totp", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple", "code": regeneratedEnvelope.Data.RecoveryCodes[0],
	})
	require.Equal(t, http.StatusOK, disabled.Code, disabled.Body.String())
	status = api.Get("/me/mfa", "Cookie: "+mfaCookie)
	assert.Equal(t, http.StatusOK, status.Code, status.Body.String())
	require.NoError(t, json.Unmarshal(status.Body.Bytes(), &statusEnvelope))
	assert.False(t, statusEnvelope.Data.TOTPEnabled)
	assert.Equal(t, http.StatusConflict, api.Delete("/me/mfa/totp", "Cookie: "+mfaCookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple", "code": regeneratedEnvelope.Data.RecoveryCodes[1],
	}).Code)

	for _, path := range []string{"/session-verifications", "/me/mfa", "/me/mfa/totp-enrollments", "/me/mfa/recovery-codes", "/me/mfa/totp"} {
		assert.Contains(t, api.OpenAPI().Paths, path)
	}
	for _, operation := range []*huma.Operation{
		api.OpenAPI().Paths["/me/mfa"].Get,
		api.OpenAPI().Paths["/me/mfa/totp-enrollments"].Post,
		api.OpenAPI().Paths["/me/mfa/totp"].Post,
		api.OpenAPI().Paths["/me/mfa/recovery-codes"].Put,
		api.OpenAPI().Paths["/me/mfa/totp"].Delete,
	} {
		assert.Contains(t, operation.Responses, "500")
	}
}

func TestMFAAuditFailureHTTPRollsBackEnrollment(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	_, api := humatest.New(t)
	RegisterHTTP(api, service, service)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example.test")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	harness.auditErr = errors.New("audit denied")

	enrollment := api.Post("/me/mfa/totp-enrollments",
		map[string]any{"current_password": "correct horse battery staple"},
		"Cookie: "+cookie, "Origin: https://app.example.test")
	assert.Equal(t, http.StatusInternalServerError, enrollment.Code, enrollment.Body.String())

	var count int64
	require.NoError(t, harness.db.Table("iam_mfa_enrollments").
		Where("principal_id = ?", principalID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAuthenticationPasskeyHTTPContract(t *testing.T) {
	_, web, principalID := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)
	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	bobID := seedLocalPrincipal(t, web.repo, "bob", "bob@example.com", "correct horse battery staple")
	bobLogin, err := web.BeginLogin(t.Context(), "bob", "correct horse battery staple", "")
	require.NoError(t, err)
	bobPasskey, err := web.storePasskeyCredential(t.Context(), bobID, "Bob's key",
		passkeyCredential([]byte("http-bob-passkey"), 1, false), time.Now().UTC())
	require.NoError(t, err)

	list := api.Get("/me/passkeys", "Cookie: "+cookie)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `"data":[]`)
	assert.NotContains(t, list.Body.String(), bobPasskey.ID)
	assert.Equal(t, http.StatusNotFound, api.Patch("/me/passkeys/"+bobPasskey.ID,
		"Cookie: "+cookie, "Origin: https://app.example", map[string]any{"name": "Stolen"}).Code)
	assert.Equal(t, http.StatusNotFound, api.Delete("/me/passkeys/"+bobPasskey.ID,
		"Cookie: "+cookie, "Origin: https://app.example",
		map[string]any{"current_password": "correct horse battery staple"}).Code)
	storedBob, err := web.repo.getPasskey(t.Context(), bobPasskey.ID)
	require.NoError(t, err)
	assert.Equal(t, "Bob's key", storedBob.Name)
	_, err = web.Authenticate(t.Context(), "", web.SessionCookie(bobLogin.SessionID))
	require.NoError(t, err, "another principal's session must remain active")
	assert.Equal(t, http.StatusForbidden, api.Post("/me/passkey-registration-challenges", "Cookie: "+cookie, "Origin: https://evil.example", map[string]any{
		"current_password": "correct horse battery staple",
	}).Code)
	registration := api.Post("/me/passkey-registration-challenges", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{
		"current_password": "correct horse battery staple",
	})
	require.Equal(t, http.StatusOK, registration.Code, registration.Body.String())
	var options struct {
		Data authnext.PasskeyOptions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(registration.Body.Bytes(), &options))
	assert.NotEmpty(t, options.Data.ChallengeID)
	assert.Contains(t, string(options.Data.Options), `"residentKey":"required"`)
	invalidRegistration := map[string]any{"challenge_id": options.Data.ChallengeID, "name": "Laptop", "credential": map[string]any{"invalid": true}}
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/me/passkeys", "Cookie: "+cookie, "Origin: https://app.example", invalidRegistration).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/me/passkeys", "Cookie: "+cookie, "Origin: https://app.example", invalidRegistration).Code)

	assert.Equal(t, http.StatusForbidden, api.Post("/passkey-session-challenges", map[string]any{"return_url": "https://app.example/"}, "Origin: https://evil.example").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/passkey-session-challenges", map[string]any{"return_url": "https://evil.example/"}, "Origin: https://app.example").Code)
	loginOptions := api.Post("/passkey-session-challenges", map[string]any{"return_url": "https://app.example/"}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, loginOptions.Code, loginOptions.Body.String())
	require.NoError(t, json.Unmarshal(loginOptions.Body.Bytes(), &options))
	invalidLogin := map[string]any{"challenge_id": options.Data.ChallengeID, "credential": map[string]any{"invalid": true}}
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/passkey-session-verifications", invalidLogin, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusUnprocessableEntity, api.Post("/passkey-session-verifications", invalidLogin, "Origin: https://app.example").Code)

	missingID := strings.Repeat("a", 64)
	assert.Equal(t, http.StatusNotFound, api.Patch("/me/passkeys/"+missingID, "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"name": "Missing"}).Code)
	assert.Equal(t, http.StatusUnauthorized, api.Delete("/me/passkeys/"+missingID, "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"current_password": "wrong"}).Code)
	assert.Equal(t, http.StatusNotFound, api.Delete("/me/passkeys/"+missingID, "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"current_password": "correct horse battery staple"}).Code)

	stored, err := web.storePasskeyCredential(t.Context(), parseGUID(principalID), "Laptop",
		passkeyCredential([]byte("http-passkey"), 1, false), time.Now().UTC())
	require.NoError(t, err)
	renamed := api.Patch("/me/passkeys/"+stored.ID, "Cookie: "+cookie, "Origin: https://app.example",
		map[string]any{"name": "Security key"})
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())
	assert.Contains(t, renamed.Body.String(), `"name":"Security key"`)
	deleted := api.Delete("/me/passkeys/"+stored.ID, "Cookie: "+cookie, "Origin: https://app.example",
		map[string]any{"current_password": "correct horse battery staple"})
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	assert.Contains(t, deleted.Body.String(), `"deleted":true`)

	for _, path := range []string{"/me/passkeys", "/me/passkey-registration-challenges", "/passkey-session-challenges", "/passkey-session-verifications", "/me/passkeys/{passkey_id}"} {
		assert.Contains(t, api.OpenAPI().Paths, path)
	}
	assert.Equal(t, "authn-create-passkey-session-verification", api.OpenAPI().Paths["/passkey-session-verifications"].Post.OperationID)
	assert.Contains(t, api.OpenAPI().Paths["/passkey-session-verifications"].Post.Responses, "401")
	for _, operation := range []*huma.Operation{
		api.OpenAPI().Paths["/passkey-session-challenges"].Post,
		api.OpenAPI().Paths["/passkey-session-verifications"].Post,
		api.OpenAPI().Paths["/me/passkeys"].Get,
		api.OpenAPI().Paths["/me/passkey-registration-challenges"].Post,
		api.OpenAPI().Paths["/me/passkeys"].Post,
		api.OpenAPI().Paths["/me/passkeys/{passkey_id}"].Patch,
		api.OpenAPI().Paths["/me/passkeys/{passkey_id}"].Delete,
	} {
		assert.Contains(t, operation.Responses, "500")
	}
}

func TestAuthenticationStepUpHTTPContract(t *testing.T) {
	_, web, _ := newAuthenticationService(t)
	_, api := humatest.New(t)
	RegisterHTTP(api, web, web)

	login := api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple", "return_url": "https://app.example/",
	}, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	cookie := strings.Split(login.Header().Get("Set-Cookie"), ";")[0]

	session := api.Get("/sessions/current", "Cookie: "+cookie)
	require.Equal(t, http.StatusOK, session.Code, session.Body.String())
	assert.Contains(t, session.Body.String(), `"acr":1`)
	assert.Contains(t, session.Body.String(), `"amr":["pwd"]`)

	enroll := api.Post("/me/mfa/totp-enrollments", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"current_password": "correct horse battery staple"})
	require.Equal(t, http.StatusOK, enroll.Code, enroll.Body.String())
	var enrollment struct {
		Data authnext.MFAEnrollment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enroll.Body.Bytes(), &enrollment))
	confirm := api.Post("/me/mfa/totp", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{"code": mustTOTPValue(t, enrollment.Data.Secret)})
	require.Equal(t, http.StatusOK, confirm.Code, confirm.Body.String())
	var confirmation struct {
		Data authnext.MFAConfirmation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(confirm.Body.Bytes(), &confirmation))

	assert.Equal(t, http.StatusForbidden, api.Post("/step-up-challenges", "Cookie: "+cookie, "Origin: https://evil.example").Code)
	options := api.Post("/step-up-challenges", "Cookie: "+cookie, "Origin: https://app.example")
	require.Equal(t, http.StatusOK, options.Code, options.Body.String())
	var stepUp struct {
		Data authnext.StepUpOptions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(options.Body.Bytes(), &stepUp))
	assert.NotEmpty(t, stepUp.Data.ChallengeID)
	assert.Contains(t, stepUp.Data.Methods, "totp")

	wrong := api.Post("/step-up-verifications", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{
		"challenge_id": stepUp.Data.ChallengeID, "code": "000000",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, wrong.Code, wrong.Body.String())

	verify := api.Post("/step-up-verifications", "Cookie: "+cookie, "Origin: https://app.example", map[string]any{
		"challenge_id": stepUp.Data.ChallengeID, "code": confirmation.Data.RecoveryCodes[0],
	})
	require.Equal(t, http.StatusOK, verify.Code, verify.Body.String())
	upgradedCookie := strings.Split(verify.Header().Get("Set-Cookie"), ";")[0]
	require.NotEmpty(t, upgradedCookie)

	session = api.Get("/sessions/current", "Cookie: "+upgradedCookie)
	require.Equal(t, http.StatusOK, session.Code, session.Body.String())
	assert.Contains(t, session.Body.String(), `"acr":2`)
	assert.Contains(t, session.Body.String(), `"mfa"`)
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", "Cookie: "+cookie).Code)

	assert.Contains(t, api.OpenAPI().Paths, "/step-up-challenges")
	assert.Contains(t, api.OpenAPI().Paths, "/step-up-verifications")
	assert.Equal(t, "authn-create-step-up-challenge", api.OpenAPI().Paths["/step-up-challenges"].Post.OperationID)
	assert.Equal(t, "authn-create-step-up-verification", api.OpenAPI().Paths["/step-up-verifications"].Post.OperationID)
	assert.Contains(t, api.OpenAPI().Paths["/step-up-verifications"].Post.Responses, "500")
}

func mustTOTPValue(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	return code
}

func differentTOTP(code string) string {
	if code[0] == '0' {
		return "1" + code[1:]
	}
	return "0" + code[1:]
}

func TestAuthenticationHTTPUnavailableAndDisabledWeb(t *testing.T) {
	db, web, principalID := newAuthenticationService(t)
	accessToken, _, err := web.IssueAccessToken(t.Context(), parseGUID(principalID), "api", "openid")
	require.NoError(t, err)
	_, api := humatest.New(t)
	RegisterHTTP(api, fixedTokenVerifier{claims: &authnext.Claims{
		PrincipalID: parseGUID(principalID), Subject: principalID,
	}}, web)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.Equal(t, http.StatusInternalServerError, api.Post("/sessions", map[string]any{
		"login_name": "alice", "password": "correct horse battery staple",
	}, "Origin: https://app.example").Code)
	assert.Equal(t, http.StatusInternalServerError, api.Post("/passkey-session-challenges", map[string]any{
		"return_url": "https://app.example/",
	}, "Origin: https://app.example").Code)
	authorization := "Authorization: Bearer " + accessToken
	assert.Equal(t, http.StatusUnauthorized, api.Get("/sessions/current", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Get("/sessions", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Get("/me/mfa", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Get("/me/passkeys", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Patch("/me/password", authorization, map[string]any{
		"current_password": "correct horse battery staple", "new_password": "another correct horse battery staple",
	}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, api.Post("/email-verification-requests", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Post("/me/mfa/totp-enrollments", authorization,
		map[string]any{"current_password": "correct horse battery staple"}).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Post("/step-up-challenges", authorization).Code)
	assert.Equal(t, http.StatusInternalServerError, api.Post("/me/passkey-registration-challenges", authorization,
		map[string]any{"current_password": "correct horse battery staple"}).Code)

	_, declaration := humatest.New(t)
	RegisterHTTP(declaration, web, nil)
	assert.Contains(t, declaration.OpenAPI().Paths, "/me")
	assert.NotContains(t, declaration.OpenAPI().Paths, "/sessions")
}

func TestAuthenticationHTTPHandlersEnforceCSRFDefenseInDepth(t *testing.T) {
	_, web, principalID := newAuthenticationService(t)
	login, err := web.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	_, api := humatest.New(t)
	RegisterHTTP(api, fixedTokenVerifier{claims: &authnext.Claims{
		PrincipalID: parseGUID(principalID), Subject: principalID,
	}}, web)
	cookie := "Cookie: " + strings.Split(web.SessionCookie(login.SessionID), ";")[0]
	evilOrigin := "Origin: https://evil.example"
	id := strings.Repeat("a", 64)

	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions/current", cookie, evilOrigin).Code)
	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions/"+id, cookie, evilOrigin).Code)
	assert.Equal(t, http.StatusForbidden, api.Delete("/sessions", cookie, evilOrigin).Code)
	assert.Equal(t, http.StatusForbidden, api.Patch("/me/password", cookie, evilOrigin, map[string]any{
		"current_password": "correct horse battery staple", "new_password": "another correct horse battery staple",
	}).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/email-verification-requests", cookie, evilOrigin).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/me/mfa/totp-enrollments", cookie, evilOrigin,
		map[string]any{"current_password": "correct horse battery staple"}).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/me/mfa/totp", cookie, evilOrigin,
		map[string]any{"code": "123456"}).Code)
	verification := map[string]any{"current_password": "correct horse battery staple", "code": "123456"}
	assert.Equal(t, http.StatusForbidden, api.Put("/me/mfa/recovery-codes", cookie, evilOrigin, verification).Code)
	assert.Equal(t, http.StatusForbidden, api.Delete("/me/mfa/totp", cookie, evilOrigin, verification).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/step-up-challenges", cookie, evilOrigin).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/step-up-verifications", cookie, evilOrigin,
		map[string]any{"challenge_id": strings.Repeat("c", 32), "code": "123456"}).Code)
	assert.Equal(t, http.StatusForbidden, api.Post("/me/passkey-registration-challenges", cookie, evilOrigin,
		map[string]any{"current_password": "correct horse battery staple"}).Code)
	assert.Equal(t, http.StatusForbidden, api.Patch("/me/passkeys/"+id, cookie, evilOrigin,
		map[string]any{"name": "Security key"}).Code)
	assert.Equal(t, http.StatusForbidden, api.Delete("/me/passkeys/"+id, cookie, evilOrigin,
		map[string]any{"current_password": "correct horse battery staple"}).Code)
}

func newAuthenticationService(t *testing.T) (*gorm.DB, *Service, string) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	service, err := NewWebService(authnext.Config{
		Enabled: true, Issuer: "https://iam.example", Audience: []string{"api"},
		SigningKey: base64.RawStdEncoding.EncodeToString(seed), AccessTokenTTL: time.Hour,
		MFA:     authnext.MFAConfig{EncryptionKey: base64.RawStdEncoding.EncodeToString(seed)},
		Passkey: authnext.PasskeyConfig{Enabled: true, RPID: "app.example", DisplayName: "SevenLink", Origins: []string{"https://app.example"}},
		Web: authnext.WebConfig{
			Enabled: true, CookieName: "custom_session", SessionTTL: time.Hour, IdleTTL: 10 * time.Minute,
			PostLoginURL: "https://app.example/", PostLogoutURL: "https://app.example/login",
			AllowedReturnURLs: []string{"https://app.example/"}, AllowedOrigins: []string{"https://app.example"},
		},
	}, harness.repository, WithMemberTenantResolver(boundMemberTenantResolver(t, harness.runtime)))
	require.NoError(t, err)
	principalID := seedLocalPrincipal(t, harness.repository, "alice", "alice@example.com", "correct horse battery staple")
	return harness.db, service, guidString(principalID)
}

func newRecoveryAuthenticationService(t *testing.T, notificationURL string, registration ...bool) (*gorm.DB, *Service, string) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	registrationEnabled := len(registration) > 0 && registration[0]
	options := []WebOption{WithMemberTenantResolver(boundMemberTenantResolver(t, harness.runtime))}
	if registrationEnabled {
		options = append(options, WithRegistrationPrincipalCreator(registrationCreatorForHarness(harness)))
	}
	service, err := NewWebService(authnext.Config{
		Enabled: true, Issuer: "https://iam.example", Audience: []string{"api"},
		SigningKey: base64.RawStdEncoding.EncodeToString(seed), AccessTokenTTL: time.Hour,
		MFA:               authnext.MFAConfig{EncryptionKey: base64.RawStdEncoding.EncodeToString(seed)},
		Recovery:          authnext.RecoveryConfig{Enabled: true, TokenTTL: 15 * time.Minute, Cooldown: 24 * time.Hour, ResetURL: "https://app.example/recover"},
		EmailVerification: authnext.EmailVerificationConfig{Enabled: true, TokenTTL: time.Hour, VerifyURL: "https://app.example/verify-email"},
		Registration:      authnext.RegistrationConfig{Enabled: registrationEnabled},
		Notification:      authnext.NotificationConfig{URL: notificationURL, PollInterval: 10 * time.Millisecond, RequestTimeout: time.Second, MaxAttempts: 3},
		Web: authnext.WebConfig{
			Enabled: true, CookieName: "custom_session", SessionTTL: time.Hour, IdleTTL: 10 * time.Minute,
			PostLoginURL: "https://app.example/", PostLogoutURL: "https://app.example/login",
			AllowedReturnURLs: []string{"https://app.example/"}, AllowedOrigins: []string{"https://app.example"},
		},
	}, harness.repository, options...)
	require.NoError(t, err)
	principalID := seedLocalPrincipal(t, harness.repository, "alice", "alice@example.com", "correct horse battery staple")
	return harness.db, service, guidString(principalID)
}

func authnAPIRowCount(t *testing.T, db *gorm.DB, table, condition string, args ...any) int {
	t.Helper()
	var count int64
	err := db.WithContext(t.Context()).Table(table).Where(condition, args...).Count(&count).Error
	require.NoError(t, err)
	return int(count)
}
