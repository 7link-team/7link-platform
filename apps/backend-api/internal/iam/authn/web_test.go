package authn

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	identitymodel "7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func localAuthnConfig() authnext.Config {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	return authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", Audience: []string{"7link-api"},
		SigningKey: key,
		Web: authnext.WebConfig{
			Enabled: true, CookieName: "session", CookieSecure: true,
			PostLoginURL: "https://app.example.test/home", PostLogoutURL: "https://app.example.test/signed-out",
			AllowedReturnURLs: []string{"https://app.example.test/home"},
			AllowedOrigins:    []string{"https://app.example.test"},
			SessionTTL:        time.Hour, IdleTTL: 15 * time.Minute,
		},
		MFA: authnext.MFAConfig{EncryptionKey: key},
	}
}

// newMFATestKeyring wraps raw key material the way the module does, so a test
// that seals through the ring and a test that reads a stored row are talking
// about the same key.
func newMFATestKeyring(t *testing.T, active []byte, previous ...[]byte) *keyringx.Keyring {
	t.Helper()
	encoded := make([]string, 0, len(previous))
	for _, key := range previous {
		encoded = append(encoded, base64.RawStdEncoding.EncodeToString(key))
	}
	ring, err := keyringx.New(mfaKeyDomain, base64.RawStdEncoding.EncodeToString(active), encoded...)
	require.NoError(t, err)
	return ring
}

func newLocalService(t *testing.T) (*Service, *authnRepositoryHarness) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	service, err := NewWebService(localAuthnConfig(), harness.repository,
		WithMemberTenantResolver(boundMemberTenantResolver(t, harness.runtime)))
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	return service, harness
}

func bootstrapLocalPrincipal(t *testing.T, harness *authnRepositoryHarness, login, password string) guid.ID {
	t.Helper()
	return seedLocalPrincipal(t, harness.repository, login, login+"@example.test", password)
}

func TestNewWebServiceConfiguration(t *testing.T) {
	disabled, err := NewWebService(authnext.Config{}, nil)
	require.NoError(t, err)
	assert.False(t, disabled.Enabled())
	assert.ErrorIs(t, disabled.ValidateLoginOrigin(""), ErrCSRF)
	assert.Empty(t, disabled.JWKS()["keys"])

	_, err = NewWebService(authnext.Config{Enabled: true}, nil)
	assert.ErrorContains(t, err, "writable database")

	harness := newAuthnRepositoryHarness(t, "")
	config := localAuthnConfig()
	config.Issuer = ""
	_, err = NewWebService(config, harness.repository)
	assert.ErrorContains(t, err, "issuer")

	config = localAuthnConfig()
	config.MFA.EncryptionKey = "invalid"
	_, err = NewWebService(config, harness.repository)
	assert.ErrorContains(t, err, "authn MFA encryption key")
	assert.ErrorContains(t, err, "must decode to 32 bytes")

	config = localAuthnConfig()
	config.MFA.EncryptionKey = "not base64 at all !!"
	_, err = NewWebService(config, harness.repository)
	assert.ErrorContains(t, err, "must be base64-encoded")
}

func TestBootstrapLoginSessionAndBearerLifecycle(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "Admin", "correct horse battery staple")

	result, err := service.BeginLogin(
		t.Context(), " ADMIN ", "correct horse battery staple", "",
	)
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result.Status)
	assert.Equal(t, service.web.PostLoginURL, result.ReturnURL)
	require.NotEmpty(t, result.SessionID)

	cookie := service.SessionCookie(result.SessionID)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.Equal(t, principalID, claims.PrincipalID)
	assert.Equal(t, []string{"pwd"}, claims.AMR)
	assert.Equal(t, 1, claims.ACR)

	sessions, err := service.ListSessions(t.Context(), "", cookie)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.True(t, sessions[0].Current)

	accessToken, expiresIn, err := service.IssueAccessToken(t.Context(), principalID, "", "openid")
	require.NoError(t, err)
	assert.Equal(t, int64((15 * time.Minute).Seconds()), expiresIn)
	bearerClaims, err := service.Authenticate(t.Context(), "Bearer "+accessToken, "")
	require.NoError(t, err)
	assert.Equal(t, principalID, bearerClaims.PrincipalID)
	assert.Equal(t, "openid", tokenPayloadString(t, accessToken, "scope"))

	logoutURL, err := service.Logout(t.Context(), cookie)
	require.NoError(t, err)
	assert.Equal(t, service.web.PostLogoutURL, logoutURL)
	_, err = service.Authenticate(t.Context(), "", cookie)
	assert.ErrorIs(t, err, ErrInvalidSession)
}

