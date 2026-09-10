package oauth

import (
	"net/url"
	"testing"

	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGlobalClosureRevokesPendingRequestsCodesAndAllTenantGrants(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "First tenant")
	first := grantTokens(t, h, client, secret)
	unusedCode := grantCode(t, h, client)
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenants (id,status) VALUES (8,'active')").Error)
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (8,101,'active')").Error)
	other, otherSecret, err := h.service.CreateClient(t.Context(), 8, "Second tenant", []string{"https://app.example/callback"}, []string{"authorization_code", "refresh_token"}, []string{"openid"}, false, ConsentImplicit)
	require.NoError(t, err)
	second := grantTokens(t, h, other, otherSecret)
	location, err := h.service.Authorize(t.Context(), "session", other.ID, other.RedirectURIs[0], "code", "openid", "", oauthChallenge(), "S256", "", "consent")
	require.NoError(t, err)
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	challenge := parsed.Query().Get("consent_challenge")
	require.NotEmpty(t, challenge)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.service.revokePrincipalGrantsOn(t.Context(), tx, 101, 0)
	}))
	for _, token := range []string{first.AccessToken, second.AccessToken} {
		_, err = authenticator.Authenticate(t.Context(), "Bearer "+token, "")
		require.Error(t, err)
	}
	_, err = h.service.PendingConsent(t.Context(), "session", challenge)
	require.Error(t, err)
	_, err = h.service.Token(t.Context(), url.Values{
		"grant_type": {"authorization_code"}, "client_id": {client.ID}, "client_secret": {secret},
		"code": {unusedCode}, "redirect_uri": {client.RedirectURIs[0]}, "code_verifier": {oauthVerifier},
	}, "")
	require.Error(t, err)
	for _, table := range []string{"iam_oauth_consents", "iam_refresh_tokens"} {
		var count int64
		query := h.db.Table(table).Where("principal_id=101")
		if table == "iam_refresh_tokens" {
			query = query.Where("revoked_at IS NULL")
		}
		require.NoError(t, query.Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestTenantExitRevokesOnlyTenantGrantIncarnations(t *testing.T) {
	h, authenticator := newGrantHarness(t)
	client, secret := grantClient(t, h, "Departing tenant")
	first := grantTokens(t, h, client, secret)
	// Include a client without any consent: it must not block leaving.
	_, _ = grantClient(t, h, "No consent")
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenants (id,status) VALUES (8,'active')").Error)
	require.NoError(t, h.db.Exec("INSERT INTO iam_tenant_members (tenant_id,principal_id,status) VALUES (8,101,'active')").Error)
	other, otherSecret, err := h.service.CreateClient(t.Context(), 8, "Other tenant", []string{"https://app.example/callback"}, []string{"authorization_code", "refresh_token"}, []string{"openid", "profile"}, false, ConsentImplicit)
	require.NoError(t, err)
	unaffected := grantTokens(t, h, other, otherSecret)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("iam_tenant_members").Where("tenant_id=7 AND principal_id=101").Update("status", "disabled").Error; err != nil {
			return err
		}
		return h.service.revokeTenantGrantsOn(t.Context(), tx, 7, 101)
	}))
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+first.AccessToken, "")
	require.Error(t, err)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+unaffected.AccessToken, "")
	require.NoError(t, err)
	require.NoError(t, h.db.Table("iam_tenant_members").Where("tenant_id=7 AND principal_id=101").Update("status", "active").Error)
	fresh := grantTokens(t, h, client, secret)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+fresh.AccessToken, "")
	require.NoError(t, err)
	_, err = authenticator.Authenticate(t.Context(), "Bearer "+first.AccessToken, "")
	require.Error(t, err, "rejoin and a fresh grant must not revive old access")
	var count int64
	require.NoError(t, h.db.Table("iam_refresh_tokens").Where("principal_id=101 AND client_id=? AND revoked_at IS NULL", client.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "only the new grant has an active refresh token")
}

func TestLateAuthorizationCannotCreateGrantAfterExitOrClosure(t *testing.T) {
	h, _ := newGrantHarness(t)
	client, _ := grantClient(t, h, "Delayed application")
	claims, err := h.service.authn.Authenticate(t.Context(), "", "session")
	require.NoError(t, err)
	id, err := guid.Parse(client.ID)
	require.NoError(t, err)
	row, err := h.repo.activeClient(t.Context(), id)
	require.NoError(t, err)
	issue := func() error {
		_, err := h.service.issueAuthorizationCode(t.Context(), h.repo, claims, row, client.RedirectURIs[0], "openid profile", "state", oauthChallenge(), "nonce")
		return err
	}
	park := func() error {
		_, err := h.service.startConsent(t.Context(), h.repo, row, claims, client.RedirectURIs[0], "openid profile", "state", oauthChallenge(), "nonce")
		return err
	}
	// Model a request paused after its initial authentication and admission read.
	require.NoError(t, h.db.Table("iam_tenant_members").Where("tenant_id=7 AND principal_id=101").Update("status", "disabled").Error)
	require.ErrorIs(t, issue(), ErrInvalidRequest)
	require.ErrorIs(t, park(), ErrInvalidRequest)
	require.NoError(t, h.db.Table("iam_tenant_members").Where("tenant_id=7 AND principal_id=101").Update("status", "active").Error)
	require.NoError(t, h.db.Table("iam_principals").Where("id=101").Update("status", "disabled").Error)
	require.ErrorIs(t, issue(), ErrInvalidRequest)
	require.ErrorIs(t, park(), ErrInvalidRequest)
	// Processing increments even federated-only principals from version zero.
	require.NoError(t, h.db.Table("iam_credentials").Create(&authnmodel.Credential{PrincipalID: 101, CredentialVersion: 1, PasswordChangedAt: oauthTestNow, UpdatedAt: oauthTestNow}).Error)
	require.NoError(t, h.db.Table("iam_principals").Where("id=101").Update("status", "active").Error)
	require.ErrorIs(t, issue(), ErrInvalidRequest, "restoration cannot authorize captured pre-closure claims")
	require.ErrorIs(t, park(), ErrInvalidRequest, "restoration cannot park captured pre-closure requests")
	var count int64
	require.NoError(t, h.db.Table("iam_oauth_consents").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, h.db.Table("iam_oauth_consent_requests").Count(&count).Error)
	require.Zero(t, count)
}
