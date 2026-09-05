package authn

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSigningSeed builds an obviously fake seed that is still distinct per
// marker, so a test about rotation can tell one key from another.
func testSigningSeed(marker string) string {
	return base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat(marker, ed25519.SeedSize)))
}

// TestSigningKeyringSignsWithOneKeyAndVerifiesWithAnyOfThem is the property the
// whole rotation window rests on. Without it, replacing a key would invalidate
// every token already issued -- every signed-in user at once, at the moment the
// new configuration rolls out.
func TestSigningKeyringSignsWithOneKeyAndVerifiesWithAnyOfThem(t *testing.T) {
	retired, err := newSigningKeyring(testSigningSeed("a"), nil)
	require.NoError(t, err)
	ring, err := newSigningKeyring(testSigningSeed("b"), []string{testSigningSeed("a")})
	require.NoError(t, err)

	assert.NotEqual(t, retired.activeID(), ring.activeID(),
		"two different keys have to be published under two different identifiers")
	assert.True(t, ring.usable())

	signed := []byte("header.payload")
	// A signature the retired key made, which is what an unexpired token in
	// somebody's browser carries right after a rotation.
	old := ed25519.Sign(retired.private, signed)
	assert.True(t, ring.verify(retired.activeID(), signed, old),
		"a token issued before the rotation must still be accepted")

	// Signing still happens with one key only.
	fresh := ed25519.Sign(ring.private, signed)
	assert.True(t, ring.verify(ring.activeID(), signed, fresh))
	assert.False(t, ring.verify(retired.activeID(), signed, fresh),
		"a signature may only verify under the key that actually made it")
}

// TestSigningKeyringPublishesEveryKeyThatStillVerifies covers the half of the
// window a relying party sees. A document that advertised only the active key
// would leave a cached client unable to verify the tokens it is being handed.
func TestSigningKeyringPublishesEveryKeyThatStillVerifies(t *testing.T) {
	ring, err := newSigningKeyring(testSigningSeed("b"), []string{testSigningSeed("a")})
	require.NoError(t, err)

	document := ring.jwks()
	keys, ok := document["keys"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, keys, 2)
	assert.Equal(t, ring.activeID(), keys[0]["kid"], "the signing key is published first")
	for _, key := range keys {
		assert.Equal(t, "OKP", key["kty"])
		assert.Equal(t, "Ed25519", key["crv"])
		assert.Equal(t, "EdDSA", key["alg"])
		assert.Equal(t, "sig", key["use"])
		assert.NotEmpty(t, key["x"])
	}
	assert.NotEqual(t, keys[0]["kid"], keys[1]["kid"])

	// A ring nobody configured publishes an empty set rather than a key with no
	// bytes in it, which a relying party would try to use and fail on.
	empty := (*signingKeyring)(nil).jwks()
	assert.Empty(t, empty["keys"])
	assert.False(t, (*signingKeyring)(nil).usable())
	assert.Empty(t, (*signingKeyring)(nil).activeID())
	assert.False(t, (*signingKeyring)(nil).verify("any", nil, nil))
	assert.False(t, (*signingKeyring)(nil).contains("any"))
}

