package authn

import (
	"context"
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

// captchaProvider names who poses the challenge. The drawn one needs nothing
// but this service; Turnstile needs a Cloudflare account and reachability.
type captchaProvider string

const (
	captchaProviderImage     captchaProvider = "image"
	captchaProviderTurnstile captchaProvider = "turnstile"
)

// CaptchaCapability is what a client needs before it can present a challenge.
// The site key is published deliberately: the widget cannot render without it,
// and it is the secret key -- which never leaves this process -- that makes a
// token worth anything.
type CaptchaCapability struct {
	Provider string `json:"provider" enum:"image,turnstile" doc:"who poses the challenge"`
	SiteKey  string `json:"site_key,omitempty" doc:"Turnstile site key; absent unless the provider is turnstile"`
}

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
	// turnstile is set only when the deployment delegates the challenge. When
	// it is, nothing is drawn here and kinds is not consulted.
	turnstile *turnstileVerifier
	// turnstileSiteKey is published to clients so the widget can render.
	turnstileSiteKey string
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
	// A delegated challenge is drawn by Cloudflare, so the kinds above stop
	// meaning anything and the verifier replaces the store entirely. Chosen
	// rather than inferred from the presence of keys: a half-filled Turnstile
	// section should fail the deployment, not quietly serve drawn challenges.
	switch captchaProvider(strings.ToLower(strings.TrimSpace(cfg.Provider))) {
	case "", captchaProviderImage:
	case captchaProviderTurnstile:
		verifier, err := newTurnstileVerifier(cfg.Turnstile)
		if err != nil {
			return nil, err
		}
		offering.turnstile = verifier
		offering.turnstileSiteKey = strings.TrimSpace(cfg.Turnstile.SiteKey)
	default:
		return nil, fmt.Errorf(
			"authn captcha provider %q must be image or turnstile", cfg.Provider,
		)
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
		slog.Warn("authn accepts a configured captcha test answer;" +
			" login and registration on this deployment are not captcha-protected")
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
	if o.turnstile != nil {
		// Nothing is drawn here. A client that asks has not read the login
		// options, and saying so is more useful than handing back a challenge
		// its widget would ignore.
		return nil, fmt.Errorf("%w: this deployment delegates the challenge to turnstile", errCaptchaUnavailable)
	}
	return issueCaptchaWithStore(o.kinds[secureIntn(len(o.kinds))], o.store)
}

// verify consumes one challenge and reports whether it was answered. A kind the
// deployment does not offer is refused before the answer is looked at.
//
// The challenge is always consumed, including when the test answer is what
// arrived, so a configured test answer changes which answers are accepted and
// not whether a challenge is one-shot.
func (o *captchaOffering) verify(ctx context.Context, kind, id, answer string) (bool, error) {
	if o.turnstile != nil {
		// The configured test answer is honored here too, for the same reason
		// it exists at all: an automated check cannot drive a Cloudflare widget
		// any more than it can read an image.
		if o.testAnswer != "" && subtle.ConstantTimeCompare([]byte(answer), []byte(o.testAnswer)) == 1 {
			return true, nil
		}
		return o.turnstile.verify(ctx, answer)
	}
	if id == "" || answer == "" || !o.offers(captchaKind(kind)) {
		return false, nil
	}
	solved := verifyCaptchaWithStore(captchaKind(kind), id, answer, o.store)
	if solved || o.testAnswer == "" {
		return solved, nil
	}
	return subtle.ConstantTimeCompare([]byte(answer), []byte(o.testAnswer)) == 1, nil
}

// IssueCaptcha produces one challenge of a kind this deployment draws.
//
// A deployment that delegates to Turnstile draws nothing: the widget obtains
// its own token from Cloudflare, and the client learns which provider to render
// from the login options rather than by asking for a challenge it will not get.
func (s *Service) IssueCaptcha() (*captchaOutput, error) { return s.captcha.issue() }

// VerifyCaptcha reports whether the challenge was answered.
//
// The error is not the answer: false with no error is a challenge that was not
// solved, and errCaptchaUnavailable is a challenge that could not be checked.
// Both refuse the attempt -- a captcha fails closed -- but only the first is
// the caller's doing, and telling somebody who solved it that they got it wrong
// sends them round a loop they cannot win.
func (s *Service) VerifyCaptcha(ctx context.Context, kind, id, answer string) (bool, error) {
	return s.captcha.verify(ctx, kind, id, answer)
}

// CaptchaCapability describes what a client must render to answer a challenge.
func (s *Service) CaptchaCapability() CaptchaCapability {
	if s == nil || s.captcha == nil || s.captcha.turnstile == nil {
		return CaptchaCapability{Provider: string(captchaProviderImage)}
	}
	return CaptchaCapability{
		Provider: string(captchaProviderTurnstile),
		SiteKey:  s.captcha.turnstileSiteKey,
	}
}
