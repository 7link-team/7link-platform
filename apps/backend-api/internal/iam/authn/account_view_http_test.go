package authn

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountViewHTTPControlsAndSecurityBoundaries(t *testing.T) {
	for _, mode := range []string{"read_only", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			s, h := newAccountViewService(t, "controls_")
			_, api := humatest.New(t)
			RegisterHTTP(api, s, s)
			cookie := "Cookie: session=browser-101"
			origin := "Origin: https://app.example.test"
			body := `{"principal_id":"202","scope_type":"personal","scope_id":"202","mode":"` + mode + `"}`
			csrf := api.Post("/account-views", cookie, strings.NewReader(body))
			require.Equal(t, http.StatusForbidden, csrf.Code, csrf.Body.String())
			created := api.Post("/account-views", cookie, origin, strings.NewReader(body))
			require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
			var response struct {
				Data AccountViewCreated `json:"data"`
			}
			require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
			require.NotEmpty(t, response.Data.Token)
			authorization := "Authorization: Bearer " + response.Data.Token
			var before, after []model.Session
			require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&before).Error)
			me := api.Get("/me", authorization, cookie)
			require.Equal(t, http.StatusOK, me.Code, me.Body.String())
			assert.Contains(t, me.Body.String(), `"subject":"202"`)
			assert.NotContains(t, me.Body.String(), response.Data.Token)
			mfa := api.Get("/principals/202/mfa", authorization, cookie)
			require.Equal(t, http.StatusOK, mfa.Code, mfa.Body.String())
			for _, route := range []struct{ method, path string }{
				{http.MethodDelete, "/sessions/current"},
				{http.MethodDelete, "/sessions/" + tokenHash("browser-202")},
				{http.MethodDelete, "/principals/202/sessions"},
				{http.MethodPatch, "/principals/202/password"},
				{http.MethodPost, "/principals/202/mfa/totp-enrollments"},
				{http.MethodPost, "/principals/202/mfa/totp"},
				{http.MethodPut, "/principals/202/mfa/recovery-codes"},
				{http.MethodDelete, "/principals/202/mfa/totp"},
				{http.MethodPost, "/sessions/current/step-up-challenges"},
				{http.MethodPost, "/sessions/current/step-up-verifications"},
				{http.MethodPost, "/principals/202/passkey-registration-challenges"},
				{http.MethodPost, "/principals/202/passkeys"},
				{http.MethodPatch, "/principals/202/passkeys/key"},
				{http.MethodDelete, "/principals/202/passkeys/key"},
				{http.MethodPost, "/principals/202/reauthentication/challenges"},
				{http.MethodPost, "/principals/202/reauthentication/verifications"},
				{http.MethodPost, "/principals/202/personal-token-preparations"},
				{http.MethodPost, "/principals/202/personal-tokens"},
				{http.MethodPost, "/email-verifications/requests"},
				{http.MethodPost, "/email-verifications"},
				{http.MethodPost, "/login"},
				{http.MethodPost, "/login/mfa"},
				{http.MethodPost, "/login/passkey/options"},
				{http.MethodPost, "/login/passkey/verify"},
				{http.MethodPost, "/account-views"},
				{http.MethodDelete, "/account-views/" + response.Data.ID.String()},
			} {
				denied := api.Do(route.method, route.path, authorization, cookie, origin, strings.NewReader(`{}`))
				require.Equal(t, http.StatusForbidden, denied.Code, "%s %s: %s", route.method, route.path, denied.Body.String())
			}
			require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&after).Error)
			assert.Equal(t, before, after)
			assert.Error(t, s.RevokeSession(t.Context(), "Bearer "+response.Data.Token, "session=browser-101", tokenHash("browser-202")))
			assert.Error(t, s.LogoutAll(t.Context(), "Bearer "+response.Data.Token, "session=browser-101"))
			assert.Error(t, s.ChangePassword(t.Context(), "Bearer "+response.Data.Token, "session=browser-101", "old-password", "new-long-password"))
			verified, err := s.Authenticate(t.Context(), "Bearer "+response.Data.Token, "")
			require.NoError(t, err)
			viewContext := authnext.WithClaims(t.Context(), verified)
			_, err = s.CreateSession(viewContext, 202, repositoryTestNow, authnext.Assurance{Level: 1})
			assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			_, _, err = s.IssueAccessToken(viewContext, 202, "", "openid")
			assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			_, err = s.IssueIDToken(viewContext, 202, "client", "nonce")
			assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			_, err = s.Logout(viewContext, "session=browser-101")
			assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			wrongOwner := api.Delete("/account-views/"+response.Data.ID.String(), "Cookie: session=browser-202", origin)
			require.Equal(t, http.StatusUnauthorized, wrongOwner.Code, wrongOwner.Body.String())
			csrfRevoke := api.Delete("/account-views/"+response.Data.ID.String(), cookie)
			require.Equal(t, http.StatusForbidden, csrfRevoke.Code, csrfRevoke.Body.String())
			closed := api.Delete("/account-views/"+response.Data.ID.String(), cookie, origin)
			require.Equal(t, http.StatusOK, closed.Code, closed.Body.String())
			revoked := api.Get("/me", authorization, cookie)
			require.Equal(t, http.StatusUnauthorized, revoked.Code, revoked.Body.String())
		})
	}
}

func TestAccountViewHTTPFailsClosedWithoutAuthorityAndOnAuditFailure(t *testing.T) {
	s, h := newAccountViewService(t, "httpfail_")
	_, api := humatest.New(t)
	RegisterHTTP(api, s, s)
	body := `{"principal_id":"202","scope_type":"personal","scope_id":"202"}`
	cookie := "Cookie: session=browser-101"
	origin := "Origin: https://app.example.test"
	ordinaryBearer := api.Post("/account-views", "Authorization: Bearer ordinary-token", cookie, origin, strings.NewReader(body))
	require.Equal(t, http.StatusUnauthorized, ordinaryBearer.Code, ordinaryBearer.Body.String())
	authorizer := s.accountViewAuthorizer
	s.accountViewAuthorizer = nil
	unavailable := api.Post("/account-views", cookie, origin, strings.NewReader(body))
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code, unavailable.Body.String())
	s.accountViewAuthorizer = authorizer
	h.auditErr = assert.AnError
	failedAudit := api.Post("/account-views", cookie, origin, strings.NewReader(body))
	require.Equal(t, http.StatusServiceUnavailable, failedAudit.Code, failedAudit.Body.String())
	assert.NotContains(t, failedAudit.Body.String(), `"token"`)
	var count int64
	require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Count(&count).Error)
	assert.Zero(t, count)
}