func TestLoginFailureLockoutAndReturnURL(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	service.cfg.Password.MaxFailedAttempts = 2
	service.cfg.Password.LockDuration = time.Hour

	_, err := service.BeginLogin(t.Context(), "missing", "wrong", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "alice", "wrong", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "alice", "wrong", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	credential, err := harness.repository.getCredential(t.Context(), principalID)
	require.NoError(t, err)
	require.NotNil(t, credential.LockedUntil)
	assert.True(t, credential.LockedUntil.After(service.now()))

	_, err = service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "https://evil.example")
	assert.ErrorIs(t, err, authnext.ErrReturnURL)
}

func TestChangePasswordRevokesOtherAuthenticationAndOldTokens(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	first, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	second, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	oldAccessToken, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
	require.NoError(t, err)

	firstCookie := service.SessionCookie(first.SessionID)
	require.NoError(t, service.ChangePassword(
		t.Context(), "", firstCookie,
		"correct horse battery staple", "different horse battery staple",
	))
	_, err = service.Authenticate(t.Context(), "", firstCookie)
	require.NoError(t, err, "the current browser session remains active")
	_, err = service.Authenticate(t.Context(), "", service.SessionCookie(second.SessionID))
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = service.Authenticate(t.Context(), "Bearer "+oldAccessToken, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)

	_, err = service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.BeginLogin(t.Context(), "alice", "different horse battery staple", "")
	require.NoError(t, err)
	assert.NotEmpty(t, harness.audits)
}

func TestSessionOwnershipAndCSRF(t *testing.T) {
	service, harness := newLocalService(t)
	bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	bootstrapLocalPrincipal(t, harness, "bob", "correct horse battery staple")
	alice, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	bob, err := service.BeginLogin(t.Context(), "bob", "correct horse battery staple", "")
	require.NoError(t, err)

	err = service.RevokeSession(t.Context(), "", service.SessionCookie(alice.SessionID), tokenHash(bob.SessionID))
	assert.ErrorIs(t, err, ErrSessionNotFound)
	_, err = service.Authenticate(t.Context(), "", service.SessionCookie(bob.SessionID))
	require.NoError(t, err)

	assert.NoError(t, service.ValidateCSRF("GET", "", service.SessionCookie(alice.SessionID), ""))
	assert.NoError(t, service.ValidateCSRF("POST", "", "", ""))
	assert.NoError(t, service.ValidateCSRF("POST", "", service.SessionCookie(alice.SessionID), "Bearer token"))
	assert.ErrorIs(t, service.ValidateCSRF("POST", "https://evil.example", service.SessionCookie(alice.SessionID), ""), ErrCSRF)
	assert.NoError(t, service.ValidateCSRF("POST", "https://app.example.test", service.SessionCookie(alice.SessionID), ""))
}

func tokenPayloadString(t *testing.T, token, key string) string {
	t.Helper()
	segments := strings.Split(token, ".")
	require.Len(t, segments, 3)
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	value, ok := claims[key].(string)
	require.True(t, ok)
	return value
}

func TestDisabledAndNilServiceMethodsFailClosed(t *testing.T) {
	disabled, err := NewWebService(authnext.Config{}, nil)
	require.NoError(t, err)
	_, _, err = disabled.IssueSubjectToken("service", "audience", "", "", "")
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	_, err = disabled.CreateSession(t.Context(), 1, time.Now(), authnext.Assurance{})
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	_, err = (*Service)(nil).Authenticate(t.Context(), "Bearer invalid", "")
	assert.True(t, errors.Is(err, authnext.ErrInvalidToken) || errors.Is(err, authnext.ErrMissingBearer))
}

type claimEnricherFunc func(context.Context, ClaimContext) (map[string]any, error)

func (f claimEnricherFunc) Enrich(ctx context.Context, claimContext ClaimContext) (map[string]any, error) {
	return f(ctx, claimContext)
}

func TestWebServicePublicHelpersAndLoginWrapper(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	assert.Equal(t, "session", service.SessionCookieName())
	assert.True(t, service.CookieSecure())
	assert.Equal(t, "https://app.example.test/signed-out", service.PostLogoutURL())
	assert.Equal(t, "https://iam.example.test", service.Issuer())
	assert.Empty(t, (*Service)(nil).PostLogoutURL())
	assert.Empty(t, (*Service)(nil).Issuer())
	assert.False(t, (*Service)(nil).CookieSecure())
	assert.Contains(t, service.ClearCookie(), "Max-Age=0")

	token, returnURL, err := service.Login(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	assert.Equal(t, service.web.PostLoginURL, returnURL)
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(token))
	require.NoError(t, err)
	assert.Equal(t, principalID, claims.PrincipalID)

	require.NoError(t, harness.repository.requireMFA(t.Context(), principalID, service.now()))
	_, _, err = service.Login(t.Context(), "alice", "correct horse battery staple", "")
	assert.ErrorIs(t, err, authnext.ErrAdditionalVerification)
}

