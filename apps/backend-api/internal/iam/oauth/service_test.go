package oauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const oauthVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func oauthChallenge() string { return pkceChallenge(oauthVerifier) }

func createBrowserClient(t *testing.T, harness *oauthHarness) Client {
	t.Helper()
	client, _, err := harness.service.CreateClient(
		t.Context(), 7, "Browser", []string{"https://app.example/callback"},
		[]string{"authorization_code", "refresh_token"}, []string{"openid", "profile"}, true, ConsentImplicit,
	)
	require.NoError(t, err)
	return client
}

func authorizeCode(t *testing.T, harness *oauthHarness, client Client) string {
	t.Helper()
	redirect, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid profile",
		"state", oauthChallenge(), "S256", "nonce", "",
	)
	require.NoError(t, err)
	parsed, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.Equal(t, "state", parsed.Query().Get("state"))
	code := parsed.Query().Get("code")
	require.NotEmpty(t, code)
	return code
}

func exchangeCode(t *testing.T, harness *oauthHarness, client Client, code string) (TokenResponse, error) {
	t.Helper()
	return harness.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID},
		"code": {code}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
	}, "")
}

func TestNewOAuthServiceRequiresDependencies(t *testing.T) {
	harness := newOAuthHarness(t, "")
	assert.Panics(t, func() {
		NewService(nil, harness.web, newOAuthTestIDs(0), harness.repo.appender, harness.repo.principals, ConsentConfig{}, DeviceConfig{VerificationURI: "https://console.example.test/device"})
	})
	assert.Panics(t, func() {
		NewService(harness.runtime, nil, newOAuthTestIDs(0), harness.repo.appender, harness.repo.principals, ConsentConfig{}, DeviceConfig{VerificationURI: "https://console.example.test/device"})
	})
	assert.Panics(t, func() {
		NewService(harness.runtime, harness.web, nil, harness.repo.appender, harness.repo.principals, ConsentConfig{}, DeviceConfig{VerificationURI: "https://console.example.test/device"})
	})
	assert.Panics(t, func() {
		NewService(harness.runtime, harness.web, newOAuthTestIDs(0), nil, harness.repo.principals, ConsentConfig{}, DeviceConfig{VerificationURI: "https://console.example.test/device"})
	})
	assert.Panics(t, func() {
		NewService(harness.runtime, harness.web, newOAuthTestIDs(0), harness.repo.appender, identity.PrincipalStateStore{}, ConsentConfig{}, DeviceConfig{VerificationURI: "https://console.example.test/device"})
	})
}

func TestClientManagementIsTenantScopedAndAudited(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read", "jobs.write"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	assert.NotEmpty(t, secret)
	assert.Equal(t, []string{"oauth_client_created"}, harness.eventTypes(t))

	other, err := harness.service.ListClients(t.Context(), 8)
	require.NoError(t, err)
	assert.Empty(t, other)
	id, err := guid.Parse(client.ID)
	require.NoError(t, err)
	_, err = harness.service.UpdateClient(
		t.Context(), 8, id, "Other", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit, "active",
	)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.RotateClientSecret(t.Context(), 8, id)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	assert.ErrorIs(t, harness.service.DeleteClient(t.Context(), 8, id), ErrInvalidRequest)

	token, err := harness.service.Token(t.Context(), url.Values{
		"grant_type": {"client_credentials"}, "scope": {"jobs.read"},
	}, "Basic "+base64.StdEncoding.EncodeToString([]byte(client.ID+":"+secret)))
	require.NoError(t, err)
	assert.NotEmpty(t, token.AccessToken)

	updated, err := harness.service.UpdateClient(
		t.Context(), 7, id, "Worker Public", []string{"https://app.example/callback"},
		[]string{"authorization_code"}, []string{"openid"}, true, ConsentImplicit, "active",
	)
	require.NoError(t, err)
	assert.True(t, updated.PublicClient)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID},
	}, "Basic "+base64.StdEncoding.EncodeToString([]byte(client.ID+":"+secret)))
	assert.ErrorIs(t, err, ErrInvalidRequest, "a secret cleared by public mode must not remain accepted")

	_, err = harness.service.UpdateClient(
		t.Context(), 7, id, "Worker Again", nil,
		[]string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit, "active",
	)
	require.NoError(t, err)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"client_credentials"}, "client_id": {client.ID}, "client_secret": {secret},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest, "a public-to-confidential transition must not revive the old secret")

	rotated, err := harness.service.RotateClientSecret(t.Context(), 7, id)
	require.NoError(t, err)
	assert.NotEqual(t, secret, rotated)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"client_credentials"}, "client_id": {client.ID},
		"client_secret": {rotated}, "scope": {"jobs.read"},
	}, "")
	require.NoError(t, err)
	require.NoError(t, harness.service.DeleteClient(t.Context(), 7, id))
	assert.Equal(t, []string{
		"oauth_client_created", "oauth_client_updated", "oauth_client_updated",
		"oauth_client_secret_rotated", "oauth_client_deleted",
	}, harness.eventTypes(t))
	assert.Zero(t, harness.count(t, "iam_oauth_clients"))
}

func TestClientWritesRollBackWhenAuditFails(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	harness.auditErr = errors.New("audit unavailable")
	_, _, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	assert.ErrorContains(t, err, "audit unavailable")
	assert.Zero(t, harness.count(t, "iam_oauth_clients"))
	assert.Zero(t, harness.count(t, "oauth_test_audits"))
}

