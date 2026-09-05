package authn

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/secretx"
)

// signingKeyring holds the key tokens are signed with and every key they may
// still be verified with.
//
// Rotating a signing key is not one act but two, separated by time. The new key
// has to be published and accepted before anything is signed with it, and the
// key it replaces has to keep verifying until the last token carrying it has
// expired. Swapping in one step would reject every token already in flight --
// every signed-in user at once, for a reason none of them could see or fix.
//
// So the ring is deliberately asymmetric: exactly one key signs, and any key in
// the ring verifies. A retired key contributes only its public half, because
// nothing should be able to sign with it again even by mistake.
type signingKeyring struct {
	private ed25519.PrivateKey
	active  signingKey
	retired []signingKey
}

// signingKey is one published verification key and the identifier a token
// header names it by.
type signingKey struct {
	id     string
	public ed25519.PublicKey
}

// signingKeyID names a key by its public half rather than by a number an
// operator maintains. A configuration that lists the same key twice, or that
// reintroduces one after retiring it, then collapses to one entry instead of
// publishing a duplicate under a second name.
func signingKeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return hex.EncodeToString(sum[:8])
}

// newSigningKeyring builds the ring from the active key and the retired ones,
// in the order an operator listed them.
//
// A retired key that fails to parse is an error rather than something skipped.
// Dropping it silently would produce a deployment that starts, looks healthy,
// and rejects exactly the tokens the operator added that key to keep working --
// which is the failure a rotation window exists to prevent.
func newSigningKeyring(active string, previous []string) (*signingKeyring, error) {
	key, err := parseSigningKey(active)
	if err != nil {
		return nil, err
	}
	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("authentication signing key has an invalid public key")
	}
	ring := &signingKeyring{
		private: key,
		active:  signingKey{id: signingKeyID(public), public: public},
	}
	for index, encoded := range previous {
		if strings.TrimSpace(encoded) == "" {
			continue
		}
		retired, err := parseSigningKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("authn previous signing key %d: %w", index+1, err)
		}
		retiredPublic, ok := retired.Public().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("authn previous signing key %d has an invalid public key", index+1)
		}
		id := signingKeyID(retiredPublic)
		if ring.contains(id) {
			continue
		}
		ring.retired = append(ring.retired, signingKey{id: id, public: retiredPublic})
	}
	return ring, nil
}

func (r *signingKeyring) contains(id string) bool {
	if r == nil {
		return false
	}
	if r.active.id == id {
		return true
	}
	for _, key := range r.retired {
		if key.id == id {
			return true
		}
	}
	return false
}

// usable reports whether the ring can sign. A ring that cannot is not a
// degraded one to work around; it means the module was never configured.
func (r *signingKeyring) usable() bool {
	return r != nil && len(r.private) == ed25519.PrivateKeySize
}

// activeID is the identifier written into the header of tokens signed now.
func (r *signingKeyring) activeID() string {
	if r == nil {
		return ""
	}
	return r.active.id
}

// verify checks a signature against the one key the header names.
//
// The key is selected by identifier rather than tried in turn, so a token that
// names a key which has left the ring is rejected outright instead of being
// offered to every remaining key. Trying them all would keep working after the
// retired key was removed, and the removal would then be silently undone by the
// code rather than reported.
func (r *signingKeyring) verify(id string, signed, signature []byte) bool {
	if r == nil {
		return false
	}
	if r.active.id == id {
		return ed25519.Verify(r.active.public, signed, signature)
	}
	for _, key := range r.retired {
		if key.id == id {
			return ed25519.Verify(key.public, signed, signature)
		}
	}
	return false
}

// jwks publishes every key that still verifies, active first.
//
// Both halves of the window depend on this being the whole ring rather than
// just the active key: a client that cached the old document has to find the
// new key before tokens start carrying it, and a client fetching now has to
// still find the old one while tokens carrying it are alive.
func (r *signingKeyring) jwks() map[string]any {
	keys := []map[string]any{}
	if r != nil && len(r.active.public) == ed25519.PublicKeySize {
		keys = append(keys, publishSigningKey(r.active))
		for _, key := range r.retired {
			keys = append(keys, publishSigningKey(key))
		}
	}
	return map[string]any{"keys": keys}
}

func publishSigningKey(key signingKey) map[string]any {
	return map[string]any{
		"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": key.id,
		"x": base64.RawURLEncoding.EncodeToString(key.public),
	}
}

// resolveSigningKeyring loads the signing keys so a misconfigured rotation
// fails at startup rather than at the first request. The alternative is a
// process that starts healthy and rejects a share of live sessions -- the ones
// holding a token signed by the key that failed to load -- which reads like an
// outage with no failing component.
func resolveSigningKeyring(cfg authnext.Config) (*signingKeyring, error) {
	active, err := secretx.Resolve("authn.signing_key", cfg.SigningKey, cfg.SigningKeyFile, 4096)
	if err != nil {
		return nil, err
	}
	previous, err := secretx.ResolveList(
		"authn.previous_signing_keys",
		cfg.PreviousSigningKeys, cfg.PreviousSigningKeyFiles, 4096,
	)
	if err != nil {
		return nil, err
	}
	return newSigningKeyring(active, previous)
}