func TestLogoutAllRevokesEveryAuthentication(t *testing.T) {
	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	first, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	second, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	bearer, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
	require.NoError(t, err)
	require.NoError(t, service.LogoutAll(t.Context(), "", service.SessionCookie(first.SessionID)))
	_, err = service.Authenticate(t.Context(), "", service.SessionCookie(first.SessionID))
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = service.Authenticate(t.Context(), "", service.SessionCookie(second.SessionID))
	assert.ErrorIs(t, err, ErrInvalidSession)
	_, err = service.Authenticate(t.Context(), "Bearer "+bearer, "")
	require.NoError(t, err, "logout-all does not revoke already issued short-lived access tokens")
}

func TestSubjectAndIDTokenVariantsAndEnrichment(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	config := localAuthnConfig()
	config.AccessTokenTTL = time.Minute
	var contexts []ClaimContext
	enricher := claimEnricherFunc(func(_ context.Context, claimContext ClaimContext) (map[string]any, error) {
		contexts = append(contexts, claimContext)
		return map[string]any{"region": "west"}, nil
	})
	verified := false
	service, err := NewWebService(
		config, harness.repository,
		WithClaimEnricher(enricher),
		WithOAuthGrantValidator(func(_ context.Context, claims *authnext.Claims) error {
			assert.Equal(t, "browser", claims.ClientID)
			assert.Equal(t, "current-grant", claims.OAuthGrantID)
			return nil
		}),
		WithVerifiedHook(func(context.Context, *gorm.DB, guid.ID, string, time.Time) error { verified = true; return nil }),
	)
	require.NoError(t, err)
	assert.NotNil(t, service.verifiedHook)
	assert.False(t, verified)
	service.now = func() time.Time { return repositoryTestNow }
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")

	serviceToken, expires, err := service.IssueSubjectToken(
		"worker", "", "jobs.read", "Worker", "worker@example.test",
	)
	require.NoError(t, err)
	assert.Equal(t, int64(60), expires)
	serviceClaims, err := service.Authenticate(t.Context(), "Bearer "+serviceToken, "")
	require.NoError(t, err)
	assert.Equal(t, authnext.SubjectTypeService, serviceClaims.SubjectType)
	tenantServiceToken, _, err := service.IssueTenantSubjectToken(
		t.Context(), "worker", "tenant-a", "7link-api", "jobs.write", "Worker", "",
	)
	require.NoError(t, err)
	tenantClaims, err := service.Authenticate(t.Context(), "Bearer "+tenantServiceToken, "")
	require.NoError(t, err)
	assert.Equal(t, "tenant-a", tenantClaims.OrganizationID)

	access, _, err := service.IssueTenantAccessTokenWithAssurance(
		t.Context(), principalID, "tenant-a", "7link-api", "openid",
		authnext.Assurance{
			AuthTime: repositoryTestNow.Add(-time.Minute), Level: 2,
			Methods: []string{"pwd", "totp"}, ClientID: "browser", OAuthGrantID: "current-grant", NetworkZone: "trusted",
		},
	)
	require.NoError(t, err)
	accessClaims, err := service.Authenticate(t.Context(), "Bearer "+access, "")
	require.NoError(t, err)
	assert.Equal(t, "tenant-a", accessClaims.OrganizationID)
	assert.Equal(t, 2, accessClaims.ACR)
	assert.Equal(t, "trusted", accessClaims.NetworkZone)

	idToken, err := service.IssueIDToken(t.Context(), principalID, "browser", "nonce")
	require.NoError(t, err)
	assert.Equal(t, "nonce", tokenPayload(t, idToken)["nonce"])
	tenantIDToken, err := service.IssueTenantIDToken(t.Context(), principalID, "tenant-a", "browser", "")
	require.NoError(t, err)
	assert.Equal(t, "tenant-a", tokenPayload(t, tenantIDToken)["organization_id"])
	assuredIDToken, err := service.IssueTenantIDTokenWithAssurance(
		t.Context(), principalID, "tenant-a", "browser", "", "openid",
		authnext.Assurance{AuthTime: repositoryTestNow, Level: 2, Methods: []string{"passkey"}},
	)
	require.NoError(t, err)
	assert.InDelta(t, 2, tokenPayload(t, assuredIDToken)["acr"], 0)
	assert.Len(t, contexts, 6)
	assert.Equal(t, "id_token", contexts[len(contexts)-1].TokenType)
	assert.Equal(t, "west", tokenPayload(t, access)["ext"].(map[string]any)["region"])
}