func TestClientMutationsRollBackWithAudit(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	id, err := guid.Parse(client.ID)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_refresh_tokens")).Create(&model.RefreshToken{
		IDHash: hashToken("refresh"), FamilyID: "family", PrincipalID: 101, ClientID: id,
		Scope: "jobs.read", AuthTime: oauthTestNow, CreatedAt: oauthTestNow,
		ExpiresAt: oauthTestNow.Add(time.Hour),
	}).Error)
	harness.auditErr = errors.New("audit unavailable")

	_, err = harness.service.UpdateClient(
		t.Context(), 7, id, "Changed", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit, "active",
	)
	assert.ErrorContains(t, err, "audit unavailable")
	clients, err := harness.service.ListClients(t.Context(), 7)
	require.NoError(t, err)
	require.Len(t, clients, 1)
	assert.Equal(t, "Worker", clients[0].Name)

	_, err = harness.service.RotateClientSecret(t.Context(), 7, id)
	assert.ErrorContains(t, err, "audit unavailable")
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"client_credentials"}, "client_id": {client.ID},
		"client_secret": {secret}, "scope": {"jobs.read"},
	}, "")
	require.NoError(t, err, "failed rotation must leave the old secret usable")

	err = harness.service.DeleteClient(t.Context(), 7, id)
	assert.ErrorContains(t, err, "audit unavailable")
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_clients"))
	var revokedAt sql.NullTime
	require.NoError(t, harness.db.Table(harness.table("iam_refresh_tokens")).
		Select("revoked_at").Where("id_hash = ?", hashToken("refresh")).Scan(&revokedAt).Error)
	assert.False(t, revokedAt.Valid, "client deletion and token revocation must roll back together")
}

func TestAuthorizationCodePKCEAndRefreshRotation(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	code := authorizeCode(t, harness, client)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consents"))
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_codes"))

	tokens, err := exchangeCode(t, harness, client, code)
	require.NoError(t, err)
	assert.NotEmpty(t, tokens.AccessToken)
	assert.NotEmpty(t, tokens.RefreshToken)
	assert.NotEmpty(t, tokens.IDToken)
	claims, err := harness.web.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(101), claims.PrincipalID)
	assert.Equal(t, "7", claims.OrganizationID)
	assert.Equal(t, client.ID, claims.ClientID)
	assert.Equal(t, 1, claims.ACR)
	assert.Equal(t, []string{"pwd"}, claims.AMR)

	_, err = exchangeCode(t, harness, client, code)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	rotated, err := harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {tokens.RefreshToken},
	}, "")
	require.NoError(t, err)
	assert.NotEqual(t, tokens.RefreshToken, rotated.RefreshToken)

	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {tokens.RefreshToken},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {rotated.RefreshToken},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest, "replay must revoke the complete refresh family")
}

func TestClientCannotExchangeAnotherClientsCodeOrRefreshToken(t *testing.T) {
	harness := newOAuthHarness(t, "")
	clientA := createBrowserClient(t, harness)
	require.NoError(t, harness.db.Table("iam_tenants").Create(map[string]any{
		"id": 8, "status": "active",
	}).Error)
	require.NoError(t, harness.db.Table("iam_tenant_members").Create(map[string]any{
		"tenant_id": 8, "principal_id": 101, "status": "active",
	}).Error)
	clientB, _, err := harness.service.CreateClient(
		t.Context(), 8, "Tenant B Browser", clientA.RedirectURIs,
		[]string{"authorization_code", "refresh_token"}, []string{"openid", "profile"}, true, ConsentImplicit,
	)
	require.NoError(t, err)

	codeB := authorizeCode(t, harness, clientB)
	_, err = exchangeCode(t, harness, clientA, codeB)
	assert.ErrorIs(t, err, ErrInvalidRequest, "client A must not exchange client B's authorization code")
	tokensB, err := exchangeCode(t, harness, clientB, codeB)
	require.NoError(t, err, "a cross-client exchange attempt must not consume client B's code")

	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientA.ID},
		"refresh_token": {tokensB.RefreshToken},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest, "client A must not exchange client B's refresh token")
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientB.ID},
		"refresh_token": {tokensB.RefreshToken},
	}, "")
	require.NoError(t, err, "a cross-client exchange attempt must not consume client B's refresh token")
}

func TestAuthorizationPersistenceIsAtomic(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_codes")).Error)
	_, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	assert.ErrorContains(t, err, "store authorization code")
	assert.Zero(t, harness.count(t, "iam_oauth_consents"))
	assert.Equal(t, []string{"oauth_client_created"}, harness.eventTypes(t),
		"failed authorization must not leave a consent-success audit")
}

