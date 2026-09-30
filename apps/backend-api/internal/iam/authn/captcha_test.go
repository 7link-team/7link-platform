package authn

import (
	"encoding/json"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wenlng/go-captcha/v2/click"
)

func TestTextCaptchaIsOneShot(t *testing.T) {
	id, encoded, answer, err := imageCaptcha.Generate()
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NotEmpty(t, encoded)
	require.NotEmpty(t, answer)

	assert.True(t, imageCaptcha.Verify(id, answer, true))
	assert.False(t, imageCaptcha.Verify(id, answer, true))
}

func TestVerifyCaptchaRejectsWrongEmptyAndUnknownValues(t *testing.T) {
	id, _, answer, err := imageCaptcha.Generate()
	require.NoError(t, err)
	wrong := "0000"
	if wrong == answer {
		wrong = "9999"
	}
	require.NotEqual(t, answer, wrong)

	assert.False(t, verifyCaptchaOf(captchaText, id, wrong))
	assert.False(t, verifyCaptchaOf(captchaText, "", "1234"))
	assert.False(t, verifyCaptchaOf(captchaText, id, ""))
	assert.False(t, verifyCaptchaOf(captchaKind("unknown"), id, answer))
}

func TestInteractiveCaptchaAnswerIsOneShotAndExpires(t *testing.T) {
	storeInteractiveAnswer("valid", "S:100:50")
	assert.True(t, verifyCaptchaOf(captchaSlide, "valid", "104"))
	assert.False(t, verifyCaptchaOf(captchaSlide, "valid", "104"))

	storeInteractiveAnswer("expired", "S:100:50")
	interactiveAnswers.Lock()
	interactiveAnswers.expiresAt["expired"] = time.Now().Add(-time.Second)
	interactiveAnswers.Unlock()
	assert.False(t, verifyCaptchaOf(captchaSlide, "expired", "100"))

	interactiveAnswers.Lock()
	_, valueExists := interactiveAnswers.values["expired"]
	_, expiryExists := interactiveAnswers.expiresAt["expired"]
	interactiveAnswers.Unlock()
	assert.False(t, valueExists)
	assert.False(t, expiryExists)
}

