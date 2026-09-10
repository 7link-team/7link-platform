package identity

import (
	"errors"
	"net/http"
	"testing"

	"7link.com/platform/internal/iam/core"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	humax "7link.com/platform/pkg/extension/huma"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestClosureHTTPPersonalAndPlatformBoundaries(t *testing.T) {
	h, p := closureHarness(t)
	configureClosureProcessing(h)
	require.NoError(t, initializeIdentityLocales())
	h.service.privacyBrowser.ValidateCSRF = func(_, origin, _ string) error {
		if origin != "https://account.example.test" {
			return errors.New("CSRF")
		}
		return nil
	}
	api := humax.New(chi.NewRouter(), humax.DefaultConfig("closure-test", "1"))
	registrar := authz.NewRegistrar(authz.DefaultRegistry(), identityTokenVerifier{principalID: p.ID}, identityRouteAuthorizer{platformAllowed: true}, identityMembershipChecker{})
	RegisterHTTP(api, h.service, registrar)
	httpAPI := humatest.Wrap(t, api)
	path := "/iam/me/closure-request"
	cookie, origin := "Cookie: browser", "Origin: https://account.example.test"
	require.Equal(t, http.StatusOK, httpAPI.Get(path, cookie).Code)
	require.Equal(t, http.StatusUnauthorized, httpAPI.Get(path).Code)
	require.Equal(t, http.StatusUnauthorized, httpAPI.Get(path, "Authorization: Bearer test", cookie).Code)
	require.Equal(t, http.StatusForbidden, httpAPI.Post(path, cookie, map[string]any{"proof": "verified", "version": 0}).Code)
	require.Equal(t, http.StatusForbidden, httpAPI.Post(path, cookie, origin, map[string]any{"proof": "wrong", "version": 0}).Code)
	require.Equal(t, http.StatusUnprocessableEntity, httpAPI.Post(path, cookie, origin, map[string]any{"proof": "verified", "version": 0, "principal_id": "999"}).Code)
	created := httpAPI.Post(path, cookie, origin, map[string]any{"proof": "verified", "version": 0})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), `"status":"pending"`)
	require.Equal(t, http.StatusConflict, httpAPI.Post(path, cookie, origin, map[string]any{"proof": "verified", "version": 0}).Code)
	require.Equal(t, http.StatusOK, httpAPI.Get("/platform/iam/closure-requests?status=pending&limit=1", cookie).Code)
	base := "/platform/iam/principals/" + p.ID.String() + "/closure-request/"
	require.Equal(t, http.StatusConflict, httpAPI.Post(base+"processing", cookie, origin, map[string]any{"version": 99}).Code)
	rejected := httpAPI.Post(base+"rejection", cookie, origin, map[string]any{"version": 1})
	require.Equal(t, http.StatusOK, rejected.Code, rejected.Body.String())
	require.Equal(t, http.StatusOK, httpAPI.Post(path, cookie, origin, map[string]any{"proof": "verified", "version": 2}).Code)
	require.Equal(t, http.StatusOK, httpAPI.Delete(path, cookie, origin, map[string]any{"version": 3}).Code)
	require.Equal(t, http.StatusOK, httpAPI.Post(path, cookie, origin, map[string]any{"proof": "verified", "version": 4}).Code)
	processed := httpAPI.Post(base+"processing", cookie, origin, map[string]any{"version": 5})
	require.Equal(t, http.StatusOK, processed.Code, processed.Body.String())
	require.Contains(t, processed.Body.String(), `"status":"processing"`)
	for _, operation := range []*huma.Operation{api.OpenAPI().Paths[path].Get, api.OpenAPI().Paths[path].Post, api.OpenAPI().Paths[path].Delete} {
		require.Len(t, operation.Security, 1)
		require.Contains(t, operation.Security[0], authz.SessionScheme)
		for _, parameter := range operation.Parameters {
			require.NotEqual(t, "Authorization", parameter.Name)
		}
	}
}

func TestClosureHTTPPlatformPermissionAndUnavailableProof(t *testing.T) {
	h := newIdentityHTTPHarness(t, false)
	require.Equal(t, http.StatusForbidden, h.api.Get("/platform/iam/closure-requests", "Cookie: session=browser").Code)
	require.Equal(t, http.StatusForbidden, h.api.Post("/platform/iam/principals/11/closure-request/processing", "Cookie: session=browser", map[string]any{"version": 1}).Code)
	require.Equal(t, http.StatusForbidden, h.api.Post("/platform/iam/principals/11/closure-request/rejection", "Cookie: session=browser", map[string]any{"version": 1}).Code)
	for _, test := range []struct {
		err    error
		status int
	}{
		{ErrClosureConflict, 409}, {ErrUnavailable, 503}, {ErrInvalid, 422},
		{authnext.ErrUnavailable, 503}, {authnext.ErrInvalidCredentials, 403}, {core.ErrPrincipalHasPlatformAccess, 409},
	} {
		var response huma.StatusError
		require.ErrorAs(t, closureError(test.err), &response)
		require.Equal(t, test.status, response.GetStatus())
	}
}

func TestClosureHTTPUnavailableAndStorageFailures(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	path, cookie := "/iam/me/closure-request", "Cookie: session=browser"
	proof := map[string]any{"proof": "verified", "version": 0}
	require.Equal(t, http.StatusUnauthorized, h.api.Post(path, proof).Code)
	require.Equal(t, http.StatusServiceUnavailable, h.api.Post(path, cookie, proof).Code)
	h.service.privacyBrowser.ValidateCSRF = func(string, string, string) error { return nil }
	require.Equal(t, http.StatusServiceUnavailable, h.api.Post(path, cookie, proof).Code)
	require.Equal(t, http.StatusNotFound, h.api.Get(path, cookie).Code)
	require.Equal(t, http.StatusNotFound, h.api.Delete(path, cookie, map[string]any{"version": 1}).Code)
	require.Equal(t, http.StatusNotFound, h.api.Post("/platform/iam/principals/999/closure-request/rejection", cookie, map[string]any{"version": 1}).Code)
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_closure_requests").Error)
	require.Equal(t, http.StatusInternalServerError, h.api.Get("/platform/iam/closure-requests", cookie).Code)
}
