package iamintegration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComposedTenantDeactivationPreservesOtherTenantAndWithdrawsOldGrants(t *testing.T) {
	for _, prefix := range []string{"", "isolation_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			f := newIAMTenantFixtureWithDatabase(t, app.Database{Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "isolation.sqlite"), Writable: true, Readable: true}, prefix)
			principal := f.requesterB
			response := f.request(t, http.MethodPost, "/members", iamTenantA, f.tokenA, map[string]any{"subject": principal.ID.String(), "display_name": "Shared member", "email": principal.Email, "status": "active"}, nil)
			requireStatus(t, http.StatusCreated, response)
			cookie := f.closureCookie(t, principal)
			patA := f.closurePAT(t, principal, cookie, iamTenantA)
			patB := f.closurePAT(t, principal, cookie, iamTenantB)
			clientA, secretA, tokensA := f.closureOAuth(t, cookie, iamTenantA)
			clientB, secretB, tokensB := f.closureOAuth(t, cookie, iamTenantB)
			browserHeaders := map[string]string{"Cookie": cookie}
			for _, tenant := range []struct{ id, token string }{{iamTenantA.String(), tokensA.AccessToken}, {iamTenantB.String(), tokensB.AccessToken}} {
				response = f.request(t, http.MethodGet, "/tenants/"+tenant.id+"/entities", 0, "", nil, browserHeaders)
				requireStatus(t, http.StatusOK, response)
				response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, tenant.token, nil, nil)
				requireStatus(t, http.StatusOK, response)
			}
			identityService := requireIAMModule[*identity.Module](t, f, identity.Name).Service
			input := identity.ProvisionedPrincipalInput{LoginName: principal.LoginName, DisplayName: "Shared member", Email: principal.Email, Active: false}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				_, err := identityService.ReplaceProvisionedTo(t.Context(), tx, iamTenantA, principal.ID, input)
				return err
			})
			require.NoError(t, err)
			checkWithdrawn := func() {
				t.Helper()
				response = f.request(t, http.MethodGet, "/entities", iamTenantA, patA.Token, nil, nil)
				requireStatus(t, http.StatusUnauthorized, response)
				response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, tokensA.AccessToken, nil, nil)
				requireStatus(t, http.StatusUnauthorized, response)
				response = f.request(t, http.MethodPost, "/oauth/token", 0, "", url.Values{"grant_type": {"refresh_token"}, "client_id": {clientA.ID}, "client_secret": {secretA}, "refresh_token": {tokensA.RefreshToken}}, nil)
				requireStatus(t, http.StatusBadRequest, response)
				assert.Contains(t, string(response.Body), "invalid_grant")
			}
			response = f.request(t, http.MethodGet, "/entities", iamTenantA, "", nil, browserHeaders)
			requireStatus(t, http.StatusForbidden, response)
			checkWithdrawn()
			response = f.request(t, http.MethodGet, "/entities", iamTenantB, "", nil, browserHeaders)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodGet, "/entities", iamTenantB, patB.Token, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, tokensB.AccessToken, nil, nil)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodPost, "/oauth/token", 0, "", url.Values{"grant_type": {"refresh_token"}, "client_id": {clientB.ID}, "client_secret": {secretB}, "refresh_token": {tokensB.RefreshToken}}, nil)
			requireStatus(t, http.StatusOK, response)
			var rotatedB oauth.TokenResponse
			require.NoError(t, json.Unmarshal(response.Body, &rotatedB))
			require.NotEmpty(t, rotatedB.AccessToken)
			input.Active = true
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
				_, err := identityService.ReplaceProvisionedTo(t.Context(), tx, iamTenantA, principal.ID, input)
				return err
			}))
			checkWithdrawn()
			response = f.request(t, http.MethodGet, "/entities", iamTenantA, "", nil, browserHeaders)
			requireStatus(t, http.StatusOK, response)
			response = f.request(t, http.MethodGet, "/oauth/userinfo", 0, rotatedB.AccessToken, nil, nil)
			requireStatus(t, http.StatusOK, response)
		})
	}
}
