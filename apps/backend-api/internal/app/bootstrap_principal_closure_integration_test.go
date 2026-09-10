package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestComposedPrincipalClosure(t *testing.T) {
	f := newIAMTenantFixtureWithDatabase(t, Database{Type: "sqlite", Dsn: filepath.Join(t.TempDir(), "closure.sqlite"), Writable: true, Readable: true}, "")
	f.runPrincipalClosure(t)
}

func TestPrincipalClosureExternalDatabase(t *testing.T) {
	database := Database{Type: os.Getenv(iamLifecycleDatabaseType), Dsn: os.Getenv(iamLifecycleDatabaseDSN), Writable: true, Readable: true, MaxOpenConns: 8, MaxIdleConns: 4}
	if database.Type == "" || database.Dsn == "" {
		t.Skip("external IAM lifecycle database is not configured")
	}
	f := newIAMTenantFixtureWithDatabase(t, database, "clx_")
	f.runPrincipalClosure(t)
	f.runClosureAdmissionInterleavings(t)
}

func (f *iamTenantFixture) closureCookie(t *testing.T, principal identity.Principal) string {
	t.Helper()
	response := f.request(t, http.MethodPost, "/authn/sessions", 0, "", map[string]any{
		"login_name": principal.LoginName, "password": "correct horse battery staple",
	}, map[string]string{"Origin": "https://app.example.test"})
	requireStatus(t, http.StatusOK, response)
	require.NotEmpty(t, response.Cookies)
	return response.Cookies[0].String()
}

func (f *iamTenantFixture) closureProof(t *testing.T, cookie, purpose, target string) string {
	t.Helper()
	service := requireIAMModule[*authn.Module](t, f, authn.Name).Service
	options, err := service.BeginReauthentication(t.Context(), cookie, purpose, target, "password", "https://app.example.test")
	require.NoError(t, err)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "https://app.example.test", nil))
	return options.Proof
}