func TestAuthorizationPromptAndMembershipFailures(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	silentLocation, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "none",
	)
	require.NoError(t, err)
	assert.Contains(t, silentLocation, "error=consent_required")
	// prompt=consent asks for the screen, so it parks the request instead of
	// either granting or failing.
	location, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "consent",
	)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(location, defaultConsentPath+"?"), location)
	assert.Zero(t, harness.count(t, "iam_oauth_consents"),
		"parking a request must not record consent before the subject answers")
	_ = authorizeCode(t, harness, client)
	silentLocation, err = harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"silent-state", oauthChallenge(), "S256", "", "none",
	)
	require.NoError(t, err)
	silentURL, err := url.Parse(silentLocation)
	require.NoError(t, err)
	assert.NotEmpty(t, silentURL.Query().Get("code"))
	assert.Empty(t, silentURL.Query().Get("error"))
	assert.Equal(t, "silent-state", silentURL.Query().Get("state"))

	require.NoError(t, harness.db.Table("iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", 7, 101).
		Update("status", "disabled").Error)
	_, err = harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.Authorize(
		t.Context(), "bad-session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestStoredAuthorizationCodeRejectionPaths(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	code := authorizeCode(t, harness, client)
	_, err := harness.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "code": {code},
		"redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {"wrong"},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	require.NoError(t, harness.db.Table("iam_oauth_codes").Where("code_hash = ?", hashToken(code)).
		Update("expires_at", oauthTestNow.Add(-time.Millisecond)).Error)
	_, err = exchangeCode(t, harness, client, code)
	assert.ErrorIs(t, err, ErrInvalidRequest)

	code = authorizeCode(t, harness, client)
	require.NoError(t, harness.db.Table("iam_tenant_members").Where("tenant_id = ? AND principal_id = ?", 7, 101).
		Update("status", "disabled").Error)
	_, err = exchangeCode(t, harness, client, code)
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestAuthorizationCodeAndRefreshAreSingleUseUnderConcurrency(t *testing.T) {
	t.Run("authorization code", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		client := createBrowserClient(t, harness)
		code := authorizeCode(t, harness, client)
		errs := runOAuthConcurrently(8, func() error {
			_, err := exchangeCode(t, harness, client, code)
			return err
		})
		assertSingleOAuthWinner(t, errs)
		assert.Equal(t, int64(1), harness.count(t, "iam_refresh_tokens"))
	})

	t.Run("refresh token", func(t *testing.T) {
		harness := newOAuthHarness(t, "")
		client := createBrowserClient(t, harness)
		tokens, err := exchangeCode(t, harness, client, authorizeCode(t, harness, client))
		require.NoError(t, err)
		var winnerMu sync.Mutex
		winningRefresh := ""
		errs := runOAuthConcurrently(8, func() error {
			response, err := harness.service.Token(t.Context(), url.Values{
				"grant_type": {"refresh_token"}, "client_id": {client.ID},
				"refresh_token": {tokens.RefreshToken},
			}, "")
			if err == nil {
				winnerMu.Lock()
				winningRefresh = response.RefreshToken
				winnerMu.Unlock()
			}
			return err
		})
		assertSingleOAuthWinner(t, errs)
		assert.Equal(t, int64(2), harness.count(t, "iam_refresh_tokens"))
		require.NotEmpty(t, winningRefresh)
		_, err = harness.service.Token(t.Context(), url.Values{
			"grant_type": {"refresh_token"}, "client_id": {client.ID},
			"refresh_token": {winningRefresh},
		}, "")
		assert.ErrorIs(t, err, ErrInvalidRequest, "a CAS loser must revoke the winner's successor family")
	})
}

func runOAuthConcurrently(count int, fn func() error) []error {
	start := make(chan struct{})
	errs := make([]error, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			errs[index] = fn()
		}(index)
	}
	close(start)
	wait.Wait()
	return errs
}

func assertSingleOAuthWinner(t *testing.T, errs []error) {
	t.Helper()
	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
			continue
		}
		assert.ErrorIs(t, err, ErrInvalidRequest)
	}
	assert.Equal(t, 1, winners)
}

