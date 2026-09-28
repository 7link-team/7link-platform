package keyringx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The material here is obviously not key material anybody deployed: each key is
// a byte repeated 32 times. A test that reproduced a plausible-looking secret
// would be a secret in the repository.
const (
	firstKey  = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	secondKey = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
	thirdKey  = "AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="
)

const testDomain = "example:key:"

func newTestRing(t *testing.T, active string, previous ...string) *Keyring {
	t.Helper()
	ring, err := New(testDomain, active, previous...)
	require.NoError(t, err)
	return ring
}

// sealLegacy is the construction every call site in this repository used before
// keys had identities, written out independently so the compatibility test does
// not verify the package against itself. If this and the package ever disagree,
// the package is the one that broke, and every ciphertext already in a database
// is what breaks with it.
func sealLegacy(t *testing.T, domain, encodedKey, purpose string, associatedData, plain []byte) string {
	t.Helper()
	material, err := base64.StdEncoding.DecodeString(encodedKey)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(domain + purpose))

	block, err := aes.NewCipher(mac.Sum(nil))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = rand.Read(nonce)
	require.NoError(t, err)
	return "v1." + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, associatedData))
}

// macLegacy is the other half of the same story: a stored authenticator has to
// keep matching, or every outstanding invitation and recovery code stops being
// recognized the moment this package lands.
func macLegacy(t *testing.T, domain, encodedKey, purpose string, message []byte) string {
	t.Helper()
	material, err := base64.StdEncoding.DecodeString(encodedKey)
	require.NoError(t, err)
	subkey := hmac.New(sha256.New, material)
	_, _ = subkey.Write([]byte(domain + purpose))
	mac := hmac.New(sha256.New, subkey.Sum(nil))
	_, _ = mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// deployedUses names every domain and purpose this repository already has
// ciphertexts or authenticators stored under. They are listed here rather than
// left to the modules that adopt the ring because a drift in the derivation
// does not fail at the call site -- it fails in a database nobody can read any
// more, and by then the code that caused it is long merged.
var deployedUses = map[string]struct {
	domain  string
	purpose string
}{
	"federation client secrets": {domain: "federation:key:", purpose: "client-secret"},
	"federation login state":    {domain: "federation:key:", purpose: "state"},
	"the SAML signing key":      {domain: "federation:key:", purpose: "saml-key"},
	"SCIM bearer tokens":        {domain: "provisioning:key:", purpose: "scim-target-bearer"},
	"MFA secrets":               {domain: "platform:mfa:key:", purpose: "encryption:v1"},
	"invitation tokens":         {domain: "platform:mfa:key:", purpose: "invitation:v1"},
	"recovery codes":            {domain: "platform:mfa:key:", purpose: "recovery:v1"},
	"password recovery tokens":  {domain: "platform:mfa:key:", purpose: "password-recovery:v1"},
	"email verification tokens": {domain: "platform:mfa:key:", purpose: "email-verification:v1"},
}

// TestARingOpensWhatTheSingleKeyItReplacedSealed is the reason this package can
// be adopted at all. Every deployment already holds ciphertexts written by the
// construction it replaces, and a ring that could not read them would turn an
// upgrade into data loss.
func TestARingOpensWhatTheSingleKeyItReplacedSealed(t *testing.T) {
	for name, use := range deployedUses {
		t.Run(name, func(t *testing.T) {
			ring, err := New(use.domain, firstKey)
			require.NoError(t, err)
			associated := []byte("example:secret\x0042")
			legacy := sealLegacy(t, use.domain, firstKey, use.purpose, associated, []byte("hunter2"))

			opened, err := ring.Open(use.purpose, associated, legacy)
			require.NoError(t, err)
			assert.Equal(t, "hunter2", string(opened))
		})
	}
}

// TestARingDerivesTheSameAuthenticatorTheSingleKeyDid keeps stored MACs
// matching. They are lookup columns, so a changed derivation does not fail
// loudly -- it silently stops finding rows that are still there.
func TestARingDerivesTheSameAuthenticatorTheSingleKeyDid(t *testing.T) {
	message := []byte("invitation-token")
	for name, use := range deployedUses {
		t.Run(name, func(t *testing.T) {
			ring, err := New(use.domain, firstKey)
			require.NoError(t, err)

			assert.Equal(t,
				macLegacy(t, use.domain, firstKey, use.purpose, message),
				ring.MAC(use.purpose, message))
		})
	}
}

// TestRotationKeepsTheOldValuesReadableWhileNewOnesMoveOn walks the whole point
// of the package: the day a key changes, nothing that already exists breaks,
// and everything written afterwards is on the new key.
func TestRotationKeepsTheOldValuesReadableWhileNewOnesMoveOn(t *testing.T) {
	before := newTestRing(t, firstKey)
	associated := []byte("example:secret\x0042")
	old, err := before.Seal("client-secret", associated, []byte("hunter2"))
	require.NoError(t, err)

	// The rotation itself: the new key seals, the old one is kept only to open.
	after := newTestRing(t, secondKey, firstKey)

	opened, err := after.Open("client-secret", associated, old)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", string(opened))
	assert.True(t, after.NeedsRewrap(old), "a value on the previous key is what retirement waits for")

	fresh, err := after.Seal("client-secret", associated, []byte("hunter2"))
	require.NoError(t, err)
	assert.False(t, after.NeedsRewrap(fresh))
	sealedBy, ok := after.SealedBy(fresh)
	require.True(t, ok)
	assert.Equal(t, after.ActiveID(), sealedBy)

	// Retirement: once nothing is left on the old key, the ring holds only the
	// new one and the rewrapped value is still readable.
	retired := newTestRing(t, secondKey)
	opened, err = retired.Open("client-secret", associated, fresh)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", string(opened))
}

// TestAValueOnARetiredKeyReportsTheKeyRatherThanCorruption is the failure an
// operator hits when they drop a key one deploy too early. Reporting it as
// undecryptable would send them looking for data corruption; naming the key
// sends them to the configuration line they deleted.
func TestAValueOnARetiredKeyReportsTheKeyRatherThanCorruption(t *testing.T) {
	sealed, err := newTestRing(t, firstKey).Seal("client-secret", nil, []byte("hunter2"))
	require.NoError(t, err)

	_, err = newTestRing(t, secondKey).Open("client-secret", nil, sealed)
	assert.ErrorIs(t, err, ErrUnknownKey)
}

// TestALegacyValueNoKeyInTheRingSealedIsRefused covers the same mistake for the
// values that name no key: with nothing to look up, the ring can only try, and
// running out of keys has to be a refusal rather than a guess.
func TestALegacyValueNoKeyInTheRingSealedIsRefused(t *testing.T) {
	legacy := sealLegacy(t, testDomain, thirdKey, "client-secret", nil, []byte("hunter2"))

	_, err := newTestRing(t, firstKey, secondKey).Open("client-secret", nil, legacy)
	assert.ErrorIs(t, err, ErrOpen)
}

// TestOpeningIsBoundToPurposeDomainAndRow keeps a ciphertext from being read
// anywhere other than where it belongs. Each of these would otherwise let a
// stored secret be lifted into a context that was never allowed to see it.
func TestOpeningIsBoundToPurposeDomainAndRow(t *testing.T) {
	ring := newTestRing(t, firstKey)
	sealed, err := ring.Seal("client-secret", []byte("row-42"), []byte("hunter2"))
	require.NoError(t, err)

	t.Run("another purpose cannot read it", func(t *testing.T) {
		_, err := ring.Open("scim-target-bearer", []byte("row-42"), sealed)
		assert.ErrorIs(t, err, ErrOpen)
	})
	t.Run("another row cannot read it", func(t *testing.T) {
		_, err := ring.Open("client-secret", []byte("row-43"), sealed)
		assert.ErrorIs(t, err, ErrOpen)
	})
	t.Run("the same key under another domain cannot read it", func(t *testing.T) {
		other, err := New("other:key:", firstKey)
		require.NoError(t, err)
		_, err = other.Open("client-secret", []byte("row-42"), sealed)
		// The identity is domain-derived too, so the other ring does not even
		// recognize the key the envelope names.
		assert.ErrorIs(t, err, ErrUnknownKey)
	})
}

// TestMalformedValuesAreRefusedRatherThanDecoded keeps anything that is not an
// envelope out of the cipher.
func TestMalformedValuesAreRefusedRatherThanDecoded(t *testing.T) {
	ring := newTestRing(t, firstKey, secondKey)

	for name, envelope := range map[string]string{
		"nothing at all":      "",
		"no version at all":   "bare-value",
		"a version we do not": "v9.abcd",
	} {
		t.Run(name+" is unsupported", func(t *testing.T) {
			_, err := ring.Open("client-secret", nil, envelope)
			assert.ErrorIs(t, err, ErrUnsupportedEnvelope)
			_, ok := ring.SealedBy(envelope)
			assert.False(t, ok)
			assert.False(t, ring.NeedsRewrap(envelope))
		})
	}

	for name, envelope := range map[string]string{
		"a legacy envelope with no payload":   "v1.",
		"a keyed envelope with no key":        "v2.payload",
		"a keyed envelope with no payload":    "v2.0011223344556677.",
		"a legacy payload that is not base64": "v1.not base64",
	} {
		t.Run(name+" is invalid", func(t *testing.T) {
			_, err := ring.Open("client-secret", nil, envelope)
			assert.ErrorIs(t, err, ErrInvalidEnvelope)
		})
	}

	// A malformed envelope is not work a rewrap can finish, so it must not be
	// counted as outstanding: a retirement waiting on a row no rewrap will ever
	// clear is a retirement that never happens.
	for name, envelope := range map[string]string{
		"a keyed envelope with no key":     "v2.payload",
		"a keyed envelope with no payload": "v2.0011223344556677.",
	} {
		t.Run(name+" is not rewrappable", func(t *testing.T) {
			_, ok := ring.SealedBy(envelope)
			assert.False(t, ok)
			assert.False(t, ring.NeedsRewrap(envelope))
		})
	}

	t.Run("a keyed payload that is not base64", func(t *testing.T) {
		sealed, err := ring.Seal("client-secret", nil, []byte("hunter2"))
		require.NoError(t, err)
		version, rest, _ := strings.Cut(sealed, ".")
		id, _, _ := strings.Cut(rest, ".")
		_, err = ring.Open("client-secret", nil, version+"."+id+".not base64")
		assert.ErrorIs(t, err, ErrInvalidEnvelope)
	})

	t.Run("a payload too short to hold a nonce", func(t *testing.T) {
		short := base64.RawURLEncoding.EncodeToString([]byte("tiny"))
		_, err := ring.Open("client-secret", nil, "v1."+short)
		assert.ErrorIs(t, err, ErrInvalidEnvelope)
	})
}

// TestALegacyValueIsAlwaysOutstandingWork keeps a rewrap from declaring itself
// finished while values that name no key are still in the table. They are
// exactly the ones that pin the oldest key in place.
func TestALegacyValueIsAlwaysOutstandingWork(t *testing.T) {
	ring := newTestRing(t, firstKey)
	legacy := sealLegacy(t, testDomain, firstKey, "client-secret", nil, []byte("hunter2"))

	id, ok := ring.SealedBy(legacy)
	require.True(t, ok)
	assert.Empty(t, id, "a legacy envelope names no key, which is a fact rather than a failure")
	assert.True(t, ring.NeedsRewrap(legacy), "even on the same key, it has to be rewritten to name one")
}

// TestAuthenticatorsFollowTheWholeRing covers the half of rotation that is not
// encryption: a token issued under the previous key still has to be recognized,
// and a new one still has to be written under the current key.
func TestAuthenticatorsFollowTheWholeRing(t *testing.T) {
	message := []byte("invitation-token")
	before := newTestRing(t, firstKey)
	issued := before.MAC("invitation:v1", message)

	after := newTestRing(t, secondKey, firstKey)

	assert.NotEqual(t, issued, after.MAC("invitation:v1", message),
		"a new token is written under the key that is current now")
	assert.True(t, after.MACMatches("invitation:v1", message, issued),
		"a token issued before the rotation still has to be recognized")

	// A lookup cannot compare, so the ring hands over every candidate in the
	// order they are worth trying.
	assert.Equal(t,
		[]string{after.MAC("invitation:v1", message), issued},
		after.MACs("invitation:v1", message))
}

// TestAnAuthenticatorNoKeyProducedIsRefused keeps a forged or stale value from
// matching, including one that is not an authenticator at all.
func TestAnAuthenticatorNoKeyProducedIsRefused(t *testing.T) {
	ring := newTestRing(t, firstKey, secondKey)
	message := []byte("invitation-token")

	for name, want := range map[string]string{
		"a value another key produced": macLegacy(t, testDomain, thirdKey, "invitation:v1", message),
		"a value for another purpose":  ring.MAC("recovery:v1", message),
		"a value that is not hex":      "not hex",
		"nothing at all":               "",
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, ring.MACMatches("invitation:v1", message, want))
		})
	}
}

