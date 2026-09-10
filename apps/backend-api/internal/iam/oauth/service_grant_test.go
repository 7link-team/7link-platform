package oauth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Only the initiating browser is a fixture; all issued JWTs pass through the
// production signer, parser, principal checks and application grant validator.
type grantWeb struct {
	*authn.Service
	browser *authnext.Claims
}

func (web *grantWeb) Authenticate(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if authorization == "" && cookie == "session" {
		return web.browser, nil
	}
	return web.Service.Authenticate(ctx, authorization, cookie)
}

func newGrantHarness(t *testing.T) (*oauthHarness, *authn.Service) {
	t.Helper()
	h := newOAuthHarness(t, "")
	db, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, goosex.Migrate(t.Context(), db, (&authn.Module{}).MigrationSource(), "sqlite"))
	appender := audit.Appender(func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	authenticator, err := authn.NewWebService(authnext.Config{
		Enabled: true, Issuer: "https://issuer.example", Audience: []string{"api"},
		SigningKey:     base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		AccessTokenTTL: time.Hour,
	}, authn.NewRepository(h.runtime, newOAuthTestIDs(9000), appender), authn.WithOAuthGrantValidator(h.service.ValidateAccessGrant))
	require.NoError(t, err)
	h.service.authn = &grantWeb{Service: authenticator, browser: h.web.session}
	return h, authenticator
}

func grantClient(t *testing.T, h *oauthHarness, name string) (Client, string) {
	t.Helper()
	client, secret, err := h.service.CreateClient(t.Context(), 7, name, []string{"https://client.example/callback"},
		[]string{"authorization_code", "refresh_token"}, []string{"openid"}, false, ConsentImplicit)
	require.NoError(t, err)
	return client, secret
}

func grantCode(t *testing.T, h *oauthHarness, client Client) string {
	t.Helper()
	location, err := h.service.Authorize(t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid", "", oauthChallenge(), "S256", "", "")
	require.NoError(t, err)
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	return parsed.Query().Get("code")
}

func grantTokens(t *testing.T, h *oauthHarness, client Client, secret string) TokenResponse {
	t.Helper()
	tokens, err := h.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "client_secret": {secret},
		"code": {grantCode(t, h, client)}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
	}, "")
	require.NoError(t, err)
	return tokens
}

func TestGrantWithdrawalReachesRealBearerResourcesAndCannotRevive(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "First")
	other, otherSecret := grantClient(t, h, "Other")
	first, unaffected := grantTokens(t, h, client, secret), grantTokens(t, h, other, otherSecret)
	claims, err := authenticator.Authenticate(t.Context(), "Bearer "+first.AccessToken, "")
	require.NoError(t, err)
	require.NotEmpty(t, claims.OAuthGrantID)
	router, api := humatest.New(t)
	authn.RegisterHTTP(api, authenticator, authenticator)
	require.Equal(t, http.StatusOK, grantResourceStatus(router, first.AccessToken))
	clientID, err := guid.Parse(client.ID)
	require.NoError(t, err)
	h.auditErr = assert.AnError
	require.Error(t, h.service.RevokeConsent(t.Context(), 101, clientID))
	h.auditErr = nil
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+first.AccessToken, "")
	require.NoError(t, err, "audit rollback must preserve the original grant")
	require.NoError(t, h.service.RevokeConsent(t.Context(), 101, clientID))
	require.Equal(t, http.StatusUnauthorized, grantResourceStatus(router, first.AccessToken))
	result, err := h.service.Introspect(t.Context(), url.Values{"client_id": {client.ID}, "client_secret": {secret}, "token": {first.AccessToken}}, "")
	require.NoError(t, err)
	assert.Equal(t, false, result["active"])
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+unaffected.AccessToken, "")
	require.NoError(t, err, "withdrawing one application must not revoke another")
	replacement := grantTokens(t, h, client, secret) // h.now has not advanced even one millisecond.
	newClaims, err := authenticator.Authenticate(t.Context(), "Bearer "+replacement.AccessToken, "")
	require.NoError(t, err)
	assert.NotEqual(t, claims.OAuthGrantID, newClaims.OAuthGrantID)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+first.AccessToken, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	legacy, _, err := authenticator.IssueTenantAccessTokenWithAssurance(t.Context(), 101, "7", "", "openid", authnext.Assurance{ClientID: client.ID})
	require.NoError(t, err)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+legacy, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	ordinary, _, err := authenticator.IssueTenantAccessTokenWithAssurance(t.Context(), 101, "7", "", "openid", authnext.Assurance{})
	require.NoError(t, err)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+ordinary, "")
	require.NoError(t, err, "non-OAuth principal tokens retain their existing contract")
}

