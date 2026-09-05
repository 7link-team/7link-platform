package oauth

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	oauthi18n "7link.com/platform/internal/iam/oauth/i18n"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconfigureConsent swaps the consent settings and discards the parsed screen
// so the next render honors the new configuration.
func reconfigureConsent(service *Service, consent ConsentConfig) {
	service.consent = consent.normalized()
	service.consentPage = &consentRenderer{}
}

// consentChallengeOf pulls the challenge out of a consent-screen redirect.
func consentChallengeOf(t *testing.T, location string) string {
	t.Helper()
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	challenge := parsed.Query().Get("consent_challenge")
	require.NotEmpty(t, challenge)
	return challenge
}

func TestConsentTemplateOverrideIsUsedAndFailuresAreReportedNotHidden(t *testing.T) {
	harness := newOAuthHarness(t, "sl_")
	client := createBrowserClient(t, harness)
	location, err := harness.service.Authorize(
		t.Context(), "session", client.ID, client.RedirectURIs[0], "code", "openid",
		"", oauthChallenge(), "S256", "", "consent",
	)
	require.NoError(t, err)
	challenge := consentChallengeOf(t, location)

	directory := t.TempDir()
	custom := filepath.Join(directory, "consent.html")
	require.NoError(t, os.WriteFile(custom, []byte("<p>{{.ClientName}} wants {{.Challenge}}</p>"), 0o600))
	reconfigureConsent(harness.service, ConsentConfig{TemplateFile: custom})
	page, err := harness.service.RenderConsent(t.Context(), "session", challenge)
	require.NoError(t, err)
	assert.Equal(t, "<p>Browser wants "+challenge+"</p>", string(page))

	// A template file that cannot be read is a startup-visible error rather
	// than a silent fallback to the built-in screen, which would quietly serve
	// the wrong branding forever.
	reconfigureConsent(harness.service, ConsentConfig{TemplateFile: filepath.Join(directory, "absent.html")})
	_, err = harness.service.RenderConsent(t.Context(), "session", challenge)
	assert.ErrorContains(t, err, "read OAuth consent template")

	broken := filepath.Join(directory, "broken.html")
	require.NoError(t, os.WriteFile(broken, []byte("{{.ClientName"), 0o600))
	reconfigureConsent(harness.service, ConsentConfig{TemplateFile: broken})
	_, err = harness.service.RenderConsent(t.Context(), "session", challenge)
	assert.ErrorContains(t, err, "parse OAuth consent template")

	// A template that parses but fails at execution time is reported too.
	failing := filepath.Join(directory, "failing.html")
	require.NoError(t, os.WriteFile(failing, []byte("{{.Scopes.Missing}}"), 0o600))
	reconfigureConsent(harness.service, ConsentConfig{TemplateFile: failing})
	_, err = harness.service.RenderConsent(t.Context(), "session", challenge)
	assert.ErrorContains(t, err, "render OAuth consent template")
}

func TestConsentScreenTranslatesKnownScopesAndStillNamesUnknownOnes(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, oauthi18n.Register())

	// An unknown scope is still shown. Hiding a permission the subject is
	// about to grant would be worse than showing an untranslated token.
	lines := consentScopeLines(t.Context(), []string{"openid", "jobs.read"})
	require.Len(t, lines, 2)
	assert.True(t, lines[0].Detail)
	assert.NotEqual(t, lines[0].Name, lines[0].Description)
	assert.False(t, lines[1].Detail)
	assert.Equal(t, "jobs.read", lines[1].Name)
	assert.Equal(t, "jobs.read", lines[1].Description)

	assert.Equal(t, platformi18n.Locale(), consentLang(t.Context()))
	assert.Equal(t, "zh-CN", consentLang(platformi18n.WithLocale(t.Context(), "zh-CN")))
}

func TestConsentChallengeShapeIsCheckedBeforeAnyLookup(t *testing.T) {
	assert.True(t, validConsentChallenge(strings.Repeat("a", 43)))
	assert.True(t, validConsentChallenge(strings.Repeat("-", 21)+"_"+strings.Repeat("Z9", 10)+"x"))
	assert.False(t, validConsentChallenge(""))
	assert.False(t, validConsentChallenge(strings.Repeat("a", 42)))
	assert.False(t, validConsentChallenge(strings.Repeat("a", 42)+"+"))
	assert.False(t, validConsentChallenge(strings.Repeat("a", 42)+"你"))
}
