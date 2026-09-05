// Package keyringx seals and opens secrets under a key a deployment can replace
// without losing what the previous key protected.
//
// A single configured key makes rotation a flag day: the moment it changes,
// every stored ciphertext stops opening and every derived token stops matching,
// so the safe action and the possible action are different actions and a
// deployment picks the possible one. A ring separates them. One key seals,
// every key in the ring opens, and a key leaves the ring only once nothing
// still depends on it -- which is a question the ring can answer rather than
// one an operator has to guess.
package keyringx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the length every ring key decodes to. The sealed payloads are
// AES-256, and accepting anything shorter would weaken them without saying so.
const KeySize = 32

const (
	// sealedVersion labels envelopes that name the key that produced them.
	sealedVersion = "v2"
	// legacyVersion labels envelopes written before keys had identities. They
	// are opened by trying the ring, which an AEAD makes unambiguous: only the
	// key that sealed a payload authenticates it, so a wrong key cannot return
	// a wrong plaintext, only a failure.
	legacyVersion = "v1"
)

var (
	// ErrUnsupportedEnvelope reports a value that is not a keyring envelope.
	ErrUnsupportedEnvelope = errors.New("unsupported keyring envelope")
	// ErrInvalidEnvelope reports an envelope whose payload is malformed.
	ErrInvalidEnvelope = errors.New("invalid keyring envelope")
	// ErrUnknownKey reports an envelope naming a key the ring does not hold.
	// It is worth telling apart from a failed open: the fix is to put the key
	// back in the configuration, not to accept that the data is gone.
	ErrUnknownKey = errors.New("keyring does not hold the key this value was sealed with")
	// ErrOpen reports an envelope the ring holds a key for but cannot open.
	ErrOpen = errors.New("keyring cannot open this value")
)

// ringKey is one key and the identity envelopes record it by.
type ringKey struct {
	id       string
	material []byte
}

// Keyring is the key a deployment currently seals with plus the keys it can
// still open with. The order is meaningful: the first key is the active one.
type Keyring struct {
	domain string
	keys   []ringKey
}

// New builds a ring from base64-encoded keys. active is the key that seals;
// active and previous both open.
//
// domain is prepended verbatim to every purpose before the subkey is derived,
// so it is a literal prefix rather than a namespace with an implied separator.
// Two rings given the same key material under different domains derive
// different subkeys and cannot open values sealed by the other.
func New(domain, active string, previous ...string) (*Keyring, error) {
	if domain == "" {
		return nil, errors.New("keyring domain must not be empty")
	}
	activeMaterial, err := decodeKey(domain, "active key", active)
	if err != nil {
		return nil, err
	}
	ring := &Keyring{domain: domain, keys: []ringKey{newRingKey(domain, activeMaterial)}}
	for index, encoded := range previous {
		// A blank entry configures nothing. Deployment configuration grows
		// placeholder list entries, and failing on one would make an operator
		// delete a line rather than think about the key.
		if strings.TrimSpace(encoded) == "" {
			continue
		}
		key, err := decodeRingKey(domain, fmt.Sprintf("previous key %d", index+1), encoded)
		if err != nil {
			return nil, err
		}
		// A repeat is reported by position rather than by value, because the
		// value is the secret. An operator who listed the active key again
		// believes they have rotated and have not.
		if _, held := ring.find(key.id); held {
			return nil, fmt.Errorf("previous key %d repeats a key the ring already holds", index+1)
		}
		ring.keys = append(ring.keys, key)
	}
	return ring, nil
}

// ActiveID names the key that seals. Everything sealed from now on records it.
func (r *Keyring) ActiveID() string { return r.keys[0].id }

// IDs names every key the ring holds, the active one first.
func (r *Keyring) IDs() []string {
	ids := make([]string, len(r.keys))
	for index, key := range r.keys {
		ids[index] = key.id
	}
	return ids
}

// Seal encrypts and authenticates plain under the active key. purpose separates
// one use of the ring from another, and associatedData binds the result to the
// row it belongs to, so a ciphertext moved to a different row stops opening.
func (r *Keyring) Seal(purpose string, associatedData, plain []byte) (string, error) {
	active := r.keys[0]
	gcm, err := r.aead(active, purpose)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate keyring nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plain, associatedData)
	return sealedVersion + "." + active.id + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts an envelope sealed by any key the ring holds. An envelope that
// names its key goes straight to that key; one written before keys had
// identities is tried against the whole ring.
func (r *Keyring) Open(purpose string, associatedData []byte, envelope string) ([]byte, error) {
	version, rest, ok := strings.Cut(envelope, ".")
	if !ok {
		return nil, ErrUnsupportedEnvelope
	}
	switch version {
	case sealedVersion:
		id, payload, split := strings.Cut(rest, ".")
		if !split || payload == "" {
			return nil, ErrInvalidEnvelope
		}
		key, held := r.find(id)
		if !held {
			return nil, ErrUnknownKey
		}
		return r.open(key, purpose, associatedData, payload)
	case legacyVersion:
		if rest == "" {
			return nil, ErrInvalidEnvelope
		}
		for _, key := range r.keys {
			plain, err := r.open(key, purpose, associatedData, rest)
			if err == nil {
				return plain, nil
			}
			// A malformed payload is malformed for every key, so there is
			// nothing to gain by trying the rest of the ring.
			if errors.Is(err, ErrInvalidEnvelope) {
				return nil, err
			}
		}
		return nil, ErrOpen
	default:
		return nil, ErrUnsupportedEnvelope
	}
}