// Use the real router without humatest's request dump, which includes secrets.
func grantResourceStatus(router http.Handler, token string) int {
	request := httptest.NewRequest(http.MethodGet, "/authn/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code
}

func TestGrantBearerRejectsDisabledAndDeletedApplications(t *testing.T) {
	for _, state := range []string{"disabled", "deleted"} {
		t.Run(state, func(t *testing.T) {
			h, authenticator := newGrantHarness(t)
			client, secret := grantClient(t, h, "Application")
			tokens := grantTokens(t, h, client, secret)
			router, api := humatest.New(t)
			authn.RegisterHTTP(api, authenticator, authenticator)
			require.Equal(t, http.StatusOK, grantResourceStatus(router, tokens.AccessToken))
			query := h.db.Model(&model.Client{}).Where("id = ?", client.ID)
			if state == "deleted" {
				require.NoError(t, query.Delete(&model.Client{}).Error)
			} else {
				require.NoError(t, query.Update("status", "disabled").Error)
			}
			assert.Equal(t, http.StatusUnauthorized, grantResourceStatus(router, tokens.AccessToken))
		})
	}
}

func TestLegacyAndWithdrawnCodeAndRefreshFailClosed(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		t.Run(map[bool]string{true: "legacy", false: "withdrawn and regranted"}[legacy], func(t *testing.T) {
			h, _ := newGrantHarness(t)
			client, secret := grantClient(t, h, "Application")
			tokens := grantTokens(t, h, client, secret)
			code := grantCode(t, h, client)
			if legacy {
				require.NoError(t, h.db.Model(&model.AuthorizationCode{}).Where("code_hash = ?", hashToken(code)).Update("grant_id", "").Error)
				require.NoError(t, h.db.Model(&model.RefreshToken{}).Where("id_hash = ?", hashToken(tokens.RefreshToken)).Update("grant_id", "").Error)
			} else {
				clientID, err := guid.Parse(client.ID)
				require.NoError(t, err)
				require.NoError(t, h.service.RevokeConsent(t.Context(), 101, clientID))
				grantCode(t, h, client)
			}
			for _, form := range []url.Values{
				{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier}},
				{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}},
			} {
				form.Set("client_id", client.ID)
				form.Set("client_secret", secret)
				response, err := h.service.Token(t.Context(), form, "")
				assert.ErrorIs(t, err, ErrInvalidRequest)
				assert.Empty(t, response.AccessToken)
			}
		})
	}
}

type pausedGrantWeb struct {
	authn.WebService
	signed chan struct{}
	resume chan struct{}
}

