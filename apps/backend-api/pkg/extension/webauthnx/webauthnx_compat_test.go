package webauthnx

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures below were serialized by go-webauthn v0.17.4: legacyCredential
// is a stored passkey row's Data and legacySession is the state BeginLogin
// persisted, produced by webauthn.BeginDiscoverableLogin under this adapter's
// configuration (expiry moved to 2099 so the fixture never lapses). A passkey
// registered before the upgrade must keep completing a login, and a login
// begun before a rolling upgrade must still finish after it.
const (
	legacyPrivateKey = "MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgLZz234GpkysRT2pbCbzv4zTNqQUneM1UkyPCwk/1reyhRANCAASAaujpQA1acpKDLtN7z6V5BLK11jybcHCsfxWAiBqM23SvIl7+S2tnj1wJAmGHAZwi2x7o7q60v9PWghPjySYT"
	legacyCredential = `{"id":"bGVnYWN5LWNyZWRlbnRpYWwtaWQ=","publicKey":"pQECAyYgASFYIIBq6OlADVpykoMu03vPpXkEsrXWPJtwcKx/FYCIGozbIlggdK8iXv5La2ePXAkCYYcBnCLbHujurrS/09aCE+PJJhM=","attestationType":"none","attestationFormat":"none","transport":["internal"],"flags":{"userPresent":true,"userVerified":true,"backupEligible":false,"backupState":false},"authenticator":{"AAGUID":"AAAAAAAAAAAAAAAAAAAAAA==","signCount":7},"attestation":{}}`
	legacySession    = `{"challenge":"tbZnT8WrLpecKIWjzoN2bkDKxNsEN4dlPMdqwedJvWg","rpId":"example.com","expires":"2099-01-01T00:00:00Z","userVerification":"required"}`
)

func TestLegacyCredentialAndSessionCompleteLogin(t *testing.T) {
	adapter, err := New(Config{RPID: "example.com", DisplayName: "Example Platform", Origins: []string{"https://example.com"}})
	require.NoError(t, err)
	handle := []byte("legacy-user-handle")
	user, err := NewUser(handle, "alice", "Alice", [][]byte{[]byte(legacyCredential)})
	require.NoError(t, err)
	lookup := func(credentialID, userHandle []byte) (User, error) {
		assert.Equal(t, []byte("legacy-credential-id"), credentialID)
		assert.Equal(t, handle, userHandle)
		return user, nil
	}
	const challenge = "tbZnT8WrLpecKIWjzoN2bkDKxNsEN4dlPMdqwedJvWg"

	for _, tc := range []struct {
		name       string
		extensions any
	}{
		{name: "no extension output", extensions: map[string]any{}},
		{name: "unsolicited extension output", extensions: map[string]any{
			"credProps": map[string]any{"rk": true}, "vendor.example": true,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := legacyAssertion(t, challenge, 8, tc.extensions, false)
			resolved, credential, err := adapter.FinishLogin([]byte(legacySession), response, lookup)
			require.NoError(t, err)
			assert.Equal(t, handle, resolved.WebAuthnID())
			assert.Equal(t, []byte("legacy-credential-id"), credential.ID)
			assert.Equal(t, uint32(8), credential.SignCount)
			assert.Equal(t, "none", credential.AttestationFormat)
			assert.False(t, credential.CloneWarning)

			var stored webauthn.Credential
			require.NoError(t, json.Unmarshal(credential.Data, &stored))
			var legacy webauthn.Credential
			require.NoError(t, json.Unmarshal([]byte(legacyCredential), &legacy))
			assert.Equal(t, legacy.PublicKey, stored.PublicKey, "re-encoding keeps the stored key")
		})
	}

	t.Run("tampered signature", func(t *testing.T) {
		_, _, err := adapter.FinishLogin([]byte(legacySession), legacyAssertion(t, challenge, 8, map[string]any{}, true), lookup)
		assert.ErrorIs(t, err, ErrInvalidCredential)
	})
	t.Run("challenge of another session", func(t *testing.T) {
		other := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		_, _, err := adapter.FinishLogin([]byte(legacySession), legacyAssertion(t, other, 8, map[string]any{}, false), lookup)
		assert.ErrorIs(t, err, ErrInvalidCredential)
	})
	t.Run("session challenge shorter than the protocol minimum", func(t *testing.T) {
		short := base64.RawURLEncoding.EncodeToString([]byte("short"))
		session := `{"challenge":"` + short + `","rpId":"example.com","expires":"2099-01-01T00:00:00Z","userVerification":"required"}`
		_, _, err := adapter.FinishLogin([]byte(session), legacyAssertion(t, short, 8, map[string]any{}, false), lookup)
		assert.ErrorIs(t, err, ErrInvalidCredential)
	})
}

// legacyAssertion signs a discoverable assertion with the fixture key, as the
// authenticator that registered legacyCredential would.
func legacyAssertion(t *testing.T, challenge string, signCount uint32, extensions any, tamper bool) []byte {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(legacyPrivateKey)
	require.NoError(t, err)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	require.NoError(t, err)
	key, ok := parsed.(*ecdsa.PrivateKey)
	require.True(t, ok)

	clientData, err := json.Marshal(map[string]any{
		"type": "webauthn.get", "challenge": challenge, "origin": "https://example.com",
	})
	require.NoError(t, err)
	rpIDHash := sha256.Sum256([]byte("example.com"))
	authenticatorData := append(rpIDHash[:], 0x05) // user present and user verified
	authenticatorData = binary.BigEndian.AppendUint32(authenticatorData, signCount)
	clientDataHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte(nil), authenticatorData...), clientDataHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	require.NoError(t, err)
	if tamper {
		signature[len(signature)-1] ^= 0x01
	}

	id := base64.RawURLEncoding.EncodeToString([]byte("legacy-credential-id"))
	data, err := json.Marshal(map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"clientExtensionResults": extensions,
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authenticatorData),
			"signature":         base64.RawURLEncoding.EncodeToString(signature),
			"userHandle":        base64.RawURLEncoding.EncodeToString([]byte("legacy-user-handle")),
		},
	})
	require.NoError(t, err)
	return data
}
