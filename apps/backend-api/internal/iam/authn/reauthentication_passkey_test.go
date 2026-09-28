package authn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReauthenticationPasskeyVerifiesRealSignatureWithoutPassword(t *testing.T) {
	service, harness, principalID, _ := newPasskeyService(t)
	service.now = time.Now
	session, err := service.CreateSession(t.Context(), principalID, time.Now(), assurancePassword(time.Now()))
	require.NoError(t, err)
	cookie := service.SessionCookie(session)
	require.NoError(t, harness.db.Delete(&model.Credential{}, "principal_id = ?", principalID).Error)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	credentialID := []byte("fresh-reauthentication-key")
	userHandle := []byte("reauthentication-user")
	publicKey, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	require.NoError(t, err)
	stored, err := json.Marshal(webauthn.Credential{ID: credentialID, PublicKey: publicKey, AttestationType: "none"})
	require.NoError(t, err)
	hash := passkeyIDHash(credentialID)
	ciphertext, err := service.encryptAuthnData(passkeyCipher, passkeyAAD(principalID, hash), stored)
	require.NoError(t, err)
	require.NoError(t, harness.db.Create(&model.PasskeyUser{PrincipalID: principalID, UserHandle: base64.RawURLEncoding.EncodeToString(userHandle), CreatedAt: time.Now()}).Error)
	require.NoError(t, harness.db.Create(&model.Passkey{IDHash: hash, PrincipalID: principalID, Name: "Security key", CredentialCiphertext: ciphertext, CreatedAt: time.Now(), UpdatedAt: time.Now()}).Error)
	methods, err := service.AvailableReauthenticationMethods(t.Context(), cookie)
	require.NoError(t, err)
	assert.Equal(t, []string{"passkey"}, methods.Methods)
	count, err := service.localLoginMethodsOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	for attempt, verified := range []bool{false, true, true, true} {
		options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, principalID.String(), "passkey", "https://app.example.test")
		require.NoError(t, err)
		var request struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		}
		require.NoError(t, json.Unmarshal(options.Options, &request))
		clientData, err := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": request.PublicKey.Challenge, "origin": "https://app.example.test"})
		require.NoError(t, err)
		rpHash := sha256.Sum256([]byte("app.example.test"))
		authData := append([]byte{}, rpHash[:]...)
		flags := byte(1)
		if verified {
			flags |= 4
		}
		authData = append(authData, flags)
		authData = binary.BigEndian.AppendUint32(authData, 1)
		clientHash := sha256.Sum256(clientData)
		signed := append(append([]byte{}, authData...), clientHash[:]...)
		digest := sha256.Sum256(signed)
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
		require.NoError(t, err)
		encode := base64.RawURLEncoding.EncodeToString
		response, err := json.Marshal(map[string]any{"id": encode(credentialID), "rawId": encode(credentialID), "type": "public-key",
			"response": map[string]any{"clientDataJSON": encode(clientData), "authenticatorData": encode(authData), "signature": encode(signature), "userHandle": encode(userHandle)}})
		require.NoError(t, err)
		if attempt == 0 {
			assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "", "", "https://evil.example", response), ErrCSRF)
			service.cfg.Passkey.Enabled = false
			assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "", "", "https://app.example.test", response), authnext.ErrPasskeyDisabled)
			service.cfg.Passkey.Enabled = true
			var row model.ReauthenticationChallenge
			require.NoError(t, harness.db.Take(&row, "id_hash = ?", tokenHash(options.Proof)).Error)
			require.NoError(t, harness.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", row.IDHash).Update("session_data", "corrupt").Error)
			assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "", "", "https://app.example.test", response))
			require.NoError(t, harness.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", row.IDHash).Update("session_data", row.SessionData).Error)
		}
		if attempt == 3 {
			require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register("reauth_storage_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "iam_passkeys" {
					tx.AddError(assert.AnError)
				}
			}))
		}
		err = service.VerifyReauthentication(t.Context(), cookie, options.Proof, "", "", "https://app.example.test", response)
		if attempt == 3 {
			require.NoError(t, harness.db.Callback().Query().Remove("reauth_storage_failure"))
			assert.ErrorIs(t, err, assert.AnError)
			assert.NotErrorIs(t, err, authnext.ErrInvalidCredentials)
			continue
		}
		if attempt == 2 {
			assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
			count, err := service.localLoginMethodsOn(t.Context(), harness.db, principalID)
			require.NoError(t, err)
			assert.Zero(t, count, "replayed counter must persist the clone warning")
			continue
		}
		if !verified {
			assert.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
			return service.ConsumeReauthenticationOn(t.Context(), tx, principalID, cookie, options.Proof, ReauthenticationPrincipalClose, principalID.String())
		}))
	}
	cloned, err := json.Marshal(webauthn.Credential{ID: credentialID, PublicKey: publicKey, Authenticator: webauthn.Authenticator{CloneWarning: true}})
	require.NoError(t, err)
	ciphertext, err = service.encryptAuthnData(passkeyCipher, passkeyAAD(principalID, hash), cloned)
	require.NoError(t, err)
	require.NoError(t, harness.db.Model(&model.Passkey{}).Where("id_hash = ?", hash).Update("credential_ciphertext", ciphertext).Error)
	count, err = service.localLoginMethodsOn(t.Context(), harness.db, principalID)
	require.NoError(t, err)
	assert.Zero(t, count, "a cloned passkey cannot preserve the last-login-method invariant")
	ciphertext, err = service.encryptAuthnData(passkeyCipher, passkeyAAD(principalID, hash), []byte("invalid-json"))
	require.NoError(t, err)
	require.NoError(t, harness.db.Model(&model.Passkey{}).Where("id_hash = ?", hash).Update("credential_ciphertext", ciphertext).Error)
	_, err = service.localLoginMethodsOn(t.Context(), harness.db, principalID)
	assert.Error(t, err)
	require.NoError(t, harness.db.Model(&model.Passkey{}).Where("id_hash = ?", hash).Update("credential_ciphertext", "corrupt").Error)
	_, err = service.localLoginMethodsOn(t.Context(), harness.db, principalID)
	assert.Error(t, err)
}