// TestARingRefusesConfigurationThatCannotMeanWhatItLooksLike fails the
// deployment at startup, which is the only moment an operator is still
// watching. A key problem discovered at the first login is discovered by a user.
func TestARingRefusesConfigurationThatCannotMeanWhatItLooksLike(t *testing.T) {
	tooShort := base64.StdEncoding.EncodeToString(make([]byte, KeySize-1))

	t.Run("no domain", func(t *testing.T) {
		_, err := New("", firstKey)
		require.Error(t, err)
	})
	t.Run("an active key that is not base64", func(t *testing.T) {
		_, err := New(testDomain, "not base64!")
		require.ErrorContains(t, err, "active key")
	})
	t.Run("an active key of the wrong length", func(t *testing.T) {
		_, err := New(testDomain, tooShort)
		require.ErrorContains(t, err, "must decode to 32 bytes")
	})
	t.Run("a previous key that is not base64", func(t *testing.T) {
		_, err := New(testDomain, firstKey, secondKey, "not base64!")
		require.ErrorContains(t, err, "previous key 2")
	})
	t.Run("a previous key that repeats the active one", func(t *testing.T) {
		_, err := New(testDomain, firstKey, firstKey)
		require.ErrorContains(t, err, "repeats a key the ring already holds")
	})
	t.Run("a previous key that repeats another previous one", func(t *testing.T) {
		_, err := New(testDomain, firstKey, secondKey, secondKey)
		require.ErrorContains(t, err, "previous key 2")
	})

	// Nothing in any of those messages may be the key itself: an operator reads
	// a startup failure out of a log, and a log is not where a key belongs.
	for _, err := range []error{
		mustFail(t, testDomain, "not base64!"),
		mustFail(t, testDomain, tooShort),
		mustFail(t, testDomain, firstKey, firstKey),
	} {
		for _, secret := range []string{firstKey, tooShort, "not base64!"} {
			assert.NotContains(t, err.Error(), secret)
		}
	}
}

