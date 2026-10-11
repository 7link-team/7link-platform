package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// turnstileStub stands in for Cloudflare. Pointing the verifier at a server
// this test owns is why verify_url is configuration: the suite must not depend
// on reaching the internet, and it must be able to produce the refusals and
// outages that only happen on somebody else's bad day.
func turnstileStub(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func turnstileConfig(t *testing.T, url string) authnext.TurnstileConfig {
	t.Helper()
	return authnext.TurnstileConfig{
		SiteKey: "1x00000000000000000000AA",
		// Cloudflare's own always-passes dummy secret. It is configuration here
		// rather than a constant for the same reason the test answer is.
		SecretKey: "1x0000000000000000000000000000000AA",
		VerifyURL: url, RequestTimeout: 2 * time.Second,
		// The stub is on loopback, which the outbound policy blocks by default
		// and which a deployment names the same way for an internal proxy.
		OutboundAllowPlaintext: true,
		OutboundAllowNetworks:  []string{"127.0.0.0/8", "::1/128"},
	}
}

func TestTurnstileAcceptsWhatCloudflareAccepts(t *testing.T) {
	var seen struct {
		secret, response string
		parseErr         error
	}
	url := turnstileStub(t, func(w http.ResponseWriter, r *http.Request) {
		seen.parseErr = r.ParseForm()
		seen.secret, seen.response = r.PostFormValue("secret"), r.PostFormValue("response")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	verifier, err := newTurnstileVerifier(turnstileConfig(t, url))
	require.NoError(t, err)

	solved, err := verifier.verify(t.Context(), "XXXX.DUMMY.TOKEN.XXXX")
	require.NoError(t, err)
	require.NoError(t, seen.parseErr)
	assert.True(t, solved)
	assert.Equal(t, "1x0000000000000000000000000000000AA", seen.secret,
		"the secret is sent to Cloudflare and nowhere else")
	assert.Equal(t, "XXXX.DUMMY.TOKEN.XXXX", seen.response)
}

func TestTurnstileRefusesWhatCloudflareRefuses(t *testing.T) {
	url := turnstileStub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["timeout-or-duplicate"]}`))
	})
	verifier, err := newTurnstileVerifier(turnstileConfig(t, url))
	require.NoError(t, err)

	solved, err := verifier.verify(t.Context(), "XXXX.DUMMY.TOKEN.XXXX")
	require.NoError(t, err, "a refusal is an answer, not an outage")
	assert.False(t, solved)
}

// An unreachable or broken validation endpoint must be told apart from a wrong
// answer. Both refuse the attempt; only one is the caller's fault.
func TestTurnstileSeparatesAnOutageFromARefusal(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"server error":   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"not json":       func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>captive portal</html>")) },
		"empty response": func(w http.ResponseWriter, _ *http.Request) {},
	} {
		t.Run(name, func(t *testing.T) {
			verifier, err := newTurnstileVerifier(turnstileConfig(t, turnstileStub(t, handler)))
			require.NoError(t, err)
			solved, err := verifier.verify(t.Context(), "XXXX.DUMMY.TOKEN.XXXX")
			assert.False(t, solved)
			require.Error(t, err)
			assert.ErrorIs(t, err, errCaptchaUnavailable)
		})
	}
}

// A token outside Cloudflare's documented bounds is refused before it costs a
// request, and an empty one is a refusal rather than an outage.
func TestTurnstileRefusesAnImpossibleTokenWithoutAsking(t *testing.T) {
	asked := 0
	url := turnstileStub(t, func(w http.ResponseWriter, _ *http.Request) {
		asked++
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	verifier, err := newTurnstileVerifier(turnstileConfig(t, url))
	require.NoError(t, err)

	for _, token := range []string{"", "   ", string(make([]byte, turnstileTokenMaxBytes+1))} {
		solved, err := verifier.verify(t.Context(), token)
		assert.False(t, solved)
		assert.NoError(t, err)
	}
	assert.Zero(t, asked, "Cloudflare is not asked about a token it could not have issued")
}

// Half-configured Turnstile fails the deployment rather than quietly serving
// drawn challenges, which would leave an operator believing they had turned it
// on.
func TestTurnstileRefusesAnIncompleteConfiguration(t *testing.T) {
	complete := turnstileConfig(t, "https://challenges.cloudflare.com/turnstile/v0/siteverify")
	for name, mutate := range map[string]func(*authnext.TurnstileConfig){
		"no secret":     func(c *authnext.TurnstileConfig) { c.SecretKey = "" },
		"no site key":   func(c *authnext.TurnstileConfig) { c.SiteKey = "" },
		"no verify url": func(c *authnext.TurnstileConfig) { c.VerifyURL = "" },
		"plaintext url": func(c *authnext.TurnstileConfig) {
			c.VerifyURL, c.OutboundAllowPlaintext = "http://challenges.example/verify", false
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := complete
			mutate(&cfg)
			_, err := newTurnstileVerifier(cfg)
			require.Error(t, err)
		})
	}
}

// Selecting the provider is what turns Turnstile on, not the presence of keys.
func TestCaptchaProviderSelectsWhoPosesTheChallenge(t *testing.T) {
	drawn, err := parseCaptchaOffering(authnext.CaptchaConfig{})
	require.NoError(t, err)
	assert.Nil(t, drawn.turnstile)
	issued, err := drawn.issue()
	require.NoError(t, err)
	assert.Equal(t, string(captchaText), issued.Type)

	delegated, err := parseCaptchaOffering(authnext.CaptchaConfig{
		Provider:  string(captchaProviderTurnstile),
		Turnstile: turnstileConfig(t, turnstileStub(t, func(w http.ResponseWriter, _ *http.Request) {})),
	})
	require.NoError(t, err)
	require.NotNil(t, delegated.turnstile)
	assert.Equal(t, "1x00000000000000000000AA", delegated.turnstileSiteKey)

	// Nothing is drawn when the challenge is delegated: a client that asks has
	// not read the login options.
	_, err = delegated.issue()
	require.Error(t, err)
	assert.ErrorIs(t, err, errCaptchaUnavailable)

	_, err = parseCaptchaOffering(authnext.CaptchaConfig{Provider: "recaptcha"})
	require.ErrorContains(t, err, "must be image or turnstile")
}

// The configured test answer works against a delegated challenge too: an
// automated check can no more drive a Cloudflare widget than read an image.
func TestTurnstileHonoursTheConfiguredTestAnswer(t *testing.T) {
	asked := 0
	url := turnstileStub(t, func(w http.ResponseWriter, _ *http.Request) {
		asked++
		_, _ = w.Write([]byte(`{"success":false}`))
	})
	offering, err := parseCaptchaOffering(authnext.CaptchaConfig{
		Provider: string(captchaProviderTurnstile), TestAnswer: testCaptchaAnswer,
		Turnstile: turnstileConfig(t, url),
	})
	require.NoError(t, err)

	solved, err := offering.verify(t.Context(), "turnstile", "", testCaptchaAnswer)
	require.NoError(t, err)
	assert.True(t, solved)
	assert.Zero(t, asked, "the test answer is settled here, not at Cloudflare")

	refused, err := offering.verify(t.Context(), "turnstile", "", "not-the-answer")
	require.NoError(t, err)
	assert.False(t, refused)
	assert.Equal(t, 1, asked)
}