// SealedBy names the key an envelope records. An envelope written before keys
// had identities names none, which is reported as an empty identifier rather
// than an error: it is a fact about the value, and whoever is deciding what
// still has to be rewrapped needs it as much as a real identifier.
func (r *Keyring) SealedBy(envelope string) (string, bool) {
	version, rest, ok := strings.Cut(envelope, ".")
	if !ok {
		return "", false
	}
	switch version {
	case sealedVersion:
		id, payload, split := strings.Cut(rest, ".")
		if !split || payload == "" {
			return "", false
		}
		return id, true
	case legacyVersion:
		if rest == "" {
			return "", false
		}
		return "", true
	default:
		return "", false
	}
}

// NeedsRewrap reports whether an envelope is held by anything other than the
// key that currently seals. It is the question retirement asks: a key may leave
// the ring once nothing answers true on its account.
//
// A value that is not an envelope answers false. Rewrapping cannot repair one,
// and reporting it as outstanding work would leave a retirement waiting on
// something no rewrap will ever clear.
func (r *Keyring) NeedsRewrap(envelope string) bool {
	id, ok := r.SealedBy(envelope)
	return ok && id != r.ActiveID()
}

// MAC derives the authenticator to store for a message, under the active key.
func (r *Keyring) MAC(purpose string, message []byte) string {
	return hex.EncodeToString(r.mac(r.keys[0], purpose, message))
}

// MACs derives the authenticator under every key, the active one first. A
// stored MAC is often the column a row is looked up by rather than a value
// compared after the fact, and a lookup can be neither constant time nor
// key-agnostic: it has to be tried.
func (r *Keyring) MACs(purpose string, message []byte) []string {
	macs := make([]string, len(r.keys))
	for index, key := range r.keys {
		macs[index] = hex.EncodeToString(r.mac(key, purpose, message))
	}
	return macs
}

// MACMatches reports whether want authenticates message under any key the ring
// holds. The comparison is constant time and the whole ring is always walked,
// so neither the answer nor the time it took says which key was involved.
func (r *Keyring) MACMatches(purpose string, message []byte, want string) bool {
	decoded, err := hex.DecodeString(want)
	if err != nil {
		return false
	}
	matched := 0
	for _, key := range r.keys {
		matched |= subtle.ConstantTimeCompare(decoded, r.mac(key, purpose, message))
	}
	return matched == 1
}

func (r *Keyring) mac(key ringKey, purpose string, message []byte) []byte {
	mac := hmac.New(sha256.New, r.subkey(key, purpose))
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func (r *Keyring) open(key ringKey, purpose string, associatedData []byte, payload string) ([]byte, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrInvalidEnvelope
	}
	gcm, err := r.aead(key, purpose)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, ErrInvalidEnvelope
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], associatedData)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}

func (r *Keyring) aead(key ringKey, purpose string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(r.subkey(key, purpose))
	if err != nil {
		return nil, fmt.Errorf("create keyring cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create keyring AEAD: %w", err)
	}
	return gcm, nil
}

// subkey derives the key one purpose uses. The derivation is
// HMAC-SHA256(material, domain+purpose) because that is what the call sites
// this package replaced already did, and every stored ciphertext and every
// stored MAC depends on it byte for byte.
func (r *Keyring) subkey(key ringKey, purpose string) []byte {
	mac := hmac.New(sha256.New, key.material)
	_, _ = mac.Write([]byte(r.domain + purpose))
	return mac.Sum(nil)
}

func (r *Keyring) find(id string) (ringKey, bool) {
	for _, key := range r.keys {
		if key.id == id {
			return key, true
		}
	}
	return ringKey{}, false
}

func decodeRingKey(domain, name, encoded string) (ringKey, error) {
	material, err := decodeKey(domain, name, encoded)
	if err != nil {
		return ringKey{}, err
	}
	return newRingKey(domain, material), nil
}

// newRingKey derives the identity an envelope records a key by. It is derived
// from the key rather than configured, so an operator cannot mislabel one, and
// it is an HMAC under a fixed label rather than a digest of the key itself, so
// the identity an envelope carries in the clear is not a shortened hash of the
// secret that produced it.
func newRingKey(domain string, material []byte) ringKey {
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte("keyringx:key-id\x00" + domain))
	return ringKey{id: hex.EncodeToString(mac.Sum(nil)[:8]), material: material}
}

// keyEncodings are the base64 spellings a configured key may arrive in. All
// four are accepted because all four were accepted before this package existed,
// and a deployment whose key stops loading has not misconfigured anything --
// the software changed under it.
var keyEncodings = []*base64.Encoding{
	base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding,
}

// decodeKey reads one configured key. name is a position rather than a value,
// because saying which key is wrong must not say what it was. The message names
// no configuration key either: the caller knows what its keys are called and
// wraps accordingly, and the derivation domain is not a name anyone configured.
func decodeKey(domain, name, encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	decodedLength, readable := 0, false
	for _, encoding := range keyEncodings {
		material, err := encoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		if len(material) == KeySize {
			return material, nil
		}
		decodedLength, readable = len(material), true
	}
	// The length is worth reporting and safe to report: it is the difference
	// between a key that was truncated in transit and one that was never
	// base64 to begin with, and those have different fixes.
	if readable {
		return nil, fmt.Errorf("%s must decode to %d bytes, got %d", name, KeySize, decodedLength)
	}
	return nil, fmt.Errorf("%s must be base64-encoded", name)
}
