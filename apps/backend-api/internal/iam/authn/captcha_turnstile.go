package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/httpx"
	"7link.com/platform/pkg/extension/secretx"
)

// turnstileMaxResponseBytes bounds what is read back from the validation
// endpoint. The documented response is a small object; anything larger is
// either not Cloudflare or not worth reading.
const turnstileMaxResponseBytes = 8 << 10

// turnstileTokenMaxBytes is Cloudflare's documented ceiling for a token. A
// longer one is refused before it costs a request.
const turnstileTokenMaxBytes = 2048

// errCaptchaUnavailable separates "this challenge was not solved" from "this
// challenge could not be checked". Both refuse the attempt -- a captcha fails
// closed -- but only the first is the caller's fault, and telling somebody who
// solved the challenge that they got it wrong sends them round a loop they
// cannot win.
var errCaptchaUnavailable = errors.New("captcha verification unavailable")

// turnstileVerifier checks a token with Cloudflare.
//
// The secret never leaves this process: the browser is given only the site key,
// which is why the token has to be checked here rather than trusted as it
// arrives. Tokens are single-use and expire in five minutes, both of which
// Cloudflare enforces; a replayed one comes back as timeout-or-duplicate and is
// refused like any other failure.
type turnstileVerifier struct {
	client  *http.Client
	url     string
	secret  string
	timeout time.Duration
}

func newTurnstileVerifier(cfg authnext.TurnstileConfig) (*turnstileVerifier, error) {
	secret, err := secretx.Resolve("authn captcha turnstile secret_key", cfg.SecretKey, cfg.SecretKeyFile, secretx.DefaultMaxBytes)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("authn captcha turnstile requires secret_key or secret_key_file")
	}
	if strings.TrimSpace(cfg.SiteKey) == "" {
		return nil, errors.New("authn captcha turnstile requires site_key; the browser cannot render the widget without it")
	}
	policy, err := httpx.ParsePolicy(cfg.OutboundAllowPlaintext, cfg.OutboundAllowNetworks)
	if err != nil {
		return nil, fmt.Errorf("authn captcha turnstile outbound policy: %w", err)
	}
	endpoint := strings.TrimSpace(cfg.VerifyURL)
	if endpoint == "" {
		return nil, errors.New("authn captcha turnstile requires verify_url")
	}
	if err := policy.ValidateURL(endpoint); err != nil {
		return nil, fmt.Errorf("authn captcha turnstile verify_url: %w", err)
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &turnstileVerifier{
		client: httpx.NewClient(timeout, policy), url: endpoint,
		secret: strings.TrimSpace(secret), timeout: timeout,
	}, nil
}

// verify reports whether Cloudflare accepted the token.
//
// A refusal from Cloudflare is a false with no error. Anything that stops the
// question being asked -- the deadline, the network, a body that is not the
// documented shape -- is errCaptchaUnavailable, so the caller can say so
// instead of blaming the person who solved the challenge correctly.
func (v *turnstileVerifier) verify(ctx context.Context, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > turnstileTokenMaxBytes {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()
	form := url.Values{"secret": {v.secret}, "response": {token}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url, strings.NewReader(form.Encode()))
	if err != nil {
		return false, errors.Join(errCaptchaUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := v.client.Do(request)
	if err != nil {
		return false, errors.Join(errCaptchaUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: turnstile responded %d", errCaptchaUnavailable, response.StatusCode)
	}
	var body struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, turnstileMaxResponseBytes)).Decode(&body); err != nil {
		return false, errors.Join(errCaptchaUnavailable, err)
	}
	return body.Success, nil
}
