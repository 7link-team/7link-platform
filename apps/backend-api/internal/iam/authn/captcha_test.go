package authn

import (
	"encoding/json"
	"testing"
	"time"

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

	assert.False(t, VerifyCaptcha("text", id, wrong))
	assert.False(t, VerifyCaptcha("text", "", "1234"))
	assert.False(t, VerifyCaptcha("text", id, ""))
	assert.False(t, VerifyCaptcha("unknown", id, answer))
}

func TestInteractiveCaptchaAnswerIsOneShotAndExpires(t *testing.T) {
	storeInteractiveAnswer("valid", "S:100:50")
	assert.True(t, VerifyCaptcha("slide", "valid", "104"))
	assert.False(t, VerifyCaptcha("slide", "valid", "104"))

	storeInteractiveAnswer("expired", "S:100:50")
	interactiveAnswers.Lock()
	interactiveAnswers.expiresAt["expired"] = time.Now().Add(-time.Second)
	interactiveAnswers.Unlock()
	assert.False(t, VerifyCaptcha("slide", "expired", "100"))

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
	seen := make(map[string]bool, 3)
	for range 100 {
		challenge, err := issueCaptcha()
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
	assert.False(t, VerifyCaptcha("slide", "bad-slide-state", "100"))
	storeInteractiveAnswer("bad-slide-answer", "S:100:50")
	assert.False(t, VerifyCaptcha("slide", "bad-slide-answer", "invalid"))

	storeInteractiveAnswer("bad-click-prefix", "invalid")
	assert.False(t, VerifyCaptcha("click", "bad-click-prefix", "100,100"))
	storeInteractiveAnswer("bad-click-json", "C:invalid")
	assert.False(t, VerifyCaptcha("click", "bad-click-json", "100,100"))
	dots, err := json.Marshal(map[int]*click.Dot{
		0: {X: 100, Y: 100},
		1: {X: 150, Y: 120},
	})
	require.NoError(t, err)
	storeInteractiveAnswer("bad-click-answer", "C:"+string(dots))
	assert.False(t, VerifyCaptcha("click", "bad-click-answer", "invalid"))
	storeInteractiveAnswer("missed-click", "C:"+string(dots))
	assert.False(t, VerifyCaptcha("click", "missed-click", "1,1;2,2"))
	storeInteractiveAnswer("valid-click", "C:"+string(dots))
	assert.True(t, VerifyCaptcha("click", "valid-click", "100,100;150,120"))
	assert.False(t, VerifyCaptcha("click", "valid-click", "100,100;150,120"))
}