func TestIntrospectionIsBoundToTenantAndClient(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	clientA, secretA, err := harness.service.CreateClient(
		t.Context(), 7, "A", nil, []string{"client_credentials"}, []string{"introspect"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	clientB, _, err := harness.service.CreateClient(
		t.Context(), 8, "B", nil, []string{"client_credentials"}, []string{"introspect"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	clientC, _, err := harness.service.CreateClient(
		t.Context(), 7, "C", nil, []string{"client_credentials"}, []string{"introspect"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	tokenA, _, err := harness.web.issueToken(&authnext.Claims{
		Subject: "101", PrincipalID: 101, OrganizationID: "7", ClientID: clientA.ID,
	})
	require.NoError(t, err)
	tokenB, _, err := harness.web.issueToken(&authnext.Claims{
		Subject: "202", PrincipalID: 202, OrganizationID: "8", ClientID: clientB.ID,
	})
	require.NoError(t, err)
	tokenC, _, err := harness.web.issueToken(&authnext.Claims{
		Subject: "303", PrincipalID: 303, OrganizationID: "7", ClientID: clientC.ID,
	})
	require.NoError(t, err)
	wrongTenant, _, err := harness.web.issueToken(&authnext.Claims{
		Subject: "202", PrincipalID: 202, OrganizationID: "8", ClientID: clientA.ID,
	})
	require.NoError(t, err)
	form := url.Values{"client_id": {clientA.ID}, "client_secret": {secretA}, "token": {tokenA}}
	result, err := harness.service.Introspect(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, true, result["active"])
	for _, readOnly := range []bool{true, false} {
		viewToken, _, issueErr := harness.web.issueToken(&authnext.Claims{
			Subject: "101", PrincipalID: 101, OrganizationID: "7", ClientID: clientA.ID,
			AccountView: &authnext.AccountViewIdentity{ID: 50, ActorID: 7, ReadOnly: readOnly, ScopeType: "personal", ScopeID: 101},
		})
		require.NoError(t, issueErr)
		form.Set("token", viewToken)
		result, err = harness.service.Introspect(t.Context(), form, "")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"active": false}, result, "a view cannot become an OAuth credential even with matching client/tenant claims")
	}
	form.Set("token", tokenB)
	result, err = harness.service.Introspect(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, false, result["active"])
	form.Set("token", tokenC)
	result, err = harness.service.Introspect(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, false, result["active"], "same-tenant tokens remain bound to the issuing client")
	form.Set("token", wrongTenant)
	result, err = harness.service.Introspect(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, false, result["active"], "client identity cannot override the token tenant")
	form.Set("token", "invalid")
	result, err = harness.service.Introspect(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, false, result["active"])
	public := createBrowserClient(t, harness)
	_, err = harness.service.Introspect(t.Context(), url.Values{
		"client_id": {public.ID}, "token": {tokenA},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestServiceAccountClientCredentials(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	hash, err := passwordx.Hash("service-secret")
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).Create(map[string]any{
		"id": 202, "login_name": "robot", "status": "active",
		"display_name": "Robot", "created_at": oauthTestNow, "updated_at": oauthTestNow,
	}).Error)
	require.NoError(t, harness.db.Table(harness.table("iam_tenant_members")).Create(map[string]any{
		"tenant_id": 7, "principal_id": 202, "status": "active",
	}).Error)
	require.NoError(t, harness.db.Table(harness.table("iam_service_accounts")).Create(map[string]any{
		"principal_id": 202, "owner_tenant_id": 7, "status": "active", "token_version": 3,
		"created_at": oauthTestNow, "updated_at": oauthTestNow,
	}).Error)
	require.NoError(t, harness.db.Table(harness.table("iam_service_account_credentials")).Create(map[string]any{
		"id": 303, "principal_id": 202, "secret_hash": hash, "scopes": "jobs.read jobs.write",
		"name":       "worker",
		"created_at": oauthTestNow,
	}).Error)
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"303"},
		"client_secret": {"service-secret"}, "scope": {"jobs.read"},
	}
	response, err := harness.service.Token(t.Context(), form, "")
	require.NoError(t, err)
	assert.Equal(t, "jobs.read", response.Scope)
	var lastUsed *time.Time
	require.NoError(t, harness.db.Table(harness.table("iam_service_account_credentials")).
		Select("last_used_at").Where("id = ?", 303).Scan(&lastUsed).Error)
	assert.NotNil(t, lastUsed)
	form.Set("scope", "jobs.delete")
	_, err = harness.service.Token(t.Context(), form, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	require.NoError(t, harness.db.Table(harness.table("iam_service_account_credentials")).
		Where("id = ?", 303).Update("revoked_at", oauthTestNow).Error)
	form.Set("scope", "")
	_, err = harness.service.Token(t.Context(), form, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestRevokeIsClientScopedAndPropagatesStorageFailures(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	tokens, err := exchangeCode(t, harness, client, authorizeCode(t, harness, client))
	require.NoError(t, err)
	other, _, err := harness.service.CreateClient(
		t.Context(), 8, "Other", []string{"https://other.example/callback"},
		[]string{"authorization_code", "refresh_token"}, []string{"openid"}, true, ConsentImplicit,
	)
	require.NoError(t, err)
	otherID, err := guid.Parse(other.ID)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.RefreshToken{
		IDHash: hashToken("other-refresh"), FamilyID: "other-family", PrincipalID: 101, ClientID: otherID,
		Scope: "openid", AuthTime: oauthTestNow, CreatedAt: oauthTestNow, ExpiresAt: oauthTestNow.Add(time.Hour),
	}).Error)
	assert.ErrorIs(t, harness.service.Revoke(t.Context(), url.Values{"client_id": {client.ID}}, ""), ErrInvalidRequest)
	assert.NoError(t, harness.service.Revoke(t.Context(), url.Values{
		"client_id": {client.ID}, "token": {"unknown"},
	}, ""))
	assert.NoError(t, harness.service.Revoke(t.Context(), url.Values{
		"client_id": {client.ID}, "token": {"other-refresh"},
	}, ""))
	var otherRevoked sql.NullTime
	require.NoError(t, harness.db.Model(&model.RefreshToken{}).Select("revoked_at").
		Where("id_hash = ?", hashToken("other-refresh")).Scan(&otherRevoked).Error)
	assert.False(t, otherRevoked.Valid, "one client must not revoke another client's token family")

	require.NoError(t, harness.db.Exec(`CREATE TRIGGER reject_oauth_revoke
		BEFORE UPDATE OF revoked_at ON iam_refresh_tokens
		BEGIN SELECT RAISE(ABORT, 'revoke unavailable'); END`).Error)
	err = harness.service.Revoke(t.Context(), url.Values{
		"client_id": {client.ID}, "token": {tokens.RefreshToken},
	}, "")
	assert.ErrorContains(t, err, "revoke unavailable")
	require.NoError(t, harness.db.Exec("DROP TRIGGER reject_oauth_revoke").Error)
	assert.NoError(t, harness.service.Revoke(t.Context(), url.Values{
		"client_id": {client.ID}, "token": {tokens.RefreshToken},
	}, ""))
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {tokens.RefreshToken},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	assert.ErrorIs(t, harness.service.Revoke(t.Context(), url.Values{"client_id": {"missing"}}, ""), ErrInvalidRequest)
}

func TestOAuthValidationAndProtocolRejectionPaths(t *testing.T) {
	validRedirect := []string{"https://app.example/callback"}
	validScopes := []string{"openid"}
	cases := []struct {
		tenant    guid.ID
		name      string
		redirects []string
		grants    []string
		scopes    []string
		public    bool
		consent   ConsentMode
	}{
		{0, "client", nil, []string{"client_credentials"}, validScopes, false, ConsentImplicit},
		{7, " ", nil, []string{"client_credentials"}, validScopes, false, ConsentImplicit},
		{7, strings.Repeat("x", 129), nil, []string{"client_credentials"}, validScopes, false, ConsentImplicit},
		{7, "client", nil, []string{"password"}, validScopes, false, ConsentImplicit},
		{7, "client", nil, []string{"client_credentials"}, validScopes, true, ConsentImplicit},
		{7, "client", nil, []string{"authorization_code"}, validScopes, true, ConsentImplicit},
		{7, "client", []string{"file:///tmp/callback"}, []string{"authorization_code"}, validScopes, true, ConsentImplicit},
		{7, "client", []string{"https://app.example/callback#fragment"}, []string{"authorization_code"}, validScopes, true, ConsentImplicit},
		{7, "client", validRedirect, []string{"authorization_code"}, []string{"two words"}, true, ConsentImplicit},
		// An unset or unknown consent mode must not fall through to the
		// implicit grant: the column is what decides whether the
		// authorization endpoint may consent on the subject's behalf.
		{7, "client", validRedirect, []string{"authorization_code"}, validScopes, true, ""},
		{7, "client", validRedirect, []string{"authorization_code"}, validScopes, true, "Implicit"},
		{7, "client", validRedirect, []string{"authorization_code"}, validScopes, true, "trusted"},
	}
	for _, testCase := range cases {
		assert.ErrorIs(t, validateClient(
			testCase.tenant, testCase.name, testCase.redirects,
			testCase.grants, testCase.scopes, testCase.public, testCase.consent,
		), ErrInvalidRequest)
	}
	assert.NoError(t, validateClient(7, "client", validRedirect, []string{"authorization_code"}, validScopes, true, ConsentImplicit))
	assert.NoError(t, validateClient(7, "client", validRedirect, []string{"authorization_code"}, validScopes, true, ConsentExplicit))
	assert.Equal(t, "email openid profile", normalizeWords(" profile openid email profile "))
	assert.False(t, allowedRedirect("not-json", validRedirect[0]))
	assert.True(t, allowedScopes("openid profile", "openid"))
	assert.False(t, allowedScopes("openid", "admin"))
	assert.False(t, verifyPKCE("wrong", "verifier"))
	_, _, err := clientCredentials("", "", "Basic invalid")
	assert.Error(t, err)
	_, _, err = clientCredentials("", "", "Basic "+base64.StdEncoding.EncodeToString([]byte("missing-colon")))
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, _, err = clientCredentials("22junk", "secret", "")
	assert.ErrorIs(t, err, ErrInvalidRequest, "client IDs must not rely on database numeric coercion")
	assert.False(t, validPKCEChallenge(strings.Repeat("a", 44)))
	assert.False(t, validPKCEVerifier("a"))
	assert.False(t, validPKCEVerifier(strings.Repeat("a", 42)+"!"))
	assert.False(t, verifyPKCE(pkceChallenge("a"), "a"))

	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	_, err = harness.service.ListClients(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.UpdateClient(t.Context(), 7, 0, "x", nil, []string{"client_credentials"}, validScopes, false, ConsentImplicit, "active")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.RotateClientSecret(t.Context(), 0, 0)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	assert.ErrorIs(t, harness.service.DeleteClient(t.Context(), 0, 0), ErrInvalidRequest)
	_, err = harness.service.Authorize(t.Context(), "session", client.ID, client.RedirectURIs[0], "token", "openid", "", strings.Repeat("a", 43), "S256", "", "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.Authorize(t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid", "", strings.Repeat("a", 44), "S256", "", "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	weakRedirect, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid", "",
		pkceChallenge("a"), "S256", "", "",
	)
	require.NoError(t, err)
	parsedWeakRedirect, err := url.Parse(weakRedirect)
	require.NoError(t, err)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID},
		"code": {parsedWeakRedirect.Query().Get("code")}, "redirect_uri": {client.RedirectURIs[0]},
		"code_verifier": {"a"},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.Token(t.Context(), url.Values{"grant_type": {"unknown"}, "client_id": {client.ID}}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
	_, err = harness.service.Token(t.Context(), url.Values{"grant_type": {"refresh_token"}, "client_id": {client.ID}}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

func TestOAuthIssueAndDatabaseFailuresPropagate(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client, secret, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	require.NoError(t, err)
	harness.web.issue = errors.New("signing failed")
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"client_credentials"}, "client_id": {client.ID},
		"client_secret": {secret}, "scope": {"jobs.read"},
	}, "")
	assert.EqualError(t, err, "signing failed")
	harness.web.issue = nil
	require.NoError(t, harness.db.Exec("DROP TABLE iam_oauth_clients").Error)
	_, err = harness.service.ListClients(t.Context(), 7)
	assert.Error(t, err)
	_, _, err = harness.service.CreateClient(
		t.Context(), 7, "Broken", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	assert.Error(t, err)
}

func TestCreateClientIDFailureDoesNotWrite(t *testing.T) {
	harness := newOAuthHarness(t, "")
	harness.repo.ids = oauthFailingIDs{}
	_, _, err := harness.service.CreateClient(
		t.Context(), 7, "Worker", nil, []string{"client_credentials"}, []string{"jobs.read"}, false, ConsentImplicit,
	)
	assert.EqualError(t, err, "ID source down")
	assert.Zero(t, harness.count(t, "iam_oauth_clients"))
}

func TestOAuthAssuranceAndTokenHelpers(t *testing.T) {
	assurance := oauthAssurance(oauthTestNow, 2, "pwd otp", "client")
	assert.Equal(t, oauthTestNow, assurance.AuthTime)
	assert.Equal(t, 2, assurance.Level)
	assert.Equal(t, []string{"pwd", "otp"}, assurance.Methods)
	assert.Equal(t, "client", assurance.ClientID)
	assert.Len(t, hashToken("value"), 64)
	token, err := secureToken(32)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, oauthTestNow, oauthTime(oauthTestNow.Add(456*time.Microsecond)))
	assert.True(t, containsAllWords("openid profile email", "profile openid"))
	assert.False(t, containsAllWords("openid", "openid email"))
}

func TestExpiredRefreshTokenIsRejected(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	id, err := guid.Parse(client.ID)
	require.NoError(t, err)
	row := model.RefreshToken{
		IDHash: hashToken("expired"), FamilyID: "family", PrincipalID: 101, ClientID: id,
		Scope: "openid", AuthTime: oauthTestNow, CreatedAt: oauthTestNow.Add(-time.Hour),
		ExpiresAt: oauthTestNow.Add(-time.Millisecond),
	}
	require.NoError(t, harness.db.Table("iam_refresh_tokens").Create(&row).Error)
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {"expired"},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest)
}

// TestAuthorizationRejectsUnknownPromptValues pins the switch as closed: the
// three documented prompt values keep their behavior, and anything else fails
// closed instead of inheriting the implicit grant taken by an absent prompt.
// The HTTP layer already restricts prompt to the documented set, so this guards
// a future value added there.
func TestAuthorizationRejectsUnknownPromptValues(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)

	for _, prompt := range []string{"login", "select_account", "Consent", "none consent", " "} {
		_, err := harness.service.Authorize(
			t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
			"", oauthChallenge(), "S256", "", prompt,
		)
		assert.ErrorIs(t, err, ErrInvalidRequest, "prompt=%q must not grant consent implicitly", prompt)
	}
	assert.Zero(t, harness.count(t, "iam_oauth_consents"),
		"a rejected authorization must not record consent")
	assert.Zero(t, harness.count(t, "iam_oauth_consent_requests"),
		"a rejected authorization must not park a request either")

	// An absent prompt still grants without the screen for a client whose
	// consent mode is implicit.
	redirect, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	require.NoError(t, err)
	assert.NotEmpty(t, redirect)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consents"))
}

// TestRevokingAConsentEndsTheAccessItAlreadyGranted is the whole point of the
// operation. Deleting the consent row alone would only stop the application
// being granted access again: the refresh token it already holds keeps working,
// so somebody who just clicked "revoke" would stay signed in to it for as long
// as that token rotates.
func TestRevokingAConsentEndsTheAccessItAlreadyGranted(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	tokens, err := exchangeCode(t, harness, client, authorizeCode(t, harness, client))
	require.NoError(t, err)
	require.NotEmpty(t, tokens.RefreshToken)

	listed, err := harness.service.ListConsents(t.Context(), 101)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, client.ID, listed[0].ClientID)
	assert.Equal(t, "7", listed[0].TenantID)
	assert.Equal(t, "Browser", listed[0].ClientName, "a list somebody acts on has to name the application")
	assert.Equal(t, []string{"openid", "profile"}, listed[0].Scopes)
	assert.Equal(t, oauthTestNow, listed[0].GrantedAt.UTC())

	require.NoError(t, harness.service.RevokeConsent(t.Context(), 101, clientID))

	assert.Zero(t, harness.count(t, "iam_oauth_consents"))
	_, err = harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {tokens.RefreshToken},
	}, "")
	assert.ErrorIs(t, err, ErrInvalidRequest, "the token the revoked grant issued must stop working")
	assert.Contains(t, harness.eventTypes(t), "oauth_consent_revoked")
}

