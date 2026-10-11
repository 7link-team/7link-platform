package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// socialProvider is a local stand-in for a social provider: a token endpoint
// and a profile endpoint, nothing signed. It is reached through the service's
// connector seam, so the compiled-in catalog stays untouched and the structural
// gate over it keeps asserting against the real rows.
type socialProvider struct {
	server  *httptest.Server
	profile string
}

func newSocialProvider(t *testing.T, profile string) *socialProvider {
	t.Helper()
	fake := &socialProvider{profile: profile}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"access_token":"upstream-access-token"}`))
	})
	mux.HandleFunc("/profile", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(fake.profile))
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

// install points one connector key at this provider for the duration of a test.
func (p *socialProvider) install(e *federationEnvironment, key string, template connector) {
	template.Token = p.server.URL + "/token"
	template.Profile = p.server.URL + "/profile"
	e.service.connectors = func(requested string) (connector, error) {
		if requested == key {
			return template, nil
		}
		return lookupConnector(requested)
	}
}

// socialConnectorTemplate is a provider that states a verified address, which is
// what just-in-time provisioning requires.
func socialConnectorTemplate() connector {
	return connector{
		Authorize: "https://social.example/authorize",
		Scopes:    "profile email",
		Subject:   "id", Email: "email", Name: "name", Username: "login",
	}
}

func (e *federationEnvironment) socialProviderInput(issuerKey string) ProviderInput {
	return ProviderInput{Name: "Social", ProviderType: ProviderOAuth2, Issuer: issuerKey,
		ClientID: "social-client", ClientSecret: "social-secret", AutoProvision: true, Status: ProviderActive}
}

func TestSocialLoginProvisionsAnAccountAndRecordsTheMethod(t *testing.T) {
	env := newFederationEnvironment(t, "sl_")
	upstream := newSocialProvider(t, `{"id":4815162342,"email":"ada@example.com","name":"Ada","login":"ada"}`)
	upstream.install(env, "example-social", socialConnectorTemplate())

	tenant := federationID("tenant-social")
	provider := env.createProvider(t, tenant, env.socialProviderInput("example-social"))
	require.Equal(t, ProviderOAuth2, provider.ProviderType)
	assert.Equal(t, "example-social", provider.Issuer, "the connector key is stored verbatim")

	callback := "https://console.example" + callbackURL(provider.ID)
	start, err := env.service.StartLogin(t.Context(), provider.ID, "https://app.example/", callback)
	require.NoError(t, err)

	authorization, err := url.Parse(start.AuthorizationURL)
	require.NoError(t, err)
	assert.Equal(t, "social.example", authorization.Host, "the redirect goes to the connector's own endpoint")
	assert.Equal(t, "social-client", authorization.Query().Get("client_id"))
	assert.Equal(t, "S256", authorization.Query().Get("code_challenge_method"))

	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	state, err := openState(env.keys, stateValue)
	require.NoError(t, err)

	result, err := env.service.CompleteLogin(t.Context(), provider.ID, "upstream-code", state.Nonce,
		stateCookieName+"="+stateValue, callback)
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionToken)
	assert.Equal(t, "https://app.example/", result.ReturnURL)

	// The session records how the person authenticated, so later policy can tell
	// a social sign-in from a password one.
	claims, err := env.web.Authenticate(t.Context(), "Bearer "+result.SessionToken, "")
	require.NoError(t, err)
	assert.Contains(t, claims.AMR, "oauth2")

	// The identity link is what makes a second login idempotent, and the
	// subject is the provider's exact numeric identifier.
	var subject string
	require.NoError(t, env.db.Table(env.prefix+"iam_identity_links").
		Where("provider_id = ?", provider.ID).Pluck("external_subject", &subject).Error)
	assert.Equal(t, "4815162342", subject)

	require.NotEmpty(t, env.events)
	last := env.events[len(env.events)-1]
	assert.Equal(t, "federation_login", last.EventType)
	assert.Equal(t, "ada@example.com", last.Detail["email"])
	assert.Equal(t, true, last.Detail["provisioned"])
}

func TestSocialLoginIsIdempotentForTheSameSubject(t *testing.T) {
	env := newFederationEnvironment(t)
	upstream := newSocialProvider(t, `{"id":"stable-subject","email":"ada@example.com","name":"Ada"}`)
	upstream.install(env, "example-social", socialConnectorTemplate())

	provider := env.createProvider(t, federationID("tenant-social"), env.socialProviderInput("example-social"))
	callback := "https://console.example" + callbackURL(provider.ID)

	signIn := func() string {
		start, err := env.service.StartLogin(t.Context(), provider.ID, "https://app.example/", callback)
		require.NoError(t, err)
		stateValue, err := cookieValue(start.StateCookie, stateCookieName)
		require.NoError(t, err)
		state, err := openState(env.keys, stateValue)
		require.NoError(t, err)
		result, err := env.service.CompleteLogin(t.Context(), provider.ID, "code", state.Nonce,
			stateCookieName+"="+stateValue, callback)
		require.NoError(t, err)
		return result.SessionToken
	}

	first, second := signIn(), signIn()
	assert.NotEqual(t, first, second, "each sign-in issues its own session")

	var links int64
	require.NoError(t, env.db.Table(env.prefix+"iam_identity_links").
		Where("provider_id = ?", provider.ID).Count(&links).Error)
	assert.EqualValues(t, 1, links, "one external identity maps to one account")
}

func TestSocialLoginRefusesToProvisionWithoutAnAddress(t *testing.T) {
	// X, WeChat and QQ state no address. Provisioning from one would invent an
	// unverified identity, so the shared path refuses.
	env := newFederationEnvironment(t)
	template := socialConnectorTemplate()
	template.Email = ""
	upstream := newSocialProvider(t, `{"id":"no-address","name":"Anon"}`)
	upstream.install(env, "example-social", template)

	provider := env.createProvider(t, federationID("tenant-social"), env.socialProviderInput("example-social"))
	callback := "https://console.example" + callbackURL(provider.ID)

	start, err := env.service.StartLogin(t.Context(), provider.ID, "https://app.example/", callback)
	require.NoError(t, err)
	stateValue, err := cookieValue(start.StateCookie, stateCookieName)
	require.NoError(t, err)
	state, err := openState(env.keys, stateValue)
	require.NoError(t, err)

	_, err = env.service.CompleteLogin(t.Context(), provider.ID, "code", state.Nonce,
		stateCookieName+"="+stateValue, callback)
	require.ErrorIs(t, err, ErrProvisioningUnverified)
}

func TestSocialProviderCannotStandInForReauthentication(t *testing.T) {
	// A social provider carries no upstream authentication time and routinely
	// answers from an existing session, so using one as a re-authentication
	// proof would be a redirect that always succeeds.
	env := newFederationEnvironment(t)
	upstream := newSocialProvider(t, `{"id":"subject","email":"ada@example.com","name":"Ada"}`)
	upstream.install(env, "example-social", socialConnectorTemplate())

	// Only the capability guard that runs before the refusal is stubbed. Inspect
	// is deliberately left failing: reaching it would mean the refusal did not
	// happen, so a regression shows up as the wrong error rather than as a pass.
	env.service.accountReauthentication = federatedReauthentication{
		ConsumeIndependentOn: func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error {
			return nil
		},
		Inspect: func(context.Context, string, string) (authn.ReauthenticationBinding, error) {
			return authn.ReauthenticationBinding{}, assert.AnError
		},
		ValidateSessionOn: func(context.Context, *gorm.DB, guid.ID, string) (int64, error) { return 0, nil },
	}

	principalID := federationID("principal-social")
	session, err := env.web.CreateSession(t.Context(), principalID, federationTestNow,
		authnext.Assurance{AuthTime: federationTestNow, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	cookie := env.web.SessionCookie(session)

	provider := env.createProvider(t, federationID("tenant-social"), env.socialProviderInput("example-social"))
	callback := "https://console.example" + callbackURL(provider.ID)

	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID,
		"proof", identityLinkID(provider.ID, "subject"), "https://app.example/", callback)
	require.ErrorIs(t, err, ErrNoUpstreamAuthTime)

	// Linking is still open to the same provider, which is the whole point of
	// refusing only the stronger purpose: the redirect is built, and it is the
	// connector's own endpoint rather than an OIDC discovery result.
	row, err := env.service.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	redirect, err := env.service.identityOperationRedirect(t.Context(), row,
		"account.state", "verifier", callback, federationTestNow)
	require.NoError(t, err)

	parsed, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.Equal(t, "social.example", parsed.Host)
	assert.Equal(t, "account.state", parsed.Query().Get("state"))
	assert.Equal(t, codeChallenge("verifier"), parsed.Query().Get("code_challenge"))
}

func TestSocialProviderRegistrationValidatesTheConnectorKey(t *testing.T) {
	env := newFederationEnvironment(t)
	tenant := federationID("tenant-social")

	_, err := env.service.CreateProvider(t.Context(), tenant, env.socialProviderInput("not-a-connector"))
	require.ErrorIs(t, err, ErrUnknownConnector, "an unknown key is refused at registration, not at login")

	// A real catalog key is accepted and normalized, and its own scopes become
	// the default rather than the OIDC ones.
	input := env.socialProviderInput("  GitHub  ")
	provider := env.createProvider(t, tenant, input)
	assert.Equal(t, "github", provider.Issuer)
	assert.Equal(t, socialConnectors["github"].Scopes, provider.Scopes)
	assert.NotEqual(t, defaultScopes, provider.Scopes)
}

func TestSocialProviderRegistrationKeepsExplicitScopes(t *testing.T) {
	env := newFederationEnvironment(t)
	input := env.socialProviderInput("github")
	input.Scopes = "read:user"

	provider := env.createProvider(t, federationID("tenant-social"), input)
	assert.Equal(t, "read:user", provider.Scopes, "a tenant's own scopes are not overwritten by the default")
}

func TestSocialProviderFingerprintCoversTheConnectorKey(t *testing.T) {
	// The key decides which hosts a login reaches, so changing it has to
	// distrust the identity links bound under the old one.
	github := providerRow{ProviderType: ProviderOAuth2, Issuer: "github", ClientID: "same"}
	gitlab := providerRow{ProviderType: ProviderOAuth2, Issuer: "gitlab", ClientID: "same"}

	assert.NotEqual(t, securityProviderFingerprint(github), securityProviderFingerprint(gitlab))
	assert.NotEqual(t,
		securityProviderFingerprint(github),
		securityProviderFingerprint(providerRow{ProviderType: ProviderOIDC, Issuer: "github", ClientID: "same"}),
		"the protocol is part of the fingerprint, so the same name under another protocol is a different provider")
}
