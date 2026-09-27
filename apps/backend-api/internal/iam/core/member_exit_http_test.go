package core

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemberExitHTTPUsesCookieAndExplicitTenant(t *testing.T) {
	h := newHTTPIAMHarness(t, false)
	s := h.module.service
	s.privacyBrowser.ValidateCSRF = func(_, origin, _ string) error {
		if origin != "https://account.example.test" {
			return errors.New("CSRF")
		}
		return nil
	}
	s.reauthenticate = func(_ context.Context, _ *gorm.DB, id guid.ID, cookie, proof, purpose, target string) error {
		if id != 11 || cookie != "session=browser" || proof != "verified" || purpose != "tenant_leave" || target != "1" {
			return authnext.ErrInvalidCredentials
		}
		return nil
	}
	s.revokePersonalTokens = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	s.revokeOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	cookie, origin := "Cookie: session=browser", "Origin: https://account.example.test"
	page := h.api.Get("/principals/"+"11"+"/memberships?limit=1", cookie)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	require.Contains(t, page.Body.String(), `"tenant_id":"1"`)
	path := "/principals/" + "11" + "/memberships/1"
	for _, operation := range []*huma.Operation{h.api.OpenAPI().Paths["/principals/{principal_id}/memberships"].Get, h.api.OpenAPI().Paths["/principals/{principal_id}/memberships/{tenant_id}"].Delete} {
		for _, parameter := range operation.Parameters {
			require.NotEqual(t, "Authorization", parameter.Name)
		}
	}
	require.Equal(t, http.StatusUnauthorized, h.api.Get("/principals/"+"11"+"/memberships", cookie, "Authorization: Bearer test").Code)
	require.Equal(t, http.StatusUnauthorized, h.api.Delete(path, cookie, origin, "Authorization: Bearer test", map[string]any{"proof": "verified"}).Code)
	require.Equal(t, http.StatusForbidden, h.api.Delete(path, cookie, map[string]any{"proof": "verified"}).Code)
	require.Equal(t, http.StatusForbidden, h.api.Delete(path, cookie, origin, map[string]any{"proof": "wrong"}).Code)
	left := h.api.Delete(path, cookie, origin, map[string]any{"proof": "verified"})
	require.Equal(t, http.StatusOK, left.Code, left.Body.String())
	require.Empty(t, left.Header().Get("Set-Cookie"), "tenant exit must preserve the global cookie")
	page = h.api.Get("/principals/"+"11"+"/memberships", cookie)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	require.Contains(t, page.Body.String(), `"items":[]`)
}

func TestPrincipalPlatformAccessGuardRejectsRetainedPlatformAuthority(t *testing.T) {
	h := newCoreServiceHarness(t)
	seedSystemAccessFixture(t, h.db, "sl_")
	h.seedMember(t, 1, 11, MemberActive)
	require.NoError(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 11))
	_, err := h.repository.SetPlatformAdministrator(t.Context(), 11, true, nil)
	require.NoError(t, err)
	require.ErrorIs(t, requireNoPrincipalPlatformAccess(t.Context(), h.db, "sl_", 11), ErrPrincipalHasPlatformAccess)
}

func TestMemberExitHTTPMissingCapabilitiesAndFailures(t *testing.T) {
	h := newHTTPIAMHarness(t, false)
	s := h.module.service
	path, cookie := "/principals/"+"11"+"/memberships/1", "Cookie: session=browser"
	body := map[string]any{"proof": "verified"}
	require.Equal(t, http.StatusUnauthorized, h.api.Get("/principals/"+"11"+"/memberships").Code)
	require.Equal(t, http.StatusUnauthorized, h.api.Delete(path, body).Code)
	require.Equal(t, http.StatusServiceUnavailable, h.api.Delete(path, cookie, body).Code)
	s.privacyBrowser.ValidateCSRF = func(string, string, string) error { return nil }
	require.Equal(t, http.StatusUnprocessableEntity, h.api.Delete("/principals/"+"11"+"/memberships/9999999999999999999", cookie, body).Code)
	require.Equal(t, http.StatusServiceUnavailable, h.api.Delete(path, cookie, body).Code)
	_, err := s.memberBrowser(t.Context(), "browser", "")
	require.Error(t, err, "a cookie header alone cannot manufacture principal claims")
	_, _, err = s.MyMemberships(t.Context(), 0, 0, 50)
	require.ErrorIs(t, err, ErrInvalidArgument)
	s.reauthenticate = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	s.revokePersonalTokens = func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
		return errors.New("token storage unavailable")
	}
	s.revokeOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
	require.Equal(t, http.StatusInternalServerError, h.api.Delete(path, cookie, body).Code)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_tenant_members").Error)
	require.Equal(t, http.StatusInternalServerError, h.api.Get("/principals/"+"11"+"/memberships", cookie).Code)
}