// TestRevokingAConsentKillsACodeThatWasAlreadyIssued closes the window between
// an authorization and its exchange. A code handed out seconds before the
// revocation is a fresh refresh token the moment it is redeemed, so ignoring it
// would let the grant come back on its own a minute after somebody ended it.
func TestRevokingAConsentKillsACodeThatWasAlreadyIssued(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	code := authorizeCode(t, harness, client)

	require.NoError(t, harness.service.RevokeConsent(t.Context(), 101, clientID))

	_, err = exchangeCode(t, harness, client, code)
	assert.ErrorIs(t, err, ErrInvalidRequest, "a code outliving its consent would restore the grant")
}

// TestConsentListingAndRevocationStayWithTheirOwnSubject keeps a self-service
// operation self-service. The principal is the only thing that scopes it, so a
// mistake here is one person ending or reading somebody else's grants.
func TestConsentListingAndRevocationStayWithTheirOwnSubject(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	_, err = exchangeCode(t, harness, client, authorizeCode(t, harness, client))
	require.NoError(t, err)

	stranger, err := harness.service.ListConsents(t.Context(), 102)
	require.NoError(t, err)
	assert.NotNil(t, stranger, "an empty list has to serialize as an array, not as null")
	assert.Empty(t, stranger)
	assert.ErrorIs(t, harness.service.RevokeConsent(t.Context(), 102, clientID), ErrInvalidRequest)
	assert.EqualValues(t, 1, harness.count(t, "iam_oauth_consents"), "the owner's grant survives")

	// A grant that is already gone reads the same as one that never existed, so
	// a double click reports a mistyped client rather than a server fault.
	require.NoError(t, harness.service.RevokeConsent(t.Context(), 101, clientID))
	assert.ErrorIs(t, harness.service.RevokeConsent(t.Context(), 101, clientID), ErrInvalidRequest)
}

