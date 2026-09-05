package federation

import (
	"7link.com/platform/pkg/extension/keyringx"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// stateCookieName names the cookie by what it holds. A deployment name has
// no place in it: the same build serves every deployment, and a browser that
// still carries the old name simply starts a new login.
const stateCookieName = "federation_state"

// stateAAD binds the sealed state cookie to the purpose it was sealed for, so
// a value lifted from another AEAD that happens to share the key cannot be
// opened here. It is a protocol constant rather than a brand name: changing it
// invalidates every outstanding login, and nothing outside this file may
// restate it.
const stateAAD = "iam:federation:state"

var (
	errUnsupportedState = errors.New("unsupported federation state cookie")
	errInvalidState     = errors.New("invalid federation state cookie")
)

// loginState is the server-side browser state sealed into the federation
// cookie. The random nonce doubles as the OIDC `state` parameter, which makes
// the callback replay-proof and origin-bound without a database table.
type loginState struct {
	ProviderID   string `json:"provider_id"`
	TenantID     string `json:"tenant_id"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ReturnURL    string `json:"return_url"`
	ExpiresAt    int64  `json:"expires_at"`
}

func newLoginState(providerID, tenantID, returnURL string, ttl time.Duration, now time.Time) (loginState, error) {
	nonce, err := randomToken(24)
	if err != nil {
		return loginState{}, err
	}
	verifier, err := randomToken(32)
	if err != nil {
		return loginState{}, err
	}
	return loginState{
		ProviderID: providerID, TenantID: tenantID, Nonce: nonce, CodeVerifier: verifier,
		ReturnURL: returnURL, ExpiresAt: now.UTC().Add(ttl).Unix(),
	}, nil
}

// sealState encrypts and authenticates the login state under the federation key
// ring. The sealed value is safe to store in a browser cookie.
func sealState(keys *keyringx.Keyring, state loginState) (string, error) {
	plain, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode federation state: %w", err)
	}
	sealed, err := keys.Seal("state", []byte(stateAAD), plain)
	if err != nil {
		return "", fmt.Errorf("seal federation state: %w", err)
	}
	return sealed, nil
}

// openState reads a state cookie under any key the ring holds. A cookie sealed
// by a key that has since left the ring is reported as invalid rather than as a
// missing key: state lives for minutes, so the answer is to start the login
// again, and there is nothing for an operator to go and restore.
func openState(keys *keyringx.Keyring, encoded string) (loginState, error) {
	plain, err := keys.Open("state", []byte(stateAAD), encoded)
	if errors.Is(err, keyringx.ErrUnsupportedEnvelope) {
		return loginState{}, errUnsupportedState
	}
	if err != nil {
		return loginState{}, errInvalidState
	}
	var state loginState
	if err := json.Unmarshal(plain, &state); err != nil || state.ProviderID == "" || state.TenantID == "" || state.Nonce == "" || state.CodeVerifier == "" {
		return loginState{}, errInvalidState
	}
	return state, nil
}

// codeChallenge derives the PKCE S256 challenge for a code verifier.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func stateCookie(encoded string, secure bool, maxAge int) string {
	return (&http.Cookie{Name: stateCookieName, Value: encoded, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}).String()
}

func stateClearCookie(secure bool) string {
	return (&http.Cookie{Name: stateCookieName, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1}).String()
}
