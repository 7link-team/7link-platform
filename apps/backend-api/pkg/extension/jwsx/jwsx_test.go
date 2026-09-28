package jwsx

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signers struct {
	rsa     *rsa.PrivateKey
	ec256   *ecdsa.PrivateKey
	ec384   *ecdsa.PrivateKey
	ec521   *ecdsa.PrivateKey
	edPub   ed25519.PublicKey
	edPriv  ed25519.PrivateKey
	rsaDER  []byte
	signing []byte
}

func newSigners(t *testing.T) signers {
	t.Helper()
	var s signers
	var err error
	s.rsa, err = rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	s.ec256, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	s.ec384, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)
	s.ec521, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	require.NoError(t, err)
	s.edPub, s.edPriv, err = ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s.rsaDER, err = x509.MarshalPKIXPublicKey(&s.rsa.PublicKey)
	require.NoError(t, err)
	s.signing = []byte("eyJhbGciOiJ4In0.eyJzdWIiOiJ4In0")
	return s
}

func digest(hash crypto.Hash, data []byte) []byte {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil)
}

func (s signers) rsaPKCS1(t *testing.T, hash crypto.Hash) []byte {
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.rsa, hash, digest(hash, s.signing))
	require.NoError(t, err)
	return sig
}

func (s signers) ecdsaRaw(t *testing.T, key *ecdsa.PrivateKey, hash crypto.Hash) []byte {
	r, sig, err := ecdsa.Sign(rand.Reader, key, digest(hash, s.signing))
	require.NoError(t, err)
	size := (key.Curve.Params().BitSize + 7) / 8
	return append(r.FillBytes(make([]byte, size)), sig.FillBytes(make([]byte, size))...)
}

func (s signers) hmacWithRSAPublicKey(hash crypto.Hash) []byte {
	mac := hmac.New(hash.New, s.rsaDER)
	mac.Write(s.signing)
	return mac.Sum(nil)
}

// signedCase is a signature that is genuinely valid for its algorithm and
// key, so a rejection can only come from the algorithm policy.
type signedCase struct {
	alg       string
	key       crypto.PublicKey
	signature []byte
}

func validSignatures(t *testing.T, s signers) []signedCase {
	pss, err := rsa.SignPSS(rand.Reader, s.rsa, crypto.SHA256, digest(crypto.SHA256, s.signing), nil)
	require.NoError(t, err)
	return []signedCase{
		{RS256, &s.rsa.PublicKey, s.rsaPKCS1(t, crypto.SHA256)},
		{"RS384", &s.rsa.PublicKey, s.rsaPKCS1(t, crypto.SHA384)},
		{RS512, &s.rsa.PublicKey, s.rsaPKCS1(t, crypto.SHA512)},
		{"PS256", &s.rsa.PublicKey, pss},
		{ES256, &s.ec256.PublicKey, s.ecdsaRaw(t, s.ec256, crypto.SHA256)},
		{"ES384", &s.ec384.PublicKey, s.ecdsaRaw(t, s.ec384, crypto.SHA384)},
		{ES512, &s.ec521.PublicKey, s.ecdsaRaw(t, s.ec521, crypto.SHA512)},
		{EdDSA, s.edPub, ed25519.Sign(s.edPriv, s.signing)},
		// Algorithm confusion: an HMAC "keyed" with the RSA public key, the
		// classic forgery against verifiers that trust the header's alg.
		{"HS256", &s.rsa.PublicKey, s.hmacWithRSAPublicKey(crypto.SHA256)},
		{"HS384", &s.rsa.PublicKey, s.hmacWithRSAPublicKey(crypto.SHA384)},
		{"HS512", &s.rsa.PublicKey, s.hmacWithRSAPublicKey(crypto.SHA512)},
		{"none", &s.rsa.PublicKey, nil},
		{"", &s.rsa.PublicKey, nil},
		{"rs256", &s.rsa.PublicKey, s.rsaPKCS1(t, crypto.SHA256)},
		{"eddsa", s.edPub, ed25519.Sign(s.edPriv, s.signing)},
	}
}

// TestAlgorithmPolicyInvariant is the whole trust matrix: every algorithm,
// each validly signed, against every verifier configuration. A signature is
// accepted exactly when the configuration lists its algorithm and this
// package implements it -- a configuration can narrow the implemented set but
// never widen it, so "none" and HMAC stay refused even when listed.
func TestAlgorithmPolicyInvariant(t *testing.T) {
	s := newSigners(t)
	cases := validSignatures(t, s)
	implemented := []string{RS256, RS512, ES256, ES512, EdDSA}
	configurations := map[string]Algorithms{
		"platform access token": {RS256, EdDSA},
		"federation ID token":   {RS256, RS512, ES256, ES512, EdDSA},
		"empty":                 {},
		"nil":                   nil,
		"unimplemented only":    {"none", "HS256", "HS384", "HS512", "PS256", "RS384", "ES384", ""},
		"everything listed": {
			RS256, "RS384", RS512, "PS256", ES256, "ES384", ES512, EdDSA,
			"HS256", "HS384", "HS512", "none", "", "rs256", "eddsa",
		},
	}
	for name, allowed := range configurations {
		for _, c := range cases {
			want := slices.Contains(allowed, c.alg) && slices.Contains(implemented, c.alg)
			assert.Equal(t, want, allowed.Allows(c.alg), "%s allows %q", name, c.alg)
			err := allowed.Verify(c.alg, c.key, s.signing, c.signature)
			if want {
				assert.NoError(t, err, "%s must accept %q", name, c.alg)
			} else {
				assert.ErrorIs(t, err, ErrUnsupportedAlgorithm, "%s must refuse %q", name, c.alg)
			}
		}
	}
}