// TestConsentSelfServiceRefusesAnUnnamedSubjectOrApplication keeps a zero ID
// from reaching a query where it would match a row rather than nothing.
func TestConsentSelfServiceRefusesAnUnnamedSubjectOrApplication(t *testing.T) {
	harness := newOAuthHarness(t, "")
	_, err := harness.service.ListConsents(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	assert.ErrorIs(t, harness.service.RevokeConsent(t.Context(), 0, 1001), ErrInvalidRequest)
	assert.ErrorIs(t, harness.service.RevokeConsent(t.Context(), 101, 0), ErrInvalidRequest)
}

// TestARevocationThatCannotBeAuditedChangesNothing keeps the trail and the
// effect in one transaction. A revocation nobody can prove happened is worse
// than one that failed, because the subject was told it worked.
func TestARevocationThatCannotBeAuditedChangesNothing(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	tokens, err := exchangeCode(t, harness, client, authorizeCode(t, harness, client))
	require.NoError(t, err)

	harness.auditErr = errors.New("audit sink down")
	require.Error(t, harness.service.RevokeConsent(t.Context(), 101, clientID))
	harness.auditErr = nil

	assert.EqualValues(t, 1, harness.count(t, "iam_oauth_consents"))
	rotated, err := harness.service.Token(t.Context(), url.Values{
		"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {tokens.RefreshToken},
	}, "")
	require.NoError(t, err, "a rolled back revocation must not have revoked the token")
	assert.NotEmpty(t, rotated.RefreshToken)
}

func TestConsentModeDecidesWhetherAuthorizationGrantsImplicitly(t *testing.T) {
	harness := newOAuthHarness(t, "")
	implicitClient := createBrowserClient(t, harness)
	explicitClient, _, err := harness.service.CreateClient(
		t.Context(), 7, "Third Party", []string{"https://third.example/callback"},
		[]string{"authorization_code", "refresh_token"}, []string{"openid", "profile"}, true, ConsentExplicit,
	)
	require.NoError(t, err)
	assert.Equal(t, ConsentExplicit, explicitClient.ConsentMode)
	assert.Equal(t, ConsentImplicit, implicitClient.ConsentMode)

	// The explicit client has no stored consent covering the scope, so the
	// request is parked for the screen rather than granted.
	location, err := harness.service.Authorize(
		t.Context(), "session", explicitClient.ID, explicitClient.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(location, defaultConsentPath+"?"), location)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consent_requests"))
	assert.Zero(t, harness.count(t, "iam_oauth_consents"),
		"a parked authorization must not record consent")

	// The implicit client is unaffected: it still consents on the subject's
	// behalf, which is what every client did before the column existed.
	redirect, err := harness.service.Authorize(
		t.Context(), "session", implicitClient.ID, implicitClient.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	require.NoError(t, err)
	assert.NotEmpty(t, redirect)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consents"))
}

func TestConsentModeSurvivesUpdateAndIsAudited(t *testing.T) {
	harness := newOAuthHarness(t, "")
	client := createBrowserClient(t, harness)
	id, err := guid.Parse(client.ID)
	require.NoError(t, err)

	updated, err := harness.service.UpdateClient(
		t.Context(), 7, id, "Browser", client.RedirectURIs,
		[]string{"authorization_code"}, []string{"openid"}, true, ConsentExplicit, "active",
	)
	require.NoError(t, err)
	assert.Equal(t, ConsentExplicit, updated.ConsentMode)

	// The stored column is what Authorize reads, so the update must reach the
	// row rather than only the returned value.
	listed, err := harness.service.ListClients(t.Context(), 7)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, ConsentExplicit, listed[0].ConsentMode)

	location, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "",
	)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(location, defaultConsentPath+"?"), location)

	assert.Equal(t, []string{"oauth_client_created", "oauth_client_updated"}, harness.eventTypes(t))
}