func TestApplicationBearerRequiresAvailableGrantValidator(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	service, err := NewWebService(localAuthnConfig(), harness.repository)
	require.NoError(t, err)
	token, _, err := service.IssueTenantAccessTokenWithAssurance(t.Context(), principalID, "7", "", "openid", authnext.Assurance{
		ClientID: "22", OAuthGrantID: "current-grant",
	})
	require.NoError(t, err)
	_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken, "missing OAuth capability must fail closed")
	service.oauthGrants = func(context.Context, *authnext.Claims) error { return authnext.ErrUnavailable }
	_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
	assert.ErrorIs(t, err, authnext.ErrUnavailable, "storage outages remain server failures")
}

func TestTokenEnricherFailureIsReturned(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	service, err := NewWebService(localAuthnConfig(), harness.repository, WithClaimEnricher(
		claimEnricherFunc(func(context.Context, ClaimContext) (map[string]any, error) {
			return nil, assert.AnError
		}),
	))
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	_, _, err = service.IssueAccessToken(t.Context(), principalID, "", "")
	assert.ErrorContains(t, err, "enrich token claims")
	_, err = service.IssueIDToken(t.Context(), principalID, "browser", "")
	assert.ErrorContains(t, err, "enrich token claims")
}

func TestOAuthClientTokenLifecycle(t *testing.T) {
	service, harness := newLocalService(t)
	clientID, tenantID := guid.ID(501), guid.ID(601)
	require.NoError(t, harness.db.Table("iam_oauth_clients").Create(map[string]any{
		"id": clientID, "tenant_id": tenantID, "status": "active",
	}).Error)
	token, _, err := service.IssueTenantOAuthClientToken(
		t.Context(), clientID.String(), tenantID.String(), "7link-api", "jobs.read", "Worker",
	)
	require.NoError(t, err)
	claims, err := service.Authenticate(t.Context(), "Bearer "+token, "")
	require.NoError(t, err)
	assert.Equal(t, authnext.SubjectTypeOAuthClient, claims.SubjectType)
	assert.Equal(t, "client:"+clientID.String(), claims.Subject)

	_, _, err = service.IssueTenantOAuthClientToken(
		t.Context(), "invalid", tenantID.String(), "7link-api", "", "Worker",
	)
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	_, _, err = service.IssueTenantOAuthClientToken(
		t.Context(), clientID.String(), guid.ID(999).String(), "7link-api", "", "Worker",
	)
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	require.NoError(t, harness.db.Table("iam_oauth_clients").Where("id = ?", clientID).
		Update("status", "disabled").Error)
	_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
}

