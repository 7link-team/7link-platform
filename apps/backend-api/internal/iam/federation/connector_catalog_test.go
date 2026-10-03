package federation

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryConnectorIsWellFormed is the gate that keeps the catalog honest. A
// connector is data, so nothing but a test stands between a mistyped row and a
// login that fails only in production. Each assertion here corresponds to an
// invariant the engine relies on rather than to a style preference.
func TestEveryConnectorIsWellFormed(t *testing.T) {
	require.NotEmpty(t, socialConnectors)

	for key, conn := range socialConnectors {
		t.Run(key, func(t *testing.T) {
			// The key is stored in the issuer column and compared after
			// trimming and lowering, so a row whose key is not already in that
			// form could never be looked up.
			assert.Equal(t, strings.ToLower(strings.TrimSpace(key)), key, "the key must be storable as written")
			assert.NotContains(t, key, "://", "a connector key must not look like an OIDC issuer URL")
			assert.LessOrEqual(t, len(key), 255, "the issuer column bounds the key")

			requireHTTPSEndpoint(t, "authorize", conn.Authorize)
			requireHTTPSEndpoint(t, "token", conn.Token)

			if conn.Flow == nil {
				requireHTTPSEndpoint(t, "profile", conn.Profile,
					"a connector without its own flow is read by the standard one, which needs a profile endpoint")
			}

			// normalizeProvider refuses a non-SAML provider with no scopes, and
			// the column bounds the string.
			assert.NotEmpty(t, conn.Scopes, "a registration with no scopes would be refused")
			assert.LessOrEqual(t, len(conn.Scopes), 255, "the scopes column bounds the default")

			// Without a subject there is nothing to key an identity link on.
			assert.NotEmpty(t, conn.Subject, "every connector must name a subject path")

			if conn.EmailVerified != "" {
				assert.NotEmpty(t, conn.Email,
					"declaring where verification is stated is meaningless without an address")
			}

			if conn.ProfileAuth == profileAuthHeader {
				assert.NotEmpty(t, conn.ProfileAuthHeader, "a header style needs the header's name")
			} else {
				assert.Empty(t, conn.ProfileAuthHeader, "the header name applies only to the header style")
			}

			if conn.TokenRequest != tokenRequestForm {
				// The query and JSON shapes put the secret in the parameters by
				// construction, so a ClientAuth there would read as a choice
				// that is not being honored.
				assert.Equal(t, clientSecretBasic, conn.ClientAuth,
					"ClientAuth only means something for the form shape")
			}

			if conn.ProfileAuthScheme != "" {
				assert.Equal(t, profileAuthBearer, conn.ProfileAuth,
					"overriding the scheme only means something for an Authorization header")
			}

			for parameter, path := range conn.Carry {
				assert.NotEmpty(t, parameter, "a carried value needs the parameter it is sent as")
				assert.NotEmpty(t, path, "a carried value needs the token-response path it comes from")
			}
		})
	}
}

func requireHTTPSEndpoint(t *testing.T, label, raw string, messageAndArgs ...any) {
	t.Helper()
	require.NotEmpty(t, raw, append([]any{label + " endpoint is required"}, messageAndArgs...)...)
	parsed, err := url.Parse(raw)
	require.NoError(t, err, "%s endpoint must parse", label)
	// A deployment may permit plaintext for a self-hosted OIDC issuer, but a
	// compiled-in public provider has no reason to be reached over http.
	assert.Equal(t, "https", parsed.Scheme, "%s endpoint must be https", label)
	assert.NotEmpty(t, parsed.Host, "%s endpoint must name a host", label)
}

// TestConnectorsDoNotShadowOIDCProviders guards the one decision that would
// quietly weaken federation: routing a provider that publishes OpenID Connect
// discovery through the social path trades a verified ID token and an
// authentication time for an unsigned profile read.
func TestConnectorsDoNotShadowOIDCProviders(t *testing.T) {
	for _, key := range []string{
		"google", "microsoft", "azure", "entra", "apple", "slack", "linkedin",
		"line", "twitch", "zoom", "okta", "auth0", "keycloak", "casdoor", "logto",
	} {
		_, present := socialConnectors[key]
		assert.False(t, present,
			"%s publishes OIDC discovery and must be registered as ProviderOIDC instead", key)
	}
}

// TestConnectorsWithoutAnAddressCannotProvision records which connectors can
// only link an existing account, because the shared authentication path refuses
// to create one without a verified address. The list is an assertion, not a
// comment, so adding an address path to one of these is a visible change.
func TestConnectorsWithoutAnAddressCannotProvision(t *testing.T) {
	linkOnly := map[string]bool{}
	for key, conn := range socialConnectors {
		if conn.Email == "" {
			linkOnly[key] = true
		}
	}
	assert.Equal(t, map[string]bool{
		"baidu":     true,
		"bitbucket": true,
		"douyin":    true,
		"instagram": true,
		"qq":        true,
		"wechat":    true,
		"weibo":     true,
		"x":         true,
	}, linkOnly)
}

// TestBespokeFlowsAreReachable keeps a hand-written flow from being orphaned:
// the catalog is the only thing that puts one into service.
func TestBespokeFlowsAreReachable(t *testing.T) {
	flows := map[string]bool{}
	for key, conn := range socialConnectors {
		if conn.Flow != nil {
			flows[key] = true
		}
	}
	assert.Equal(t, map[string]bool{"qq": true}, flows,
		"every implemented profileFlow must be referenced by a catalog row")
}