// TestConsentParkingAndResolutionFailAtomically checks the two places a
// consent detour can fail on storage: parking the request, and recording the
// refusal. Neither may half-commit — a lost audit record for a refusal would
// leave a decision nobody can account for.
func TestConsentParkingAndResolutionFailAtomically(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	authorize := func() (string, error) {
		return harness.service.Authorize(
			t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
			"state-1", oauthChallenge(), "S256", "", "consent",
		)
	}

	location, err := authorize()
	require.NoError(t, err)
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	challenge := parsed.Query().Get("consent_challenge")
	require.NotEmpty(t, challenge)

	// A refusal that cannot be audited must not burn the challenge, or the
	// subject would be left with a request they can no longer answer.
	harness.auditErr = errors.New("audit unavailable")
	_, err = harness.service.ResolveConsent(t.Context(), "session", challenge, false)
	require.Error(t, err)
	assert.Equal(t, int64(1), harness.count(t, "iam_oauth_consent_requests"))
	var resolved sql.NullTime
	require.NoError(t, harness.db.Table(harness.table("iam_oauth_consent_requests")).
		Pluck("resolved_at", &resolved).Error)
	assert.False(t, resolved.Valid, "a failed decision must leave the request answerable")

	harness.auditErr = nil
	denied, err := harness.service.ResolveConsent(t.Context(), "session", challenge, false)
	require.NoError(t, err)
	assert.Contains(t, denied, "error=access_denied")

	// Parking is the other failure point: without somewhere to record the
	// request, authorization must fail rather than fall back to granting.
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_consent_requests")).Error)
	_, err = authorize()
	require.Error(t, err)
	assert.Zero(t, harness.count(t, "iam_oauth_codes"))
}