func TestServiceAccountTokenLifecycle(t *testing.T) {
	service, harness := newLocalService(t)
	principalID, tenantID := guid.ID(701), guid.ID(801)
	harness.seedPrincipal(t, principalID, "automation")
	require.NoError(t, harness.db.Table(identitymodel.ServiceAccount{}.TableName()).Create(&identitymodel.ServiceAccount{
		PrincipalID: principalID, OwnerTenantID: tenantID, Status: "active", TokenVersion: 3,
		Version: 1, CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}).Error)
	require.NoError(t, harness.db.Table("iam_tenants").Create(map[string]any{
		"id": tenantID, "status": "active",
	}).Error)
	require.NoError(t, harness.db.Table("iam_tenant_members").Create(map[string]any{
		"tenant_id": tenantID, "principal_id": principalID, "status": "active",
	}).Error)
	token, _, err := service.IssueTenantServiceAccountToken(
		t.Context(), principalID.String(), tenantID.String(), "7link-api", "jobs.read", "automation", 3,
	)
	require.NoError(t, err)
	claims, err := service.Authenticate(t.Context(), "Bearer "+token, "")
	require.NoError(t, err)
	assert.Equal(t, authnext.SubjectTypeServiceAccount, claims.SubjectType)
	assert.Equal(t, principalID, claims.PrincipalID)

	wrongVersion, _, err := service.IssueTenantServiceAccountToken(
		t.Context(), principalID.String(), tenantID.String(), "7link-api", "", "automation", 2,
	)
	require.NoError(t, err)
	_, err = service.Authenticate(t.Context(), "Bearer "+wrongVersion, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	require.NoError(t, harness.db.Table(identitymodel.ServiceAccount{}.TableName()).
		Where("principal_id = ?", principalID).Update("status", "disabled").Error)
	_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
}

func TestWebServiceDatabaseFailuresFailClosed(t *testing.T) {
	t.Run("principal lookup", func(t *testing.T) {
		service, harness := newLocalService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_principals").Error)
		_, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		assert.ErrorContains(t, err, "find principal")
	})

	t.Run("credential lookup", func(t *testing.T) {
		service, harness := newLocalService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		require.NoError(t, harness.db.Exec("DROP TABLE iam_credentials").Error)
		_, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		assert.ErrorContains(t, err, "read credential")
	})

	t.Run("session operations", func(t *testing.T) {
		service, harness := newLocalService(t)
		principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		bearer, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_sessions").Error)
		_, err = service.Authenticate(t.Context(), "", service.SessionCookie(login.SessionID))
		assert.ErrorIs(t, err, authnext.ErrUnavailable)
		_, err = service.ListSessions(t.Context(), "Bearer "+bearer, "")
		assert.ErrorContains(t, err, "list browser sessions")
		err = service.RevokeSession(t.Context(), "Bearer "+bearer, "", strings.Repeat("a", 64))
		assert.ErrorContains(t, err, "revoke browser session")
		err = service.LogoutAll(t.Context(), "Bearer "+bearer, "")
		assert.ErrorContains(t, err, "revoke all sessions")
		err = service.ChangePassword(
			t.Context(), "Bearer "+bearer, "", "correct horse battery staple", "different horse battery staple",
		)
		assert.ErrorContains(t, err, "change password")
		logoutURL, err := service.Logout(t.Context(), service.SessionCookie(login.SessionID))
		assert.ErrorContains(t, err, "revoke browser session")
		assert.Empty(t, logoutURL)
	})

	t.Run("OAuth client state", func(t *testing.T) {
		service, harness := newLocalService(t)
		clientID, tenantID := guid.ID(901), guid.ID(902)
		require.NoError(t, harness.db.Table("iam_oauth_clients").Create(map[string]any{
			"id": clientID, "tenant_id": tenantID, "status": "active",
		}).Error)
		token, _, err := service.IssueTenantOAuthClientToken(
			t.Context(), clientID.String(), tenantID.String(), "7link-api", "", "Worker",
		)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_clients").Error)
		_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
		assert.ErrorIs(t, err, authnext.ErrUnavailable)
		_, _, err = service.IssueTenantOAuthClientToken(
			t.Context(), clientID.String(), tenantID.String(), "7link-api", "", "Worker",
		)
		assert.ErrorIs(t, err, authnext.ErrUnavailable)
	})
}

func TestAccessTokenMalformedClaimsAndHeaders(t *testing.T) {
	service, _ := newLocalService(t)
	for _, header := range []string{"Basic value", "Bearer malformed", "Bearer !!!.body.signature"} {
		_, err := service.Authenticate(t.Context(), header, "")
		assert.Error(t, err, header)
	}
	base := map[string]any{
		"iss": service.Issuer(), "sub": "subject", "aud": []string{"7link-api"},
		"subject_type": authnext.SubjectTypeService,
		"iat":          service.now().Unix(), "exp": service.now().Add(time.Minute).Unix(),
	}
	for name, mutate := range map[string]func(map[string]any){
		"wrong issuer":  func(payload map[string]any) { payload["iss"] = "https://other.example" },
		"empty subject": func(payload map[string]any) { payload["sub"] = "" },
		"unknown type":  func(payload map[string]any) { payload["subject_type"] = "unknown" },
		"expired":       func(payload map[string]any) { payload["exp"] = service.now().Add(-time.Hour).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			payload := make(map[string]any, len(base))
			for key, value := range base {
				payload[key] = value
			}
			mutate(payload)
			token, err := service.sign(payload)
			require.NoError(t, err)
			_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
			assert.ErrorIs(t, err, authnext.ErrInvalidToken)
		})
	}
	wrongAudience := make(map[string]any, len(base))
	for key, value := range base {
		wrongAudience[key] = value
	}
	wrongAudience["aud"] = []string{"other"}
	token, err := service.sign(wrongAudience)
	require.NoError(t, err)
	_, err = service.Authenticate(t.Context(), "Bearer "+token, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidAud)
	assert.ErrorIs(t, service.verifySubjectState(t.Context(), nil), authnext.ErrInvalidToken)
	assert.ErrorIs(t, service.verifySubjectState(t.Context(), &authnext.Claims{SubjectType: "unknown"}), authnext.ErrInvalidToken)
	_, err = (*Service)(nil).sign(map[string]any{})
	assert.ErrorIs(t, err, authnext.ErrDisabled)
}