func mustFail(t *testing.T, domain, active string, previous ...string) error {
	t.Helper()
	_, err := New(domain, active, previous...)
	require.Error(t, err)
	return err
}

// TestABlankEntryConfiguresNothing keeps a placeholder line in a values file
// from failing a deployment that is otherwise correct.
func TestABlankEntryConfiguresNothing(t *testing.T) {
	ring := newTestRing(t, firstKey, "", "   ", secondKey)

	assert.Equal(t, []string{ring.ActiveID(), ring.IDs()[1]}, ring.IDs())
	assert.Len(t, ring.IDs(), 2)
}

// TestAKeyIsReadInEverySpellingDeploymentsAlreadyUse keeps this package from
// refusing a key the parsers it replaces would have taken. Base64 has four
// spellings in the standard library, all four were accepted before, and a
// deployment whose key stops loading has not changed anything about its key.
func TestAKeyIsReadInEverySpellingDeploymentsAlreadyUse(t *testing.T) {
	material, err := base64.StdEncoding.DecodeString(firstKey)
	require.NoError(t, err)

	for name, encoding := range map[string]*base64.Encoding{
		"standard":          base64.StdEncoding,
		"standard unpadded": base64.RawStdEncoding,
		"url-safe":          base64.URLEncoding,
		"url-safe unpadded": base64.RawURLEncoding,
	} {
		t.Run(name, func(t *testing.T) {
			ring, err := New(testDomain, encoding.EncodeToString(material))
			require.NoError(t, err)

			// The same key however it was written is the same key. The identity
			// an envelope records must not depend on how configuration spelled
			// it, or rewriting a values file would strand every stored value.
			assert.Equal(t, newTestRing(t, firstKey).ActiveID(), ring.ActiveID())
		})
	}

	t.Run("a previous key too", func(t *testing.T) {
		retired, err := base64.StdEncoding.DecodeString(secondKey)
		require.NoError(t, err)

		ring, err := New(testDomain, firstKey, base64.RawURLEncoding.EncodeToString(retired))
		require.NoError(t, err)
		assert.Equal(t, newTestRing(t, firstKey, secondKey).IDs(), ring.IDs())
	})
}

// TestKeyIdentitiesSayNothingAboutTheKeys keeps the identifier an envelope
// carries in the clear from being a usable handle on the secret behind it.
func TestKeyIdentitiesSayNothingAboutTheKeys(t *testing.T) {
	ring := newTestRing(t, firstKey, secondKey)
	ids := ring.IDs()

	require.Len(t, ids, 2)
	assert.NotEqual(t, ids[0], ids[1])
	for _, id := range ids {
		assert.Len(t, id, 16, "an identity is eight bytes of hex")
		decoded, err := hex.DecodeString(id)
		require.NoError(t, err)

		// The identity is domain-separated, so the same key configured for two
		// purposes is not recognizable as the same key from the envelopes.
		other, err := New("other:key:", firstKey)
		require.NoError(t, err)
		assert.NotEqual(t, id, other.ActiveID())

		material, err := base64.StdEncoding.DecodeString(firstKey)
		require.NoError(t, err)
		assert.NotContains(t, string(material), string(decoded))
		digest := sha256.Sum256(material)
		assert.NotEqual(t, hex.EncodeToString(digest[:8]), id,
			"the identity must not be a plain digest of the key")
	}
}
