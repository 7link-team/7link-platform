package federation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/pkg/extension/keyringx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func testKeyring(t *testing.T, keys ...[]byte) *keyringx.Keyring {
	t.Helper()
	if len(keys) == 0 {
		keys = [][]byte{testKey()}
	}
	return newFederationTestKeyring(t, keys[0], keys[1:]...)
}

// sealStateTheWayTheOldCodeDid reproduces, byte for byte, what this module
// wrote before the key ring existed. It is here so that a change to the
// derivation shows up as a failing test rather than as a deployment whose
// stored values and in-flight cookies have all silently stopped opening.
func sealStateTheWayTheOldCodeDid(t *testing.T, key []byte, state loginState) string {
	t.Helper()
	plain, err := json.Marshal(state)
	require.NoError(t, err)

	mac := hmac.New(sha256.New, key)
	_, err = mac.Write([]byte("federation:key:state"))
	require.NoError(t, err)

	block, err := aes.NewCipher(mac.Sum(nil))
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = rand.Read(nonce)
	require.NoError(t, err)

	return "v1." + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, []byte(stateAAD)))
}

func TestLoginStateSealOpenRoundTrip(t *testing.T) {
	key := testKey()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	state, err := newLoginState("provider-1", "tenant-1", "https://app.example/", 10*time.Minute, now)
	require.NoError(t, err)
	assert.Equal(t, "provider-1", state.ProviderID)
	assert.Equal(t, "tenant-1", state.TenantID)
	assert.NotEmpty(t, state.Nonce)
	assert.NotEmpty(t, state.CodeVerifier)
	assert.Equal(t, now.Add(10*time.Minute).Unix(), state.ExpiresAt)

	keys := testKeyring(t, key)
	sealed, err := sealState(keys, state)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sealed, "v2."+keys.ActiveID()+"."))

	opened, err := openState(keys, sealed)
	require.NoError(t, err)
	assert.Equal(t, state, opened)
}

// TestALoginStateWrittenBeforeTheKeyRingStillOpens is the compatibility this
// change is only safe because of: a browser holding a cookie issued by the
// previous build finishes its login rather than being told to start again.
func TestALoginStateWrittenBeforeTheKeyRingStillOpens(t *testing.T) {
	key := testKey()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	state, err := newLoginState("provider-1", "tenant-1", "https://app.example/", 10*time.Minute, now)
	require.NoError(t, err)

	opened, err := openState(testKeyring(t, key), sealStateTheWayTheOldCodeDid(t, key, state))
	require.NoError(t, err)
	assert.Equal(t, state, opened)
}

// TestALoginStateSealedByARetiredKeyStillOpens covers the window a rotation
// leaves open: cookies handed out minutes before the key changed.
func TestALoginStateSealedByARetiredKeyStillOpens(t *testing.T) {
	retired, replacement := testKey(), []byte("fedcba9876543210fedcba9876543210")
	state, err := newLoginState("provider-1", "tenant-1", "https://app.example/", time.Minute, time.Now())
	require.NoError(t, err)

	sealed, err := sealState(testKeyring(t, retired), state)
	require.NoError(t, err)

	opened, err := openState(testKeyring(t, replacement, retired), sealed)
	require.NoError(t, err)
	assert.Equal(t, state, opened)

	// Once the retired key leaves the ring the cookie is simply invalid. State
	// lives for minutes, so starting the login again is the whole remedy.
	_, err = openState(testKeyring(t, replacement), sealed)
	assert.ErrorIs(t, err, errInvalidState)
}

func TestLoginStateTamperAndWrongKeyRejected(t *testing.T) {
	key := testKey()
	keys := testKeyring(t, key)
	state, err := newLoginState("provider-1", "tenant-1", "https://app.example/", time.Minute, time.Now())
	require.NoError(t, err)
	sealed, err := sealState(keys, state)
	require.NoError(t, err)

	tampered := sealed[:len(sealed)-3] + "AAA"
	_, err = openState(keys, tampered)
	assert.ErrorIs(t, err, errInvalidState)

	wrong := append([]byte(nil), key...)
	wrong[0] ^= 0xff
	_, err = openState(testKeyring(t, wrong), sealed)
	assert.ErrorIs(t, err, errInvalidState)

	_, err = openState(keys, "v3."+sealed)
	assert.ErrorIs(t, err, errUnsupportedState)

	_, err = openState(keys, "no-version-at-all")
	assert.ErrorIs(t, err, errUnsupportedState)

	_, err = openState(keys, "v1.not-base64!!!")
	assert.ErrorIs(t, err, errInvalidState)
}

func TestCodeChallengeS256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := codeChallenge(verifier)
	assert.Len(t, challenge, 43)
	assert.Equal(t, challenge, codeChallenge(verifier))
	assert.NotEqual(t, challenge, codeChallenge(verifier+"x"))
	raw, err := base64.RawURLEncoding.DecodeString(challenge)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

func TestStateCookies(t *testing.T) {
	cookie := stateCookie("value", true, 600)
	parsed := (&http.Response{Header: http.Header{"Set-Cookie": []string{cookie}}}).Cookies()
	require.Len(t, parsed, 1)
	assert.Equal(t, "value", parsed[0].Value)
	assert.True(t, parsed[0].HttpOnly)
	assert.True(t, parsed[0].Secure)
	assert.Equal(t, 600, parsed[0].MaxAge)

	cleared := stateClearCookie(false)
	parsedClear := (&http.Response{Header: http.Header{"Set-Cookie": []string{cleared}}}).Cookies()
	require.Len(t, parsedClear, 1)
	assert.Equal(t, -1, parsedClear[0].MaxAge)
	assert.False(t, parsedClear[0].Secure)
}

func TestLoginStateOpenRejectsInvalidPayloads(t *testing.T) {
	keys := testKeyring(t)
	_, err := openState(keys, "v1.AA")
	assert.ErrorIs(t, err, errInvalidState)

	state, err := sealState(keys, loginState{ProviderID: "p", ReturnURL: "https://app.example/", ExpiresAt: time.Now().Add(time.Minute).Unix()})
	require.NoError(t, err)
	_, err = openState(keys, state)
	assert.ErrorIs(t, err, errInvalidState)
}
