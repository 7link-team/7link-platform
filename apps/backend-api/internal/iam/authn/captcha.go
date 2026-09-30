package authn

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"strings"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/mojocn/base64Captcha"
)

// The canvas is close to the size the form shows it at, so the characters are
// not shrunk on the way to the screen. A 220x64 image displayed beside a 36px
// input had to be scaled to little over half size, which is what made it hard
// to read; at 144x44 the same characters arrive nearly unscaled. The noise dots
// are reduced with the canvas so their density, not just their count, is kept.
var captchaDrivers = []base64Captcha.Driver{
	base64Captcha.NewDriverDigit(44, 132, 4, 0.25, 30),
	base64Captcha.NewDriverString(44, 144, 4, 1, 4, base64Captcha.TxtAlphabet, nil, nil, nil),
	base64Captcha.NewDriverMath(44, 144, 4, 1, nil, nil, nil),
}

// Text challenges use the process-local one-shot store, matching the
// single-instance deployment contract of the source service.
var imageCaptcha = base64Captcha.NewCaptcha(captchaDrivers[0], base64Captcha.DefaultMemStore)

// minimumCaptchaTestAnswer keeps a deployment from configuring a bypass so
// short that something could reach it by trying. The answer is shared with an
// automated check rather than kept, so the length is not standing in for
// secrecy -- it is standing in for deliberateness.
const minimumCaptchaTestAnswer = 8

// defaultCaptchaKinds is what a deployment that configures nothing presents.
// The text challenge is alone in it because it is the one every browser can
// draw and every assistive technology can get through; a challenge that needs a
// mouse and a steady hand excludes people before it excludes any bot.
var defaultCaptchaKinds = []captchaKind{captchaText}

// captchaOffering is the set of challenges a deployment presents, plus the
// answer it will accept in place of solving one.
//
// Both halves of the flow are bounded by the set: issuing picks from it, and
// verifying refuses anything outside it. Bounding only the issuing half would
// leave every kind reachable by whoever names it in the request, which matters
// because the kinds do not all cost the same to defeat.
type captchaOffering struct {
	kinds []captchaKind
	store base64Captcha.Store
	// testAnswer is empty in any deployment real people use. When it is set,
	// it is accepted as the answer to any challenge, which is what lets an
	// automated check get through registration without a way to read an image.
	//
	// It is configured rather than published as a constant. A constant in this
	// file would be the same string in every deployment that ever forgot to
	// turn the mode off, and would look, to anyone reading it later, like a
	// secret the repository was keeping.
	testAnswer string
}

// parseCaptchaOffering reads the configured challenge kinds and test answer,
// failing the deployment at startup rather than at somebody's first attempt to
// register.
func parseCaptchaOffering(cfg authnext.CaptchaConfig) (*captchaOffering, error) {
	offering := &captchaOffering{testAnswer: strings.TrimSpace(cfg.TestAnswer)}
	for _, entry := range cfg.Kinds {
		// A blank entry configures nothing. Deployment configuration grows
		// placeholder list entries, and failing on one would teach an operator
		// to delete the line rather than to fill it in.
		if strings.TrimSpace(entry) == "" {
			continue
		}
		kind := captchaKind(strings.ToLower(strings.TrimSpace(entry)))
		switch kind {
		case captchaText, captchaSlide, captchaClick:
		default:
			return nil, fmt.Errorf(
				"authn captcha kind %q must be one of text, slide, click", entry,
			)
		}
		// The configured list is a set. A repeated entry would otherwise weight
		// the random pick, which is not what a second identical line looks like
		// it is asking for.
		if offering.offers(kind) {
			continue
		}
		offering.kinds = append(offering.kinds, kind)
	}
	if len(offering.kinds) == 0 {
		offering.kinds = defaultCaptchaKinds
	}
	if offering.testAnswer != "" {
		if len(offering.testAnswer) < minimumCaptchaTestAnswer {
			return nil, fmt.Errorf(
				"authn captcha test answer must be at least %d characters", minimumCaptchaTestAnswer,
			)
		}
		// This belongs in the log of every start. A deployment reaches this
		// state by writing one line into configuration, and the symptom of
		// having written it is that everything keeps working.
		slog.Warn("authn registration accepts a configured captcha test answer;" +
			" registration on this deployment is not captcha-protected")
	}
	return offering, nil
}

// offers reports whether kind is one this deployment presents.
func (o *captchaOffering) offers(kind captchaKind) bool {
	for _, offered := range o.kinds {
		if offered == kind {
			return true
		}
	}
	return false
}

// issue produces one challenge, picking uniformly among the offered kinds so a
// deployment that offers more than one does not become predictable.
func (o *captchaOffering) issue() (*captchaOutput, error) {
	return issueCaptchaWithStore(o.kinds[secureIntn(len(o.kinds))], o.store)
}

// verify consumes one challenge and reports whether it was answered. A kind the
// deployment does not offer is refused before the answer is looked at.
//
// The challenge is always consumed, including when the test answer is what
// arrived, so a configured test answer changes which answers are accepted and
// not whether a challenge is one-shot.
func (o *captchaOffering) verify(kind, id, answer string) bool {
	if id == "" || answer == "" || !o.offers(captchaKind(kind)) {
		return false
	}
	solved := verifyCaptchaWithStore(captchaKind(kind), id, answer, o.store)
	if solved || o.testAnswer == "" {
		return solved
	}
	return subtle.ConstantTimeCompare([]byte(answer), []byte(o.testAnswer)) == 1
}

// IssueCaptcha produces one registration challenge of a kind this deployment
// presents.
func (s *Service) IssueCaptcha() (*captchaOutput, error) { return s.captcha.issue() }

// VerifyCaptcha consumes one registration challenge and reports whether it was
// answered.
func (s *Service) VerifyCaptcha(kind, id, answer string) bool {
	return s.captcha.verify(kind, id, answer)
}