func TestRandomTokenRejectsInvalidSize(t *testing.T) {
	_, err := randomToken(0)
	require.Error(t, err)
	token, err := randomToken(16)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestIssueCaptchaProducesEverySupportedChallengeShape(t *testing.T) {
	offering := newTestOffering(t, authnext.CaptchaConfig{
		Kinds: []string{"text", "slide", "click"},
	})
	seen := make(map[string]bool, 3)
	for range 100 {
		challenge, err := offering.issue()
		require.NoError(t, err)
		require.NotEmpty(t, challenge.CaptchaID)
		switch challenge.Type {
		case string(captchaText):
			assert.NotEmpty(t, challenge.ImageBase64)
		case string(captchaSlide):
			assert.NotEmpty(t, challenge.MasterImage)
			assert.NotEmpty(t, challenge.TileImage)
			assert.Positive(t, challenge.Width)
			assert.Positive(t, challenge.Height)
		case string(captchaClick):
			assert.NotEmpty(t, challenge.MasterImage)
			assert.NotEmpty(t, challenge.ThumbImage)
			assert.Positive(t, challenge.Width)
			assert.Positive(t, challenge.Height)
		default:
			t.Fatalf("unsupported captcha type %q", challenge.Type)
		}
		seen[challenge.Type] = true
		if len(seen) == 3 {
			break
		}
	}
	assert.Equal(t, map[string]bool{
		string(captchaText): true, string(captchaSlide): true, string(captchaClick): true,
	}, seen)
}

func TestVerifyInteractiveCaptchaRejectsMalformedAndConsumesValidAnswers(t *testing.T) {
	storeInteractiveAnswer("bad-slide-state", "invalid")
	assert.False(t, verifyCaptchaOf(captchaSlide, "bad-slide-state", "100"))
	storeInteractiveAnswer("bad-slide-answer", "S:100:50")
	assert.False(t, verifyCaptchaOf(captchaSlide, "bad-slide-answer", "invalid"))

	storeInteractiveAnswer("bad-click-prefix", "invalid")
	assert.False(t, verifyCaptchaOf(captchaClick, "bad-click-prefix", "100,100"))
	storeInteractiveAnswer("bad-click-json", "C:invalid")
	assert.False(t, verifyCaptchaOf(captchaClick, "bad-click-json", "100,100"))
	dots, err := json.Marshal(map[int]*click.Dot{
		0: {X: 100, Y: 100},
		1: {X: 150, Y: 120},
	})
	require.NoError(t, err)
	storeInteractiveAnswer("bad-click-answer", "C:"+string(dots))
	assert.False(t, verifyCaptchaOf(captchaClick, "bad-click-answer", "invalid"))
	storeInteractiveAnswer("missed-click", "C:"+string(dots))
	assert.False(t, verifyCaptchaOf(captchaClick, "missed-click", "1,1;2,2"))
	storeInteractiveAnswer("valid-click", "C:"+string(dots))
	assert.True(t, verifyCaptchaOf(captchaClick, "valid-click", "100,100;150,120"))
	assert.False(t, verifyCaptchaOf(captchaClick, "valid-click", "100,100;150,120"))
}

// newTestOffering builds an offering the way NewWebService does, so these tests
// exercise the parse rather than a struct literal that skipped it.
func newTestOffering(t *testing.T, cfg authnext.CaptchaConfig) *captchaOffering {
	t.Helper()
	offering, err := parseCaptchaOffering(cfg)
	require.NoError(t, err)
	return offering
}

// TestAnUnconfiguredDeploymentOffersTheTextChallengeOnly is the default this
// change exists to establish. Presenting one of three challenges at random
// meant a third of the people registering met a slider and a third met a
// click-the-characters grid, neither of which everyone can complete.
func TestAnUnconfiguredDeploymentOffersTheTextChallengeOnly(t *testing.T) {
	offering := newTestOffering(t, authnext.CaptchaConfig{})
	require.Equal(t, []captchaKind{captchaText}, offering.kinds)

	for range 20 {
		challenge, err := offering.issue()
		require.NoError(t, err)
		assert.Equal(t, string(captchaText), challenge.Type)
		assert.NotEmpty(t, challenge.ImageBase64)
	}
}

// TestADeploymentPresentsWhateverItConfigured keeps the harder challenges
// available to a deployment that wants them, without their being what an
// unconfigured one hands out.
func TestADeploymentPresentsWhateverItConfigured(t *testing.T) {
	offering := newTestOffering(t, authnext.CaptchaConfig{
		Kinds: []string{" SLIDE ", "", "slide", "click"},
	})

	assert.Equal(t, []captchaKind{captchaSlide, captchaClick}, offering.kinds,
		"spacing and case are read the way an operator meant them, a blank line configures nothing,"+
			" and a repeat is a set member rather than a second entry in the random pick")
}

// TestAKindTheDeploymentDoesNotOfferCannotBeAnswered is the half that makes the
// offering a control rather than a presentation choice. Whoever is registering
// names the kind in their own request, so a kind left out of issuing alone
// would still be reachable by asking for it.
func TestAKindTheDeploymentDoesNotOfferCannotBeAnswered(t *testing.T) {
	offering := newTestOffering(t, authnext.CaptchaConfig{Kinds: []string{"text"}})

	storeInteractiveAnswer("unoffered-slide", "S:100:50")
	assert.False(t, mustVerify(t, offering, "slide", "unoffered-slide", "104"))

	id, _, answer, err := imageCaptcha.Generate()
	require.NoError(t, err)
	assert.True(t, mustVerify(t, offering, "text", id, answer))
}

// TestAConfiguredTestAnswerLetsAScriptThrough covers the mode an acceptance run
// needs: the challenge is still issued and still looks exactly like the real
// one, and only its outcome is known ahead of time.
func TestAConfiguredTestAnswerLetsAScriptThrough(t *testing.T) {
	const configured = "not-a-real-captcha-answer"
	offering := newTestOffering(t, authnext.CaptchaConfig{TestAnswer: configured})

	id, _, solved, err := imageCaptcha.Generate()
	require.NoError(t, err)
	assert.True(t, mustVerify(t, offering, "text", id, configured))
	// The challenge is consumed either way, so a configured test answer changes
	// which answers are accepted and not whether one challenge answers twice.
	assert.False(t, mustVerify(t, offering, "text", id, solved))

	t.Run("the real answer still works", func(t *testing.T) {
		id, _, solved, err := imageCaptcha.Generate()
		require.NoError(t, err)
		assert.True(t, mustVerify(t, offering, "text", id, solved))
	})
	t.Run("and nothing near it does", func(t *testing.T) {
		id, _, _, err := imageCaptcha.Generate()
		require.NoError(t, err)
		assert.False(t, mustVerify(t, offering, "text", id, configured+"x"))
	})
}

// TestWithoutAConfiguredTestAnswerNothingStandsInForSolving is the property
// every deployment that never opts in is relying on, including the ones that
// never learn the mode exists.
func TestWithoutAConfiguredTestAnswerNothingStandsInForSolving(t *testing.T) {
	offering := newTestOffering(t, authnext.CaptchaConfig{})

	for _, guess := range []string{"", "test", "not-a-real-captcha", "not-a-real-captcha-answer"} {
		id, _, _, err := imageCaptcha.Generate()
		require.NoError(t, err)
		assert.False(t, mustVerify(t, offering, "text", id, guess))
	}
}

// TestCaptchaConfigurationThatCannotMeanWhatItLooksLikeIsRefused fails the
// deployment at startup, which is the only moment an operator is still watching.
func TestCaptchaConfigurationThatCannotMeanWhatItLooksLikeIsRefused(t *testing.T) {
	t.Run("a kind nothing here builds", func(t *testing.T) {
		_, err := parseCaptchaOffering(authnext.CaptchaConfig{
			Kinds: []string{"text", "turnstile"},
		})
		require.ErrorContains(t, err, "turnstile",
			"the operator has to be told which entry to fix")
	})
	t.Run("a test answer short enough to be reached by trying", func(t *testing.T) {
		_, err := parseCaptchaOffering(authnext.CaptchaConfig{TestAnswer: "1234"})
		require.ErrorContains(t, err, "at least 8 characters")
	})
}

// TestAChallengeShapeNothingBuildsIsAnErrorRatherThanEmptyOutput keeps a kind
// that gets added to the offering and forgotten here from returning a challenge
// with nothing in it, which a browser draws as a broken image and the person
// registering reads as their own mistake.
func TestAChallengeShapeNothingBuildsIsAnErrorRatherThanEmptyOutput(t *testing.T) {
	_, err := issueCaptchaOf(captchaKind("turnstile"))
	require.ErrorContains(t, err, "turnstile")
}

// testCaptchaAnswer is configured as every test service's captcha test answer,
// so a request that must carry a solved challenge can do so without an image.
// It is deliberately longer than minimumCaptchaTestAnswer.
const testCaptchaAnswer = "captcha-test-answer"

// mustVerify keeps these cases reading as verdicts. A drawn challenge never
// reports the second value -- nothing about it can be unreachable -- so a
// reason here is a bug in the offering rather than a case worth asserting on.
func mustVerify(t *testing.T, offering *captchaOffering, kind, id, answer string) bool {
	t.Helper()
	solved, err := offering.verify(t.Context(), kind, id, answer)
	require.NoError(t, err)
	return solved
}