func TestSigningKeyFormatsAndUnsafeConfiguration(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	for name, encoded := range map[string]string{
		"raw standard": base64.RawStdEncoding.EncodeToString(seed),
		"standard":     base64.StdEncoding.EncodeToString(seed),
		"raw url":      base64.RawURLEncoding.EncodeToString(seed),
		"url":          base64.URLEncoding.EncodeToString(seed),
		"raw bytes":    strings.Repeat("!", ed25519.SeedSize),
	} {
		t.Run(name, func(t *testing.T) {
			key, err := parseSigningKey(encoded)
			require.NoError(t, err)
			assert.Len(t, key, ed25519.PrivateKeySize)
		})
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	key, err := parseSigningKey(base64.RawStdEncoding.EncodeToString(privateKey))
	require.NoError(t, err)
	assert.Equal(t, privateKey, key)
	_, err = parseSigningKey("")
	assert.Error(t, err)

	service, harness := newLocalService(t)
	tests := []struct {
		name   string
		mutate func(*authnext.Config)
		match  string
	}{
		{"MFA limits", func(config *authnext.Config) { config.MFA.RecoveryCodes = 21 }, "MFA configuration"},
		{"MFA key sources", func(config *authnext.Config) { config.MFA.EncryptionKeyFile = "also-set" }, "mutually exclusive"},
		{"missing MFA key", func(config *authnext.Config) { config.MFA.EncryptionKey = "" }, "MFA encryption key is required"},
		{"passkey limits", func(config *authnext.Config) {
			config.Passkey = authnext.PasskeyConfig{Enabled: true, MaxCredentials: 21}
		}, "passkey configuration"},
		{"passkey requires web", func(config *authnext.Config) {
			config.Web.Enabled = false
			config.Passkey.Enabled = true
		}, "passkey configuration"},
		{"passkey origin allowlist", func(config *authnext.Config) {
			config.Passkey = authnext.PasskeyConfig{
				Enabled: true, RPID: "app.example.test", Origins: []string{"https://other.example.test"},
			}
		}, "not an allowed web origin"},
		{"passkey attestation preference", func(config *authnext.Config) {
			config.Passkey = authnext.PasskeyConfig{
				Enabled: true, RPID: "app.example.test", Origins: []string{"https://app.example.test"},
				AttestationPreference: "unsupported",
			}
		}, "configure passkeys"},
		{"passkey AAGUID hex", func(config *authnext.Config) {
			config.Passkey = authnext.PasskeyConfig{
				Enabled: true, RPID: "app.example.test", Origins: []string{"https://app.example.test"},
				AllowedAAGUIDs: []string{"not-hex"},
			}
		}, "allowed_aaguid"},
		{"passkey AAGUID length", func(config *authnext.Config) {
			config.Passkey = authnext.PasskeyConfig{
				Enabled: true, RPID: "app.example.test", Origins: []string{"https://app.example.test"},
				AllowedAAGUIDs: []string{"aa"},
			}
		}, "exactly 16 bytes"},
		{"missing post-login URL", func(config *authnext.Config) {
			config.Web.PostLoginURL = ""
		}, "post_login_url"},
		{"post-login URL outside allowlist", func(config *authnext.Config) {
			config.Web.PostLoginURL = "https://other.example.test/home"
		}, "allowed_return_urls"},
		{"signing sources", func(config *authnext.Config) { config.SigningKeyFile = "also-set" }, "mutually exclusive"},
		{"signing key", func(config *authnext.Config) { config.SigningKey = "invalid" }, "signing key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := service.cfg
			test.mutate(&config)
			_, err := NewWebService(config, harness.repository)
			assert.ErrorContains(t, err, test.match)
		})
	}
}