// TestVerifyRejectsForgeries covers what a permitted algorithm must still
// refuse: a tampered input or signature, a key of the right type that did not
// sign, and a key of the wrong type for the algorithm.
func TestVerifyRejectsForgeries(t *testing.T) {
	s := newSigners(t)
	other := newSigners(t)
	all := Algorithms{RS256, RS512, ES256, ES512, EdDSA}
	keyOf := map[string]func(signers) crypto.PublicKey{
		RS256: func(k signers) crypto.PublicKey { return &k.rsa.PublicKey },
		RS512: func(k signers) crypto.PublicKey { return &k.rsa.PublicKey },
		ES256: func(k signers) crypto.PublicKey { return &k.ec256.PublicKey },
		ES512: func(k signers) crypto.PublicKey { return &k.ec521.PublicKey },
		EdDSA: func(k signers) crypto.PublicKey { return k.edPub },
	}
	wrongType := map[string]crypto.PublicKey{
		RS256: s.edPub, RS512: &s.ec256.PublicKey,
		ES256: &s.rsa.PublicKey, ES512: s.edPub,
		EdDSA: &s.rsa.PublicKey,
	}
	for _, c := range validSignatures(t, s) {
		if !all.Allows(c.alg) {
			continue
		}
		t.Run(c.alg, func(t *testing.T) {
			require.NoError(t, all.Verify(c.alg, c.key, s.signing, c.signature))

			tamperedInput := append([]byte(nil), s.signing...)
			tamperedInput[len(tamperedInput)-1] ^= 0x01
			assert.ErrorIs(t, all.Verify(c.alg, c.key, tamperedInput, c.signature), ErrSignature)

			tamperedSignature := append([]byte(nil), c.signature...)
			tamperedSignature[len(tamperedSignature)/2] ^= 0x01
			assert.ErrorIs(t, all.Verify(c.alg, c.key, s.signing, tamperedSignature), ErrSignature)

			assert.ErrorIs(t, all.Verify(c.alg, c.key, s.signing, c.signature[:len(c.signature)-1]), ErrSignature)
			assert.ErrorIs(t, all.Verify(c.alg, c.key, s.signing, nil), ErrSignature)

			assert.ErrorIs(t, all.Verify(c.alg, keyOf[c.alg](other), s.signing, c.signature), ErrSignature,
				"a key that did not sign must not verify")
			assert.ErrorIs(t, all.Verify(c.alg, wrongType[c.alg], s.signing, c.signature), ErrKeyType)
			assert.ErrorIs(t, all.Verify(c.alg, nil, s.signing, c.signature), ErrKeyType)
		})
	}
}

func TestParse(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	token, err := Parse(enc([]byte(`{"alg":"EdDSA","kid":"k1","typ":"JWT"}`)) + "." + enc([]byte(`{"sub":"x"}`)) + "." + enc([]byte("sig")))
	require.NoError(t, err)
	assert.Equal(t, Header{Alg: "EdDSA", Kid: "k1", Typ: "JWT"}, token.Header)
	assert.JSONEq(t, `{"sub":"x"}`, string(token.Payload))
	assert.Equal(t, []byte("sig"), token.Signature)
	assert.Equal(t, enc([]byte(`{"alg":"EdDSA","kid":"k1","typ":"JWT"}`))+"."+enc([]byte(`{"sub":"x"}`)), string(token.SigningInput))

	header := enc([]byte(`{"alg":"RS256"}`))
	for name, raw := range map[string]string{
		"empty":                     "",
		"two segments":              header + ".e30",
		"four segments":             header + ".e30.c2ln.c2ln",
		"header not base64url":      "!!." + "e30.c2ln",
		"header padded base64":      header + "=.e30.c2ln",
		"header not JSON":           enc([]byte("alg")) + ".e30.c2ln",
		"header alg not a string":   enc([]byte(`{"alg":1}`)) + ".e30.c2ln",
		"payload not base64url":     header + ".!!.c2ln",
		"signature not base64url":   header + ".e30.!!",
		"signature standard base64": header + ".e30.c2ln+/",
	} {
		_, err := Parse(raw)
		assert.ErrorIs(t, err, ErrMalformed, name)
	}
}

// TestParseThenVerifyBindsSigningInput checks the parts line up: a token
// signed over its own header and payload verifies, and swapping in another
// payload under the same signature does not.
func TestParseThenVerifyBindsSigningInput(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	input := enc([]byte(`{"alg":"EdDSA","kid":"k"}`)) + "." + enc([]byte(`{"sub":"alice"}`))
	signature := enc(ed25519.Sign(private, []byte(input)))
	public := private.Public()

	token, err := Parse(input + "." + signature)
	require.NoError(t, err)
	require.NoError(t, Algorithms{EdDSA}.Verify(token.Header.Alg, public, token.SigningInput, token.Signature))

	swapped, err := Parse(enc([]byte(`{"alg":"EdDSA","kid":"k"}`)) + "." + enc([]byte(`{"sub":"admin"}`)) + "." + signature)
	require.NoError(t, err)
	assert.ErrorIs(t, Algorithms{EdDSA}.Verify(swapped.Header.Alg, public, swapped.SigningInput, swapped.Signature), ErrSignature)
}