// TestSigningKeyringRefusesAKeyItCannotRead keeps a typo in a retired key from
// producing a deployment that starts, reports healthy, and rejects exactly the
// tokens that key was listed to keep working.
func TestSigningKeyringRefusesAKeyItCannotRead(t *testing.T) {
	_, err := newSigningKeyring("", nil)
	assert.Error(t, err, "there is no signing without a signing key")

	_, err = newSigningKeyring(testSigningSeed("b"), []string{"not-a-key"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "previous signing key 1",
		"the operator has to be told which entry is wrong, not just that one is")
}

// TestSigningKeyringIgnoresARepeatedOrEmptyEntry keeps configuration edits
// forgiving where being strict would buy nothing: a blank line, or the active
// key still listed among the retired ones during a two-step rollout.
func TestSigningKeyringIgnoresARepeatedOrEmptyEntry(t *testing.T) {
	active := testSigningSeed("b")
	ring, err := newSigningKeyring(active, []string{"  ", active, testSigningSeed("a"), testSigningSeed("a")})
	require.NoError(t, err)

	keys, ok := ring.jwks()["keys"].([]map[string]any)
	require.True(t, ok)
	assert.Len(t, keys, 2, "the same key may not be published twice under two names")
	assert.True(t, ring.contains(ring.activeID()))
}

// TestSigningKeyringRejectsATokenNamingAKeyItNoLongerHolds is what makes taking
// a key out of the configuration mean something. If verification tried every
// key in turn instead of the one named, removing a key would change nothing.
func TestSigningKeyringRejectsATokenNamingAKeyItNoLongerHolds(t *testing.T) {
	removed, err := newSigningKeyring(testSigningSeed("a"), nil)
	require.NoError(t, err)
	ring, err := newSigningKeyring(testSigningSeed("b"), nil)
	require.NoError(t, err)

	signed := []byte("header.payload")
	assert.False(t, ring.verify(removed.activeID(), signed, ed25519.Sign(removed.private, signed)),
		"once the key is out of the configuration its tokens stop being accepted")
	// Naming a key that is present does not help a signature made by another.
	assert.False(t, ring.verify(ring.activeID(), signed, ed25519.Sign(removed.private, signed)))
}

// TestResolveSigningKeyringReportsConfigurationProblemsAtStartup covers the
// wiring into configuration. A secret named two ways is a mistake worth
// stopping for: the deployment would otherwise run on whichever source the code
// happened to prefer, and nobody would learn which one that was.
func TestResolveSigningKeyringReportsConfigurationProblemsAtStartup(t *testing.T) {
	cfg := localAuthnConfig()
	cfg.SigningKeyFile = "also-set"
	_, err := resolveSigningKeyring(cfg)
	assert.ErrorContains(t, err, "mutually exclusive")

	cfg = localAuthnConfig()
	cfg.PreviousSigningKeys = []string{testSigningSeed("a")}
	cfg.PreviousSigningKeyFiles = []string{"also-set"}
	_, err = resolveSigningKeyring(cfg)
	assert.Error(t, err)

	cfg = localAuthnConfig()
	cfg.PreviousSigningKeys = []string{testSigningSeed("a")}
	ring, err := resolveSigningKeyring(cfg)
	require.NoError(t, err)
	assert.Len(t, ring.retired, 1)
}

// TestWebServiceAcceptsTokensIssuedBeforeAKeyRotation is the end-to-end shape of
// the deployment step: the new key is active, the old one is listed, and a
// bearer token minted under the old key is still honored. This is the case that
// decides whether a rotation is invisible to users or logs everyone out.
func TestWebServiceAcceptsTokensIssuedBeforeAKeyRotation(t *testing.T) {
	before, harness := newLocalService(t)

	payload := map[string]any{
		"iss": before.Issuer(), "sub": "subject", "aud": []string{"7link-api"},
		"subject_type": authnext.SubjectTypeService,
		"iat":          before.now().Unix(), "exp": before.now().Add(time.Minute).Unix(),
	}
	token, err := before.sign(payload)
	require.NoError(t, err)

	rotated := localAuthnConfig()
	rotated.SigningKey = testSigningSeed("r")
	rotated.PreviousSigningKeys = []string{localAuthnConfig().SigningKey}
	after, err := NewWebService(rotated, harness.repository)
	require.NoError(t, err)
	after.now = before.now

	claims, err := after.Authenticate(t.Context(), "Bearer "+token, "")
	require.NoError(t, err, "a token issued a minute ago may not be invalidated by a key rotation")
	assert.Equal(t, "subject", claims.Subject)

	// The new service publishes both keys, so a client that refreshes its copy
	// of the document can verify tokens from either side of the rotation.
	keys, ok := after.JWKS()["keys"].([]map[string]any)
	require.True(t, ok)
	assert.Len(t, keys, 2)
	assert.Equal(t, after.signingKeys.activeID(), keys[0]["kid"])

	// New tokens carry the new key, which is what eventually lets the old one
	// be dropped: after one access token lifetime, nothing names it any more.
	fresh, err := after.sign(payload)
	require.NoError(t, err)
	assert.NotEqual(t, token, fresh)
	_, err = before.Authenticate(t.Context(), "Bearer "+fresh, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken,
		"the service that does not hold the new key must not accept its tokens")
}