func (f *iamTenantFixture) closurePAT(t *testing.T, principal identity.Principal, cookie string, tenant guid.ID) authn.PersonalTokenCreated {
	t.Helper()
	roleResponse := f.request(t, http.MethodPost, "/iam/roles", tenant, f.tokenB, map[string]any{
		"name": "Closure test access " + principal.ID.String(),
	}, nil)
	requireStatus(t, http.StatusCreated, roleResponse)
	role := decodeEnvelope[core.APIRole](t, roleResponse)
	requireStatus(t, http.StatusOK, f.request(t, http.MethodPut, "/iam/roles/"+role.ID+"/permissions/entity_view", tenant, f.tokenB, nil, nil))
	requireStatus(t, http.StatusOK, f.request(t, http.MethodPut, "/iam/roles/"+role.ID+"/members/"+principal.ID.String(), tenant, f.tokenB, nil, nil))
	service := requireIAMModule[*authn.Module](t, f, authn.Name).Service
	prepared, err := service.PreparePersonalToken(t.Context(), cookie, authn.PersonalTokenRequest{
		Name: "Closure verification", TenantID: tenant, Permissions: []string{"entity_view"}, ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	require.NoError(t, err)
	proof := f.closureProof(t, cookie, "personal_token_create", prepared.Target)
	created, err := service.CreatePersonalToken(t.Context(), cookie, proof, prepared.Request)
	require.NoError(t, err)
	requireStatus(t, http.StatusOK, f.request(t, http.MethodGet, "/iam/entities", tenant, created.Token, nil, nil))
	return created
}

const closureCodeVerifier = "closure-verifier-with-at-least-forty-three-characters"

func closureCodeChallenge() string {
	sum := sha256.Sum256([]byte(closureCodeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (f *iamTenantFixture) closureOAuth(t *testing.T, cookie string, tenant guid.ID) (oauth.Client, string, oauth.TokenResponse) {
	t.Helper()
	service := requireIAMModule[*oauth.Module](t, f, oauth.Name).Service
	client, secret, err := service.CreateClient(t.Context(), tenant, "Closure application", []string{"https://app.example.test/callback"}, []string{"authorization_code", "refresh_token"}, []string{"openid"}, false, oauth.ConsentImplicit)
	require.NoError(t, err)
	location, err := service.Authorize(t.Context(), cookie, client.ID, client.RedirectURIs[0], "code", "openid", "", closureCodeChallenge(), "S256", "", "")
	require.NoError(t, err)
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	tokens, err := service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "client_secret": {secret},
		"code": {parsed.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {closureCodeVerifier},
	}, "")
	require.NoError(t, err)
	return client, secret, tokens
}

func (f *iamTenantFixture) runPrincipalClosure(t *testing.T) {
	identityService := requireIAMModule[*identity.Module](t, f, identity.Name).Service
	authnService := requireIAMModule[*authn.Module](t, f, authn.Name).Service
	oauthService := requireIAMModule[*oauth.Module](t, f, oauth.Name).Service
	adminContext := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: f.adminA.ID, SubjectType: authnext.SubjectTypePrincipal})
	t.Run("zero-tenant-real-revocation-adapters", func(t *testing.T) {
		principal := f.requesterB
		cookie := f.closureCookie(t, principal)
		pat := f.closurePAT(t, principal, cookie, iamTenantB)
		client, secret, tokens := f.closureOAuth(t, cookie, iamTenantB)
		_, err := authnService.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
		require.NoError(t, err)
		require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("principal_id = ?", principal.ID).Delete(map[string]any{}).Error)
		proof := f.closureProof(t, cookie, "principal_close", principal.ID.String())
		request, err := identityService.ChangeClosureRequest(t.Context(), principal.ID, 0, "pending", cookie, proof)
		require.NoError(t, err)
		_, err = authnService.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err, "request alone preserves global browser access")
		processed, err := identityService.ProcessClosureRequest(adminContext, principal.ID, request.Version)
		require.NoError(t, err)
		require.Equal(t, "processing", processed.Status)
		require.EqualValues(t, 0, f.count(t, "iam_tenant_members", "principal_id = ?", principal.ID))
		require.EqualValues(t, 1, f.count(t, "iam_principals", "id = ? AND status = 'disabled'", principal.ID))
		require.EqualValues(t, 1, f.count(t, "iam_credentials", "principal_id = ? AND credential_version = 2", principal.ID))
		for _, table := range []string{"iam_sessions", "iam_refresh_tokens", "iam_personal_tokens"} {
			require.Zero(t, f.count(t, table, "principal_id = ? AND revoked_at IS NULL", principal.ID), table)
		}
		require.Zero(t, f.count(t, "iam_oauth_consents", "principal_id = ?", principal.ID))
		policy, err := identityService.GetPrincipalLifecycle(t.Context(), principal.ID)
		require.NoError(t, err)
		require.Zero(t, policy.Version, "processing cannot establish a retention policy")
		require.EqualValues(t, 1, f.count(t, "iam_audit_events", "target_id = ? AND principal_id = ? AND event_type = 'principal_closure_processing'", principal.ID, f.adminA.ID))
		// Restore through the existing admin lifecycle after re-admission. Old
		// browser credentials and grant incarnations must remain withdrawn.
		require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Create(map[string]any{"tenant_id": iamTenantB, "principal_id": principal.ID, "display_name": principal.DisplayName, "email": principal.Email, "status": "active", "created_at": time.Now().UTC(), "updated_at": time.Now().UTC()}).Error)
		_, err = identityService.SetStatus(adminContext, iamTenantB, principal.ID, "active")
		require.NoError(t, err)
		_, err = authnService.Authenticate(t.Context(), "", cookie)
		require.Error(t, err)
		_, err = authnService.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
		require.Error(t, err)
		requireStatus(t, http.StatusUnauthorized, f.request(t, http.MethodGet, "/iam/entities", iamTenantB, pat.Token, nil, nil))
		_, err = oauthService.Token(t.Context(), url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ID}, "client_secret": {secret}, "refresh_token": {tokens.RefreshToken}}, "")
		require.Error(t, err)
	})
	t.Run("last-administrator-rolls-back", func(t *testing.T) {
		principal := f.createPrincipal(t, requireIAMModule[*identity.Module](t, f, identity.Name), iamTenantB, "closure-owner", "closure-owner@example.test")
		tenantResponse := f.request(t, http.MethodPost, "/iam/tenants", 0, f.tokenA, map[string]any{"slug": "closure-owned", "name": "Closure owned", "owner_principal_id": principal.ID.String()}, nil)
		requireStatus(t, http.StatusCreated, tenantResponse)
		tenant := decodeEnvelope[organization.Tenant](t, tenantResponse)
		cookie := f.closureCookie(t, principal)
		_, _, tokens := f.closureOAuth(t, cookie, tenant.ID)
		proof := f.closureProof(t, cookie, "principal_close", principal.ID.String())
		request, err := identityService.ChangeClosureRequest(t.Context(), principal.ID, 0, "pending", cookie, proof)
		require.NoError(t, err)
		_, err = identityService.ProcessClosureRequest(adminContext, principal.ID, request.Version)
		require.ErrorIs(t, err, core.ErrLastTenantAdministrator)
		require.EqualValues(t, 1, f.count(t, "iam_principals", "id = ? AND status = 'active'", principal.ID))
		require.EqualValues(t, 1, f.count(t, "iam_credentials", "principal_id = ? AND credential_version = 1", principal.ID))
		stored, err := identityService.GetClosureRequest(t.Context(), principal.ID)
		require.NoError(t, err)
		require.Equal(t, request, stored)
		_, err = authnService.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		_, err = authnService.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
		require.NoError(t, err)
		leaveProof := f.closureProof(t, cookie, "tenant_leave", tenant.ID.String())
		response := f.request(t, http.MethodPost, "/iam/me/memberships/"+tenant.ID.String()+"/leave", 0, "", map[string]any{"proof": leaveProof}, map[string]string{"Cookie": cookie, "Origin": "https://app.example.test"})
		requireStatus(t, http.StatusConflict, response)
		sum := sha256.Sum256([]byte(leaveProof))
		require.EqualValues(t, 1, f.count(t, "iam_reauth_challenges", "id_hash = ? AND consumed_at IS NULL", hex.EncodeToString(sum[:])))
		_, err = authnService.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
		require.NoError(t, err, "a rejected tenant exit also rolls back its OAuth revocation")
	})
}