// TestParkedConsentIsRecheckedAgainstTheClientAsItStandsNow covers the window
// between parking a request and answering it: the client, the subject's
// membership, and the storage backing the request can all change while the
// screen is on someone's display.
func TestParkedConsentIsRecheckedAgainstTheClientAsItStandsNow(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	park := func(t *testing.T) string {
		t.Helper()
		location, err := harness.service.Authorize(
			t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
			"", oauthChallenge(), "S256", "", "consent",
		)
		require.NoError(t, err)
		parsed, err := url.Parse(location)
		require.NoError(t, err)
		return parsed.Query().Get("consent_challenge")
	}
	clients := harness.db.Table(harness.table("iam_oauth_clients"))

	// A client narrowed to scopes it no longer holds must not be granted the
	// wider scope the subject is looking at.
	narrowed := park(t)
	require.NoError(t, clients.Where("1 = 1").Update("scopes", "jobs.read").Error)
	_, err := harness.service.ResolveConsent(t.Context(), "session", narrowed, true)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	require.NoError(t, clients.Where("1 = 1").Update("scopes", "openid profile").Error)

	// Neither may a subject whose membership ended while they were deciding.
	ended := park(t)
	members := harness.db.Table(harness.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", 7, 101)
	require.NoError(t, members.Update("status", "disabled").Error)
	_, err = harness.service.ResolveConsent(t.Context(), "session", ended, true)
	assert.ErrorIs(t, err, ErrInvalidRequest)
	require.NoError(t, members.Update("status", "active").Error)

	assert.Zero(t, harness.count(t, "iam_oauth_codes"))

	// A storage fault reading the parked request is reported as such rather
	// than folded into the uniform "bad challenge" answer.
	live := park(t)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_oauth_consent_requests")).Error)
	_, err = harness.service.PendingConsent(t.Context(), "session", live)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidRequest)
}

// failReads makes every read of one table fail, leaving its rows in place:
// dropping the table instead would cascade into the rows under test.
func failReads(t *testing.T, harness *oauthHarness, table string) {
	t.Helper()
	name := harness.table(table)
	require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register("test:fail_reads", func(db *gorm.DB) {
		if db.Statement.Table == name {
			db.AddError(errors.New("storage unavailable"))
		}
	}))
}

// TestStorageFaultsAreNotReportedAsRefusals covers every read the grant flows
// make after the request itself has been validated. A missing row is a refusal
// (ErrInvalidRequest, or a demand to log in); a failed read is not, and must
// reach the protocol's server_error path. A client told invalid_grant discards
// a refresh token it would otherwise have retried.
func TestStorageFaultsAreNotReportedAsRefusals(t *testing.T) {
	requireFault := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidRequest)
		assert.NotErrorIs(t, err, authnext.ErrInvalidCredentials)
	}
	authorize := func(harness *oauthHarness, client Client, prompt string) (string, error) {
		return harness.service.Authorize(
			t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
			"", oauthChallenge(), "S256", "", prompt,
		)
	}
	park := func(t *testing.T, harness *oauthHarness, client Client) string {
		t.Helper()
		location, err := authorize(harness, client, "consent")
		require.NoError(t, err)
		parsed, err := url.Parse(location)
		require.NoError(t, err)
		return parsed.Query().Get("consent_challenge")
	}
	refresh := func(harness *oauthHarness, client Client, token string) error {
		_, err := harness.service.Token(t.Context(), url.Values{
			"grant_type": {"refresh_token"}, "client_id": {client.ID}, "refresh_token": {token},
		}, "")
		return err
	}
	issue := func(t *testing.T, harness *oauthHarness, client Client) TokenResponse {
		t.Helper()
		tokens, err := exchangeCode(t, harness, client, authorizeCode(t, harness, client))
		require.NoError(t, err)
		return tokens
	}

	for name, run := range map[string]func(t *testing.T, harness *oauthHarness, client Client) error{
		"authorize client": func(t *testing.T, harness *oauthHarness, client Client) error {
			failReads(t, harness, "iam_oauth_clients")
			_, err := authorize(harness, client, "")
			return err
		},
		"authorize membership": func(t *testing.T, harness *oauthHarness, client Client) error {
			failReads(t, harness, "iam_tenant_members")
			_, err := authorize(harness, client, "")
			return err
		},
		"consent authentication": func(t *testing.T, harness *oauthHarness, client Client) error {
			challenge := park(t, harness, client)
			harness.web.authenticateError = authnext.ErrUnavailable
			_, err := harness.service.PendingConsent(t.Context(), "session", challenge)
			return err
		},
		"consent client": func(t *testing.T, harness *oauthHarness, client Client) error {
			challenge := park(t, harness, client)
			failReads(t, harness, "iam_oauth_clients")
			_, err := harness.service.PendingConsent(t.Context(), "session", challenge)
			return err
		},
		"consent membership": func(t *testing.T, harness *oauthHarness, client Client) error {
			challenge := park(t, harness, client)
			failReads(t, harness, "iam_tenant_members")
			_, err := harness.service.PendingConsent(t.Context(), "session", challenge)
			return err
		},
		"authorization code": func(t *testing.T, harness *oauthHarness, client Client) error {
			code := authorizeCode(t, harness, client)
			failReads(t, harness, "iam_oauth_codes")
			_, err := exchangeCode(t, harness, client, code)
			return err
		},
		"authorization code membership": func(t *testing.T, harness *oauthHarness, client Client) error {
			code := authorizeCode(t, harness, client)
			failReads(t, harness, "iam_tenant_members")
			_, err := exchangeCode(t, harness, client, code)
			return err
		},
		"refresh token": func(t *testing.T, harness *oauthHarness, client Client) error {
			tokens := issue(t, harness, client)
			failReads(t, harness, "iam_refresh_tokens")
			return refresh(harness, client, tokens.RefreshToken)
		},
		"refresh token membership": func(t *testing.T, harness *oauthHarness, client Client) error {
			tokens := issue(t, harness, client)
			failReads(t, harness, "iam_tenant_members")
			return refresh(harness, client, tokens.RefreshToken)
		},
	} {
		t.Run(name, func(t *testing.T) {
			harness := newOAuthHarness(t, "sl_")
			requireFault(t, run(t, harness, createBrowserClient(t, harness)))
		})
	}
}