func TestWebServiceDefaultsAndConfigurationBoundaries(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("d", 32)))
	service, err := NewWebService(authnext.Config{
		Enabled: true, Issuer: " https://iam.example.test/ ", SigningKey: key,
		Web: authnext.WebConfig{
			Enabled: true, PostLoginURL: "https://app.example.test/home",
			AllowedOrigins:    []string{"https://app.example.test"},
			AllowedReturnURLs: []string{"https://app.example.test/home"},
		},
		MFA: authnext.MFAConfig{EncryptionKey: key},
	}, harness.repository)
	require.NoError(t, err)
	assert.Equal(t, "https://iam.example.test", service.Issuer())
	assert.Equal(t, []string{"7link-api"}, service.cfg.Audience)
	assert.Equal(t, 15*time.Minute, service.cfg.AccessTokenTTL)
	assert.Equal(t, 30*time.Second, service.cfg.ClockSkew)
	assert.Equal(t, 5, service.cfg.Password.MaxFailedAttempts)
	assert.Equal(t, 15*time.Minute, service.cfg.Password.LockDuration)
	assert.Equal(t, authnext.DefaultSessionCookieName, service.SessionCookieName())
	assert.Equal(t, 8*time.Hour, service.web.SessionTTL)
	assert.Equal(t, 30*time.Minute, service.web.IdleTTL)
	assert.Equal(t, 10, service.cfg.MFA.RecoveryCodes)
	assert.Equal(t, 5*time.Minute, service.cfg.Passkey.ChallengeTTL)
	assert.Equal(t, 10, service.cfg.Passkey.MaxCredentials)
	assert.Equal(t, 15*time.Minute, service.cfg.Recovery.TokenTTL)
	assert.Equal(t, 24*time.Hour, service.cfg.EmailVerification.TokenTTL)
	assert.Equal(t, 5*time.Second, service.cfg.Notification.PollInterval)
	assert.Error(t, validateAuthnURL("test", "://invalid"))
	assert.Error(t, validateAuthnURL("test", "https://user@example.test/path"))
	assert.Error(t, validateAuthnURL("test", "https://example.test/path#fragment"))
	assert.NoError(t, validateAuthnURL("test", "http://127.0.0.1/callback"))

	capped := localAuthnConfig()
	capped.Web.SessionTTL = time.Hour
	capped.Web.IdleTTL = 2 * time.Hour
	cappedService, err := NewWebService(capped, harness.repository)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, cappedService.web.IdleTTL)

	base := localAuthnConfig()
	base.EmailVerification = authnext.EmailVerificationConfig{
		Enabled: true, TokenTTL: time.Hour, VerifyURL: "https://app.example.test/verify",
	}
	base.Notification = authnext.NotificationConfig{
		URL: "https://notify.example.test/events", PollInterval: time.Second,
		RequestTimeout: time.Second, MaxAttempts: 3,
	}
	cases := []struct {
		name   string
		mutate func(*authnext.Config)
		match  string
	}{
		{"notification limits", func(config *authnext.Config) { config.Notification.MaxAttempts = 21 }, "notification configuration"},
		{"notification URL", func(config *authnext.Config) { config.Notification.URL = "http://example.test/events" }, "must use HTTPS"},
		{"notification authorization sources", func(config *authnext.Config) {
			config.Notification.Authorization = "Bearer secret"
			config.Notification.AuthorizationFile = "also-set"
		}, "mutually exclusive"},
		{"verification limits", func(config *authnext.Config) { config.EmailVerification.TokenTTL = time.Minute }, "email verification configuration"},
		{"registration dependencies", func(config *authnext.Config) { config.Registration.Enabled = true }, "identity creator"},
		{"recovery limits", func(config *authnext.Config) {
			config.Recovery = authnext.RecoveryConfig{
				Enabled: true, TokenTTL: time.Minute, Cooldown: time.Hour,
				ResetURL: "https://app.example.test/recover",
			}
		}, "recovery configuration"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.mutate(&config)
			_, err := NewWebService(config, harness.repository)
			assert.ErrorContains(t, err, test.match)
		})
	}
}