func (web *pausedGrantWeb) IssueTenantAccessTokenWithAssurance(ctx context.Context, principalID guid.ID, tenantID, audience, scope string, assurance authnext.Assurance) (string, int64, error) {
	token, expires, err := web.WebService.IssueTenantAccessTokenWithAssurance(ctx, principalID, tenantID, audience, scope, assurance)
	close(web.signed)
	select {
	case <-web.resume:
		return token, expires, err
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

func TestGrantWithdrawalWinsAgainstInFlightCodeAndRefresh(t *testing.T) {
	for _, flow := range []string{"authorization_code", "refresh_token"} {
		t.Run(flow, func(t *testing.T) {
			h, authenticator := newGrantHarness(t)
			db, err := h.db.DB()
			require.NoError(t, err)
			db.SetMaxOpenConns(4)
			client, secret := grantClient(t, h, "Application")
			initial := grantTokens(t, h, client, secret)
			code := grantCode(t, h, client)
			peer := NewService(h.runtime, h.service.authn, newOAuthTestIDs(20000), h.repo.appender, h.service.consent)
			peer.now = h.service.now
			paused := &pausedGrantWeb{WebService: h.service.authn, signed: make(chan struct{}), resume: make(chan struct{})}
			h.service.authn = paused
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				response, tokenErr := h.service.Token(ctx, url.Values{
					"grant_type": {flow}, "client_id": {client.ID}, "client_secret": {secret},
					"code": {code}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
					"refresh_token": {initial.RefreshToken},
				}, "")
				if response.AccessToken != "" {
					done <- assert.AnError
					return
				}
				done <- tokenErr
			}()
			select {
			case <-paused.signed:
			case <-ctx.Done():
				t.Fatal("token issuance did not reach signing barrier")
			}
			clientID, err := guid.Parse(client.ID)
			require.NoError(t, err)
			require.NoError(t, peer.RevokeConsent(ctx, 101, clientID))
			_, err = peer.Authorize(ctx, "session", client.ID, client.RedirectURIs[0], "code", "openid", "", oauthChallenge(), "S256", "", "")
			require.NoError(t, err)
			close(paused.resume)
			assert.ErrorIs(t, <-done, ErrInvalidRequest)
			_, err = authenticator.Authenticate(ctx, "Bearer "+initial.AccessToken, "")
			assert.ErrorIs(t, err, authnext.ErrInvalidToken)
		})
	}
}

func TestGrantValidatorRejectsMalformedIdentityAndFailsClosedOnStorage(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "Application")
	tokens := grantTokens(t, h, client, secret)
	claims, err := authenticator.Authenticate(t.Context(), "Bearer "+tokens.AccessToken, "")
	require.NoError(t, err)
	for _, invalid := range []*authnext.Claims{
		nil, {}, {PrincipalID: 101, ClientID: "invalid"},
		{PrincipalID: 101, ClientID: client.ID, OrganizationID: "invalid"},
		{PrincipalID: 101, ClientID: client.ID, OrganizationID: "8", OAuthGrantID: claims.OAuthGrantID},
	} {
		assert.ErrorIs(t, h.service.ValidateAccessGrant(t.Context(), invalid), authnext.ErrInvalidToken)
	}
	for _, table := range []string{"iam_oauth_consents", "iam_oauth_clients"} {
		t.Run(table, func(t *testing.T) {
			require.NoError(t, h.db.Migrator().RenameTable(table, table+"_unavailable"))
			assert.ErrorIs(t, h.service.ValidateAccessGrant(t.Context(), claims), authnext.ErrUnavailable)
			require.NoError(t, h.db.Migrator().RenameTable(table+"_unavailable", table))
		})
	}
	closed := &Service{repo: &Repository{}}
	assert.ErrorIs(t, closed.ValidateAccessGrant(t.Context(), claims), authnext.ErrUnavailable)
}

func TestLegacyConsentGetsNewGrantWithoutAdoptingOldTokens(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "Application")
	old := grantTokens(t, h, client, secret)
	var before model.Consent
	require.NoError(t, h.db.First(&before).Error)
	grantCode(t, h, client)
	var repeated model.Consent
	require.NoError(t, h.db.First(&repeated).Error)
	assert.Equal(t, before.GrantID, repeated.GrantID, "continued authorization preserves the same lifetime")
	require.NoError(t, h.db.Model(&model.Consent{}).Where("client_id = ?", client.ID).Update("grant_id", "").Error)
	replacement := grantTokens(t, h, client, secret)
	newClaims, err := authenticator.Authenticate(t.Context(), "Bearer "+replacement.AccessToken, "")
	require.NoError(t, err)
	assert.NotEmpty(t, newClaims.OAuthGrantID)
	assert.NotEqual(t, before.GrantID, newClaims.OAuthGrantID)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+old.AccessToken, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken)
}

func TestGrantCompletionRejectsOldIncarnationEvenWithUnconsumedRows(t *testing.T) {
	h, _ := newGrantHarness(t)
	client, secret := grantClient(t, h, "Application")
	tokens := grantTokens(t, h, client, secret)
	code := grantCode(t, h, client)
	var refresh model.RefreshToken
	require.NoError(t, h.db.Where("id_hash = ?", hashToken(tokens.RefreshToken)).Take(&refresh).Error)
	// A stale row's availability is insufficient: completion must compare its
	// immutable incarnation against the locked current consent independently.
	require.NoError(t, h.db.Model(&model.Consent{}).Where("client_id = ?", client.ID).Update("grant_id", "different-incarnation").Error)
	assert.ErrorIs(t, h.repo.consumeAuthorizationCode(t.Context(), hashToken(code), oauthTestNow, nil), ErrInvalidRequest)
	next := refresh
	next.IDHash = hashToken("successor")
	assert.ErrorIs(t, h.repo.rotateRefreshToken(t.Context(), refresh.IDHash, refresh.FamilyID, oauthTestNow, &next), ErrInvalidRequest)
	var unchanged model.AuthorizationCode
	require.NoError(t, h.db.Where("code_hash = ?", hashToken(code)).Take(&unchanged).Error)
	assert.Nil(t, unchanged.ConsumedAt)
	var unchangedRefresh model.RefreshToken
	require.NoError(t, h.db.Where("id_hash = ?", refresh.IDHash).Take(&unchangedRefresh).Error)
	assert.Nil(t, unchangedRefresh.UsedAt)
}