func (f *iamTenantFixture) runClosureAdmissionInterleavings(t *testing.T) {
	for _, operation := range []string{"leave", "close"} {
		for _, prompt := range []string{"", "consent"} {
			t.Run("late-"+operation+"-"+prompt, func(t *testing.T) {
				name := "late-" + operation + "-" + strings.ReplaceAll(prompt, "consent", "screen")
				principal := f.createPrincipal(t, requireIAMModule[*identity.Module](t, f, identity.Name), iamTenantB, name, name+"@example.test")
				cookie := f.closureCookie(t, principal)
				client, _, oldTokens := f.closureOAuth(t, cookie, iamTenantB)
				var pat authn.PersonalTokenCreated
				if operation == "leave" {
					pat = f.closurePAT(t, principal, cookie, iamTenantB)
				}
				service := requireIAMModule[*oauth.Module](t, f, oauth.Name).Service
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				paused, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				var pauseOnce sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				const callback = "test:pause_closure_oauth_admission"
				require.NoError(t, f.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Context != ctx || tx.Statement.Table != f.table("iam_oauth_consents") {
						return
					}
					pauseOnce.Do(func() {
						close(paused)
						select {
						case <-release:
						case <-ctx.Done():
							tx.AddError(ctx.Err())
						}
					})
				}))
				defer func() { require.NoError(t, f.db.Callback().Query().Remove(callback)) }()
				result := make(chan error, 1)
				go func() {
					_, err := service.Authorize(ctx, cookie, client.ID, client.RedirectURIs[0], "code", "openid", "", closureCodeChallenge(), "S256", "", prompt)
					result <- err
				}()
				select {
				case <-paused:
				case <-ctx.Done():
					t.Fatal("OAuth never reached its pre-admission consent read")
				}
				if operation == "leave" {
					proof := f.closureProof(t, cookie, "tenant_leave", iamTenantB.String())
					requireStatus(t, http.StatusOK, f.request(t, http.MethodPost, "/iam/me/memberships/"+iamTenantB.String()+"/leave", 0, "", map[string]any{"proof": proof}, map[string]string{"Cookie": cookie, "Origin": "https://app.example.test"}))
				} else {
					identityService := requireIAMModule[*identity.Module](t, f, identity.Name).Service
					proof := f.closureProof(t, cookie, "principal_close", principal.ID.String())
					request, err := identityService.ChangeClosureRequest(t.Context(), principal.ID, 0, "pending", cookie, proof)
					require.NoError(t, err)
					_, err = identityService.ProcessClosureRequest(t.Context(), principal.ID, request.Version)
					require.NoError(t, err)
				}
				unblock()
				require.ErrorIs(t, awaitErasure(t, ctx, result), oauth.ErrInvalidRequest)
				require.Zero(t, f.count(t, "iam_oauth_consents", "principal_id = ?", principal.ID))
				require.Zero(t, f.count(t, "iam_oauth_consent_requests", "principal_id = ? AND resolved_at IS NULL", principal.ID))
				if operation == "leave" {
					authenticator := requireIAMModule[*authn.Module](t, f, authn.Name).Service
					_, err := authenticator.Authenticate(t.Context(), "", cookie)
					require.NoError(t, err, "leaving the final tenant preserves global browser access")
					require.Zero(t, f.count(t, "iam_personal_tokens", "principal_id = ? AND revoked_at IS NULL", principal.ID))
					require.NoError(t, f.db.Table(f.table("iam_tenant_members")).Where("principal_id = ? AND tenant_id = ?", principal.ID, iamTenantB).Update("status", "active").Error)
					_, err = authenticator.Authenticate(t.Context(), "Bearer "+oldTokens.AccessToken, "")
					require.Error(t, err, "rejoining cannot revive the departed tenant's grant")
					requireStatus(t, http.StatusUnauthorized, f.request(t, http.MethodGet, "/iam/entities", iamTenantB, pat.Token, nil, nil))
				}
			})
		}
	}
}