func TestWebServiceReachableEdgeBranches(t *testing.T) {
	disabled, err := NewWebService(authnext.Config{}, nil)
	require.NoError(t, err)
	_, err = disabled.ResolveReturnURL("")
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	_, err = disabled.Authenticate(t.Context(), "", "")
	assert.ErrorIs(t, err, authnext.ErrMissingBearer)
	_, err = disabled.BeginLogin(t.Context(), "alice", "password", "")
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	_, err = disabled.IssueIDToken(t.Context(), 1, "client", "")
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	_, err = (*Service)(nil).Logout(t.Context(), "")
	assert.ErrorIs(t, err, authnext.ErrDisabled)
	(*Service)(nil).audit(t.Context(), 1, "ignored", "denied")

	service, harness := newLocalService(t)
	principalID := bootstrapLocalPrincipal(t, harness, "edge-user", "correct horse battery staple")
	_, err = service.ListSessions(t.Context(), "", "")
	assert.Error(t, err)
	assert.Error(t, service.RevokeSession(t.Context(), "", "", strings.Repeat("a", 64)))
	assert.Error(t, service.LogoutAll(t.Context(), "", ""))
	sessionID, err := service.CreateSession(t.Context(), principalID, service.now(), authnext.Assurance{})
	require.NoError(t, err)
	storedSession, err := harness.repository.activeSession(t.Context(), tokenHash(sessionID), service.now())
	require.NoError(t, err)
	assert.Equal(t, service.now(), storedSession.AuthTime)
	require.NoError(t, harness.db.Exec("DROP TABLE iam_sessions").Error)
	_, err = service.CreateSession(t.Context(), principalID, service.now(), authnext.Assurance{})
	assert.ErrorContains(t, err, "create session")
	_, err = service.BeginLogin(t.Context(), "edge-user", "correct horse battery staple", "")
	assert.ErrorContains(t, err, "create session")

	t.Run("MFA compatibility login", func(t *testing.T) {
		service, _, _, _ := enabledMFAService(t)
		_, returnURL, err := service.Login(t.Context(), "alice", "correct horse battery staple", "")
		assert.ErrorIs(t, err, authnext.ErrAdditionalVerification)
		assert.Equal(t, service.web.PostLoginURL, returnURL)
	})

	t.Run("session revoke validation and audit", func(t *testing.T) {
		service, harness := newLocalService(t)
		bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		current, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		other, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
		require.NoError(t, err)
		cookie := service.SessionCookie(current.SessionID)
		assert.ErrorIs(t, service.RevokeSession(t.Context(), "", cookie, "invalid"), ErrSessionNotFound)
		harness.auditErr = assert.AnError
		err = service.RevokeSession(t.Context(), "", cookie, tokenHash(other.SessionID))
		assert.ErrorContains(t, err, "revoke browser session")
		_, err = service.Authenticate(t.Context(), "", service.SessionCookie(other.SessionID))
		require.NoError(t, err, "audit failure must roll back revocation")
		harness.auditErr = nil
		require.NoError(t, service.RevokeSession(t.Context(), "", cookie, tokenHash(other.SessionID)))
		_, err = service.Authenticate(t.Context(), "", service.SessionCookie(other.SessionID))
		assert.ErrorIs(t, err, ErrInvalidSession)
	})

	t.Run("password validation and reuse", func(t *testing.T) {
		service, harness, cookie := authenticatedLocalService(t)
		assert.ErrorIs(t, service.ChangePassword(t.Context(), "", cookie, "", "different horse battery staple"), ErrInvalidPassword)
		assert.ErrorIs(t, service.ChangePassword(t.Context(), "", cookie, "correct horse battery staple", "short"), ErrInvalidPassword)
		assert.ErrorIs(t, service.ChangePassword(t.Context(), "", "", "correct horse battery staple", "different horse battery staple"), ErrInvalidSession)
		assert.ErrorIs(t, service.ChangePassword(t.Context(), "", cookie, "wrong password", "different horse battery staple"), authnext.ErrInvalidCredentials)
		assert.ErrorIs(t, service.ChangePassword(t.Context(), "", cookie, "correct horse battery staple", "correct horse battery staple"), ErrPasswordReused)
		claims, err := service.Authenticate(t.Context(), "", cookie)
		require.NoError(t, err)
		require.NoError(t, harness.db.Table("iam_password_history").Create(&model.PasswordHistory{
			ID: 999, PrincipalID: claims.PrincipalID, PasswordHash: "invalid", CreatedAt: service.now(),
		}).Error)
		err = service.ChangePassword(t.Context(), "", cookie, "correct horse battery staple", "different horse battery staple")
		assert.ErrorContains(t, err, "verify password history")
	})

	t.Run("password storage dependencies", func(t *testing.T) {
		newSession := func(t *testing.T) (*Service, *authnRepositoryHarness, guid.ID, string) {
			t.Helper()
			service, harness := newLocalService(t)
			principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
			login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
			require.NoError(t, err)
			return service, harness, principalID, service.SessionCookie(login.SessionID)
		}

		t.Run("credential missing", func(t *testing.T) {
			service, harness, principalID, cookie := newSession(t)
			require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principalID).Delete(nil).Error)
			err := service.ChangePassword(
				t.Context(), "", cookie, "correct horse battery staple", "different horse battery staple",
			)
			assert.ErrorContains(t, err, "load credential")
		})

		t.Run("credential corrupt", func(t *testing.T) {
			service, harness, principalID, cookie := newSession(t)
			require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principalID).
				Update("password_hash", "invalid").Error)
			err := service.ChangePassword(
				t.Context(), "", cookie, "correct horse battery staple", "different horse battery staple",
			)
			assert.ErrorContains(t, err, "verify current password")
		})

		t.Run("history unavailable", func(t *testing.T) {
			service, harness, _, cookie := newSession(t)
			require.NoError(t, harness.db.Exec("DROP TABLE iam_password_history").Error)
			err := service.ChangePassword(
				t.Context(), "", cookie, "correct horse battery staple", "different horse battery staple",
			)
			assert.ErrorContains(t, err, "load password history")
		})
	})

	t.Run("token metadata and malformed signatures", func(t *testing.T) {
		service, harness := newLocalService(t)
		principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
		assert.NotEmpty(t, service.JWKS()["keys"])
		_, err := service.sign(map[string]any{"unsupported": make(chan struct{})})
		assert.Error(t, err)
		_, _, err = service.IssueAccessToken(t.Context(), guid.ID(999999), "", "")
		assert.ErrorIs(t, err, ErrInvalidSession)
		valid, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
		require.NoError(t, err)
		parts := strings.Split(valid, ".")
		require.Len(t, parts, 3)
		for _, malformed := range []string{
			base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"wrong"}`)) + "." + parts[1] + "." + parts[2],
			parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString([]byte("bad-signature")),
		} {
			_, err = service.Authenticate(t.Context(), "Bearer "+malformed, "")
			assert.ErrorIs(t, err, authnext.ErrInvalidToken)
		}
	})
}

func tokenPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(payload, &result))
	return result
}
