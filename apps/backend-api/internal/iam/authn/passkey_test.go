package authn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/webauthnx"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPasskeyService(t *testing.T) (*Service, *authnRepositoryHarness, guid.ID, string) {
	t.Helper()
	harness := newAuthnRepositoryHarness(t, "")
	config := localAuthnConfig()
	config.Passkey = authnext.PasskeyConfig{
		Enabled: true, RPID: "app.example.test", DisplayName: "Example Platform",
		Origins: []string{"https://app.example.test"}, ChallengeTTL: 5 * time.Minute,
		MaxCredentials: 10,
	}
	service, err := NewWebService(config, harness.repository)
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow }
	principalID := bootstrapLocalPrincipal(t, harness, "alice", "correct horse battery staple")
	login, err := service.BeginLogin(t.Context(), "alice", "correct horse battery staple", "")
	require.NoError(t, err)
	return service, harness, principalID, service.SessionCookie(login.SessionID)
}

func TestPasskeyChallengeBoundaries(t *testing.T) {
	service, harness, principalID, cookie := newPasskeyService(t)

	listed, err := service.ListPasskeys(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.Empty(t, listed)
	_, err = service.BeginPasskeyRegistration(t.Context(), "", cookie, "wrong password")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	registration, err := service.BeginPasskeyRegistration(
		t.Context(), "", cookie, "correct horse battery staple",
	)
	require.NoError(t, err)
	assert.NotEmpty(t, registration.ChallengeID)
	assert.Equal(t, repositoryTestNow.Add(5*time.Minute), registration.ExpiresAt)
	assert.Contains(t, string(registration.Options), `"residentKey":"required"`)

	_, err = service.FinishPasskeyRegistration(
		t.Context(), "", cookie, registration.ChallengeID, "Laptop", json.RawMessage(`{"invalid":true}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.FinishPasskeyRegistration(
		t.Context(), "", cookie, registration.ChallengeID, "Laptop", json.RawMessage(`{"invalid":true}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)

	_, err = service.BeginPasskeyLogin(t.Context(), "https://evil.example", service.web.PostLoginURL)
	assert.ErrorIs(t, err, ErrCSRF)
	_, err = service.BeginPasskeyLogin(t.Context(), "https://app.example.test", "https://evil.example")
	assert.ErrorIs(t, err, authnext.ErrReturnURL)
	login, err := service.BeginPasskeyLogin(t.Context(), "https://app.example.test", "")
	require.NoError(t, err)
	assert.Contains(t, string(login.Options), `"userVerification":"required"`)
	_, err = service.FinishPasskeyLogin(
		t.Context(), "https://app.example.test", login.ChallengeID, json.RawMessage(`{"invalid":true}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.FinishPasskeyLogin(
		t.Context(), "https://app.example.test", login.ChallengeID, json.RawMessage(`{"invalid":true}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)

	user, err := harness.repository.getPasskeyUser(t.Context(), principalID)
	require.NoError(t, err)
	assert.NotEmpty(t, user.UserHandle)
}

func TestPasskeyCredentialManagementIsPrincipalScoped(t *testing.T) {
	service, harness, principalID, cookie := newPasskeyService(t)
	otherID := bootstrapLocalPrincipal(t, harness, "bob", "correct horse battery staple")
	otherLogin, err := service.BeginLogin(t.Context(), "bob", "correct horse battery staple", "")
	require.NoError(t, err)
	otherCookie := service.SessionCookie(otherLogin.SessionID)
	row := &model.Passkey{
		IDHash: strings.Repeat("a", 64), PrincipalID: principalID, Name: "Laptop",
		CredentialCiphertext: "metadata", SignCount: 7,
		CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}
	created, err := harness.repository.createPasskey(t.Context(), row, 10)
	require.NoError(t, err)
	require.True(t, created)

	_, err = service.RenamePasskey(t.Context(), "", otherCookie, row.PublicID.String(), "Stolen")
	assert.ErrorIs(t, err, authnext.ErrPasskeyNotFound)
	assert.ErrorIs(t, service.DeletePasskey(
		t.Context(), "", otherCookie, row.PublicID.String(), "correct horse battery staple",
	), authnext.ErrPasskeyNotFound)
	stored, err := harness.repository.getPasskey(t.Context(), row.IDHash)
	require.NoError(t, err)
	assert.Equal(t, principalID, stored.PrincipalID)
	assert.Equal(t, "Laptop", stored.Name)

	renamed, err := service.RenamePasskey(t.Context(), "", cookie, row.PublicID.String(), " Security key ")
	require.NoError(t, err)
	assert.Equal(t, "Security key", renamed.Name)
	assert.Equal(t, uint32(7), renamed.SignCount)
	_, err = service.RenamePasskey(t.Context(), "", cookie, row.PublicID.String(), "")
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	assert.ErrorIs(t, service.DeletePasskey(t.Context(), "", cookie, row.PublicID.String(), "wrong"), authnext.ErrInvalidCredentials)
	require.NoError(t, service.DeletePasskey(
		t.Context(), "", cookie, row.PublicID.String(), "correct horse battery staple",
	))
	_, err = service.Authenticate(t.Context(), "", otherCookie)
	require.NoError(t, err, "another principal's session must not be revoked")
	assert.NotEqual(t, principalID, otherID)
}

func TestPasskeySecurityMutationsRollBackWhenAuditFails(t *testing.T) {
	t.Run("registration", func(t *testing.T) {
		service, harness, principalID, _ := newPasskeyService(t)
		harness.auditErr = assert.AnError
		credential := passkeyCredential([]byte("audit-registration"), 1, false)
		_, err := service.storePasskeyCredential(
			t.Context(), principalID, "Security key", credential, repositoryTestNow,
		)
		assert.ErrorContains(t, err, "append authentication audit event")
		_, err = harness.repository.getPasskey(t.Context(), passkeyIDHash(credential.ID))
		assert.Error(t, err)
	})

	t.Run("login", func(t *testing.T) {
		service, harness, principalID, _ := newPasskeyService(t)
		current := seedPasskey(t, service, harness, principalID, []byte("audit-login"), 7)
		harness.auditErr = assert.AnError
		_, err := service.completePasskeyLogin(
			t.Context(), principalID, current, passkeyCredential(currentCredentialID(t, service, current), 8, false), repositoryTestNow,
		)
		assert.ErrorContains(t, err, "append authentication audit event")
		after, loadErr := harness.repository.getPasskey(t.Context(), current.IDHash)
		require.NoError(t, loadErr)
		assert.Equal(t, uint32(7), after.SignCount)
		assert.Nil(t, after.LastUsedAt)
	})

	t.Run("rename and delete", func(t *testing.T) {
		service, harness, principalID, cookie := newPasskeyService(t)
		current := seedPasskey(t, service, harness, principalID, []byte("audit-management"), 1)
		harness.auditErr = assert.AnError
		_, err := service.RenamePasskey(t.Context(), "", cookie, current.PublicID.String(), "Renamed")
		assert.ErrorContains(t, err, "append authentication audit event")
		after, loadErr := harness.repository.getPasskey(t.Context(), current.IDHash)
		require.NoError(t, loadErr)
		assert.Equal(t, current.Name, after.Name)
		err = service.DeletePasskey(
			t.Context(), "", cookie, current.PublicID.String(), "correct horse battery staple",
		)
		assert.ErrorContains(t, err, "append authentication audit event")
		_, loadErr = harness.repository.getPasskey(t.Context(), current.IDHash)
		require.NoError(t, loadErr)
	})
}

func TestPasskeyStoredCredentialResolution(t *testing.T) {
	service, harness, principalID, cookie := newPasskeyService(t)
	principal, err := harness.repository.getActivePrincipal(t.Context(), principalID)
	require.NoError(t, err)
	user, err := service.ensurePasskeyUser(t.Context(), principal, repositoryTestNow)
	require.NoError(t, err)
	firstHandle := append([]byte(nil), user.WebAuthnID()...)
	user, err = service.ensurePasskeyUser(t.Context(), principal, repositoryTestNow.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, firstHandle, user.WebAuthnID())

	credential := passkeyCredential([]byte("credential-id"), 9, false)
	view, err := service.storePasskeyCredential(
		t.Context(), principalID, "Platform authenticator", credential, repositoryTestNow,
	)
	require.NoError(t, err)
	loaded, err := service.loadPasskeyUser(t.Context(), service.repo, principalID)
	require.NoError(t, err)
	require.Len(t, loaded.WebAuthnCredentials(), 1)
	assert.Equal(t, credential.ID, loaded.WebAuthnCredentials()[0].ID)
	resolvedID, resolvedRow, err := service.resolvePasskey(t.Context(), credential.ID, firstHandle)
	require.NoError(t, err)
	assert.Equal(t, principalID, resolvedID)
	require.NotNil(t, resolvedRow.PublicID)
	assert.Equal(t, view.ID, *resolvedRow.PublicID)
	_, _, err = service.resolvePasskey(t.Context(), []byte("unknown"), firstHandle)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, _, err = service.resolvePasskey(t.Context(), credential.ID, []byte("wrong-handle"))
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	listed, err := service.ListPasskeys(t.Context(), "", cookie)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, view.ID, listed[0].ID)
}

func TestPasskeyChallengePersistenceAndLimits(t *testing.T) {
	service, harness, principalID, cookie := newPasskeyService(t)
	now := repositoryTestNow
	_, err := service.consumePasskeyChallenge(t.Context(), "", passkeyLogin, 0, now)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
	challenge, err := service.storePasskeyChallenge(
		t.Context(), passkeyRegistration, principalID, "", []byte(`{"state":true}`), now, now.Add(time.Minute),
	)
	require.NoError(t, err)
	_, err = service.consumePasskeyChallenge(t.Context(), challenge.ChallengeID, passkeyLogin, principalID, now)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
	_, err = service.consumePasskeyChallenge(t.Context(), challenge.ChallengeID, passkeyRegistration, principalID+1, now)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
	consumed, err := service.consumePasskeyChallenge(
		t.Context(), challenge.ChallengeID, passkeyRegistration, principalID, now,
	)
	require.NoError(t, err)
	require.NotNil(t, consumed.PrincipalID)
	assert.Equal(t, principalID, *consumed.PrincipalID)
	_, err = service.consumePasskeyChallenge(t.Context(), challenge.ChallengeID, passkeyRegistration, principalID, now)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)

	service.cfg.Passkey.MaxCredentials = 1
	seedPasskey(t, service, harness, principalID, []byte("only-passkey"), 1)
	_, err = service.BeginPasskeyRegistration(
		t.Context(), "", cookie, "correct horse battery staple",
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyLimit)
}

func TestPasskeyCredentialAndCounterCommit(t *testing.T) {
	service, harness, principalID, _ := newPasskeyService(t)
	credential := passkeyCredential([]byte("committed-credential"), 7, false)
	view, err := service.storePasskeyCredential(
		t.Context(), principalID, "Security key", credential, repositoryTestNow,
	)
	require.NoError(t, err)
	assert.NotZero(t, view.ID, "the passkey is published under its snowflake, not its credential hash")
	assert.Equal(t, "0102", view.AAGUID)
	current, err := harness.repository.getPasskey(t.Context(), passkeyIDHash(credential.ID))
	require.NoError(t, err)
	plain, err := service.decryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, current.IDHash), current.CredentialCiphertext,
	)
	require.NoError(t, err)
	assert.JSONEq(t, string(credential.Data), string(plain))

	loginCredential := passkeyCredential(credential.ID, 8, false)
	sessionID, err := service.completePasskeyLogin(
		t.Context(), principalID, current, loginCredential, repositoryTestNow.Add(time.Minute),
	)
	require.NoError(t, err)
	claims, err := service.Authenticate(t.Context(), "", service.SessionCookie(sessionID))
	require.NoError(t, err)
	assert.Equal(t, 2, claims.ACR)
	assert.Equal(t, []string{"passkey"}, claims.AMR)
	after, err := harness.repository.getPasskey(t.Context(), passkeyIDHash(credential.ID))
	require.NoError(t, err)
	assert.Equal(t, uint32(8), after.SignCount)
	require.NotNil(t, after.LastUsedAt)

	stale := *after
	stale.SignCount--
	_, err = service.completePasskeyLogin(
		t.Context(), principalID, &stale, passkeyCredential(credential.ID, 9, false), repositoryTestNow.Add(2*time.Minute),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.completePasskeyLogin(
		t.Context(), principalID, after, passkeyCredential(credential.ID, 9, true), repositoryTestNow.Add(2*time.Minute),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)

	warned, err := harness.repository.getPasskey(t.Context(), passkeyIDHash(credential.ID))
	require.NoError(t, err)
	warnedData, err := service.decryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, warned.IDHash), warned.CredentialCiphertext,
	)
	require.NoError(t, err)
	var warnedCredential webauthn.Credential
	require.NoError(t, json.Unmarshal(warnedData, &warnedCredential))
	assert.True(t, warnedCredential.Authenticator.CloneWarning)

	_, err = service.completePasskeyLogin(
		t.Context(), principalID, after, passkeyCredential(credential.ID, 9, false), repositoryTestNow.Add(3*time.Minute),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential, "an old snapshot must not overwrite clone-warning state")
	stillWarned, err := harness.repository.getPasskey(t.Context(), passkeyIDHash(credential.ID))
	require.NoError(t, err)
	assert.Equal(t, warned.CredentialCiphertext, stillWarned.CredentialCiphertext)
}

func TestPasskeyInputPolicyAndDisabledService(t *testing.T) {
	service, _, _, cookie := newPasskeyService(t)
	aaguid := bytes.Repeat([]byte{0xaa}, 16)
	assert.NoError(t, service.checkPasskeyAttestation(
		webauthnx.Credential{AttestationFormat: "none", AAGUID: aaguid},
	))
	service.cfg.Passkey.AllowedAttestationFormats = []string{"packed"}
	assert.ErrorIs(t, service.checkPasskeyAttestation(
		webauthnx.Credential{AttestationFormat: "none", AAGUID: []byte{0xaa}},
	), authnext.ErrPasskeyAttestation)
	service.cfg.Passkey.AllowedAttestationFormats = nil
	service.cfg.Passkey.AllowedAAGUIDs = []string{strings.Repeat("aa", 16)}
	assert.NoError(t, service.checkPasskeyAttestation(
		webauthnx.Credential{AttestationFormat: "none", AAGUID: aaguid},
	))
	assert.ErrorIs(t, service.checkPasskeyAttestation(
		webauthnx.Credential{AttestationFormat: "none", AAGUID: bytes.Repeat([]byte{0xbb}, 16)},
	), authnext.ErrPasskeyAttestation)
	_, err := normalizePasskeyName(" ")
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = normalizePasskeyName(strings.Repeat("a", 201))
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	name, err := normalizePasskeyName(" Laptop ")
	require.NoError(t, err)
	assert.Equal(t, "Laptop", name)

	service.cfg.Passkey.Enabled = false
	assert.ErrorIs(t, service.requirePasskeys(), authnext.ErrPasskeyDisabled)
	_, err = service.ListPasskeys(t.Context(), "", cookie)
	assert.ErrorIs(t, err, authnext.ErrPasskeyDisabled)
	assert.ErrorIs(t, (*Service)(nil).requirePasskeys(), authnext.ErrPasskeyDisabled)
}

func seedPasskey(
	t *testing.T,
	service *Service,
	harness *authnRepositoryHarness,
	principalID guid.ID,
	id []byte,
	signCount uint32,
) *model.Passkey {
	t.Helper()
	credential := passkeyCredential(id, signCount, false)
	idHash := passkeyIDHash(id)
	ciphertext, err := service.encryptAuthnData(
		passkeyCipher, passkeyAAD(principalID, idHash), credential.Data,
	)
	require.NoError(t, err)
	row := &model.Passkey{
		IDHash: idHash, PrincipalID: principalID, Name: "Key",
		CredentialCiphertext: ciphertext, SignCount: signCount,
		CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
	}
	created, err := harness.repository.createPasskey(t.Context(), row, 10)
	require.NoError(t, err)
	require.True(t, created)
	return row
}

func currentCredentialID(t *testing.T, service *Service, row *model.Passkey) []byte {
	t.Helper()
	plain, err := service.decryptAuthnData(
		passkeyCipher, passkeyAAD(row.PrincipalID, row.IDHash), row.CredentialCiphertext,
	)
	require.NoError(t, err)
	var credential webauthn.Credential
	require.NoError(t, json.Unmarshal(plain, &credential))
	return credential.ID
}

func passkeyCredential(id []byte, signCount uint32, cloneWarning bool) webauthnx.Credential {
	stored := webauthn.Credential{
		ID: id, PublicKey: []byte{1, 2, 3},
		AttestationFormat: "none", AttestationType: "none",
		Flags: webauthn.NewCredentialFlags(protocol.FlagUserPresent | protocol.FlagUserVerified),
		Authenticator: webauthn.Authenticator{
			AAGUID: []byte{1, 2}, SignCount: signCount, CloneWarning: cloneWarning,
		},
	}
	data, err := json.Marshal(stored)
	if err != nil {
		panic(err)
	}
	return webauthnx.Credential{
		ID: append([]byte(nil), id...), Data: data, SignCount: signCount,
		CloneWarning: cloneWarning, AttestationFormat: "none", AttestationType: "none",
		AAGUID: []byte{1, 2},
	}
}

func TestPasskeyRejectsCorruptedStoredIdentity(t *testing.T) {
	service, harness, principalID, _ := newPasskeyService(t)
	require.NoError(t, harness.db.Table(model.PasskeyUser{}.TableName()).Where("principal_id = ?", principalID).
		Update("user_handle", "%%%").Error)
	_, err := service.loadPasskeyUser(t.Context(), service.repo, principalID)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)

	handle := base64.RawURLEncoding.EncodeToString([]byte("user-handle"))
	require.NoError(t, harness.db.Table(model.PasskeyUser{}.TableName()).Where("principal_id = ?", principalID).
		Update("user_handle", handle).Error)
	row := seedPasskey(t, service, harness, principalID, []byte("corrupted"), 1)
	require.NoError(t, harness.db.Table(model.Passkey{}.TableName()).Where("id_hash = ?", row.IDHash).
		Update("credential_ciphertext", "not-ciphertext").Error)
	_, err = service.loadPasskeyUser(t.Context(), service.repo, principalID)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
}

func TestPasskeyPublicInputAndAuthenticationFailures(t *testing.T) {
	service, _, _, cookie := newPasskeyService(t)
	_, err := service.ListPasskeys(t.Context(), "", "")
	assert.Error(t, err)
	_, err = service.BeginPasskeyRegistration(t.Context(), "", "", "password")
	assert.Error(t, err)
	_, err = service.FinishPasskeyRegistration(
		t.Context(), "", "", "challenge", "Key", json.RawMessage(`{}`),
	)
	assert.Error(t, err)
	_, err = service.RenamePasskey(t.Context(), "", "", strings.Repeat("a", 64), "Key")
	assert.Error(t, err)
	assert.Error(t, service.DeletePasskey(
		t.Context(), "", "", strings.Repeat("a", 64), "password",
	))
	_, err = service.FinishPasskeyRegistration(
		t.Context(), "", cookie, "challenge", "", json.RawMessage(`{}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.FinishPasskeyRegistration(t.Context(), "", cookie, "challenge", "Key", nil)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.FinishPasskeyRegistration(
		t.Context(), "", cookie, "", "Key", json.RawMessage(`{}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
	_, err = service.FinishPasskeyLogin(
		t.Context(), "https://evil.example", "challenge", json.RawMessage(`{}`),
	)
	assert.ErrorIs(t, err, ErrCSRF)
	_, err = service.FinishPasskeyLogin(t.Context(), "https://app.example.test", "challenge", nil)
	assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	_, err = service.FinishPasskeyLogin(
		t.Context(), "https://app.example.test", "", json.RawMessage(`{}`),
	)
	assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
}

func TestPasskeyStorageFailuresFailClosed(t *testing.T) {
	t.Run("credential table", func(t *testing.T) {
		service, harness, principalID, cookie := newPasskeyService(t)
		bearer, _, err := service.IssueAccessToken(t.Context(), principalID, "", "")
		require.NoError(t, err)
		principal, err := harness.repository.getActivePrincipal(t.Context(), principalID)
		require.NoError(t, err)
		_, err = service.ensurePasskeyUser(t.Context(), principal, service.now())
		require.NoError(t, err)
		user, err := harness.repository.getPasskeyUser(t.Context(), principalID)
		require.NoError(t, err)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkeys").Error)
		_, err = service.ListPasskeys(t.Context(), "Bearer "+bearer, "")
		assert.ErrorContains(t, err, "list passkeys")
		_, err = service.loadPasskeyUserWithIdentity(t.Context(), service.repo, principal, user)
		assert.ErrorContains(t, err, "load passkey credentials")
		_, err = service.BeginPasskeyRegistration(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		assert.ErrorContains(t, err, "count passkeys")
		_, err = service.storePasskeyCredential(
			t.Context(), principalID, "Key", passkeyCredential([]byte("missing-table"), 1, false), service.now(),
		)
		assert.ErrorContains(t, err, "store passkey")
	})

	t.Run("credential limit race", func(t *testing.T) {
		service, _, principalID, _ := newPasskeyService(t)
		service.cfg.Passkey.MaxCredentials = 0
		_, err := service.storePasskeyCredential(
			t.Context(), principalID, "Key", passkeyCredential([]byte("limit-race"), 1, false), service.now(),
		)
		assert.ErrorIs(t, err, authnext.ErrPasskeyLimit)
	})

	t.Run("clone warning persistence", func(t *testing.T) {
		service, harness, principalID, _ := newPasskeyService(t)
		row := seedPasskey(t, service, harness, principalID, []byte("clone-warning-failure"), 1)
		invalid, err := service.encryptAuthnData(
			passkeyCipher, passkeyAAD(principalID, row.IDHash), []byte(`not-json`),
		)
		require.NoError(t, err)
		require.NoError(t, harness.db.Table(model.Passkey{}.TableName()).Where("id_hash = ?", row.IDHash).
			Update("credential_ciphertext", invalid).Error)
		row, err = harness.repository.getPasskey(t.Context(), row.IDHash)
		require.NoError(t, err)
		_, err = service.completePasskeyLogin(
			t.Context(), principalID, row, passkeyCredential([]byte("ignored"), 1, true), service.now(),
		)
		assert.ErrorContains(t, err, "store passkey clone warning")

		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkeys").Error)
		_, err = service.completePasskeyLogin(
			t.Context(), principalID, row, passkeyCredential([]byte("ignored"), 1, true), service.now(),
		)
		assert.ErrorContains(t, err, "store passkey clone warning")
	})

	t.Run("rename table", func(t *testing.T) {
		service, harness, principalID, cookie := newPasskeyService(t)
		row := seedPasskey(t, service, harness, principalID, []byte("rename-table"), 1)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkeys").Error)
		_, err := service.RenamePasskey(t.Context(), "", cookie, row.PublicID.String(), "Renamed")
		assert.ErrorContains(t, err, "rename passkey")
	})

	t.Run("delete table", func(t *testing.T) {
		service, harness, principalID, cookie := newPasskeyService(t)
		row := seedPasskey(t, service, harness, principalID, []byte("delete-table"), 1)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkeys").Error)
		err := service.DeletePasskey(
			t.Context(), "", cookie, row.PublicID.String(), "correct horse battery staple",
		)
		assert.ErrorContains(t, err, "delete passkey")
	})

	t.Run("user table", func(t *testing.T) {
		service, harness, _, cookie := newPasskeyService(t)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkey_users").Error)
		_, err := service.BeginPasskeyRegistration(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		assert.ErrorContains(t, err, "store passkey user handle")
	})

	t.Run("challenge table", func(t *testing.T) {
		service, harness, _, _ := newPasskeyService(t)
		require.NoError(t, harness.db.Exec("DROP TABLE iam_passkey_challenges").Error)
		_, err := service.BeginPasskeyLogin(t.Context(), "https://app.example.test", "")
		assert.ErrorContains(t, err, "store passkey challenge")
		_, err = service.consumePasskeyChallenge(
			t.Context(), "missing", passkeyLogin, 0, service.now(),
		)
		assert.ErrorContains(t, err, "load passkey challenge")
	})

	t.Run("duplicate credential", func(t *testing.T) {
		service, _, principalID, _ := newPasskeyService(t)
		credential := passkeyCredential([]byte("duplicate"), 1, false)
		_, err := service.storePasskeyCredential(t.Context(), principalID, "Key", credential, service.now())
		require.NoError(t, err)
		_, err = service.storePasskeyCredential(t.Context(), principalID, "Key", credential, service.now())
		assert.ErrorContains(t, err, "store passkey")
	})
}

func TestPasskeyAdditionalSecurityFailureStates(t *testing.T) {
	t.Run("expired challenge", func(t *testing.T) {
		service, _, _, _ := newPasskeyService(t)
		challenge, err := service.storePasskeyChallenge(
			t.Context(), passkeyLogin, 0, service.web.PostLoginURL, []byte(`{}`),
			service.now(), service.now().Add(time.Second),
		)
		require.NoError(t, err)
		_, err = service.consumePasskeyChallenge(
			t.Context(), challenge.ChallengeID, passkeyLogin, 0, service.now().Add(time.Second),
		)
		assert.ErrorIs(t, err, authnext.ErrPasskeyChallenge)
	})

	t.Run("recovery cooldown", func(t *testing.T) {
		service, harness, principalID, cookie := newPasskeyService(t)
		require.NoError(t, harness.db.Table("iam_credentials").Where("principal_id = ?", principalID).
			Update("recovery_cooldown_until", service.now().Add(time.Hour)).Error)
		_, err := service.BeginPasskeyRegistration(
			t.Context(), "", cookie, "correct horse battery staple",
		)
		assert.ErrorIs(t, err, authnext.ErrRecoveryCooldown)
	})

	t.Run("disabled principal", func(t *testing.T) {
		service, harness, principalID, _ := newPasskeyService(t)
		principal, err := harness.repository.getActivePrincipal(t.Context(), principalID)
		require.NoError(t, err)
		user, err := service.ensurePasskeyUser(t.Context(), principal, service.now())
		require.NoError(t, err)
		credential := passkeyCredential([]byte("disabled-principal"), 1, false)
		_, err = service.storePasskeyCredential(t.Context(), principalID, "Key", credential, service.now())
		require.NoError(t, err)
		require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", principalID).
			Update("status", "disabled").Error)
		_, _, err = service.resolvePasskey(t.Context(), credential.ID, user.WebAuthnID())
		assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
		_, err = service.loadPasskeyUser(t.Context(), service.repo, principalID)
		assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	})

	t.Run("invalid handle and credential JSON", func(t *testing.T) {
		service, harness, principalID, _ := newPasskeyService(t)
		principal, err := harness.repository.getActivePrincipal(t.Context(), principalID)
		require.NoError(t, err)
		user := &model.PasskeyUser{PrincipalID: principalID, UserHandle: base64.RawURLEncoding.EncodeToString(make([]byte, 65))}
		_, err = service.loadPasskeyUserWithIdentity(t.Context(), service.repo, principal, user)
		assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
		user.UserHandle = base64.RawURLEncoding.EncodeToString([]byte("valid-handle"))
		idHash := strings.Repeat("f", 64)
		ciphertext, err := service.encryptAuthnData(passkeyCipher, passkeyAAD(principalID, idHash), []byte("not-json"))
		require.NoError(t, err)
		created, err := harness.repository.createPasskey(t.Context(), &model.Passkey{
			IDHash: idHash, PrincipalID: principalID, Name: "Invalid", CredentialCiphertext: ciphertext,
			CreatedAt: service.now(), UpdatedAt: service.now(),
		}, 10)
		require.NoError(t, err)
		require.True(t, created)
		_, err = service.loadPasskeyUserWithIdentity(t.Context(), service.repo, principal, user)
		assert.ErrorIs(t, err, authnext.ErrPasskeyCredential)
	})

}

// TestPasskeyUserStorageFaultIsNotAWrongCredential covers both reads behind a
// passkey user. A missing row is a credential that does not resolve; a failed
// read is a fault, and a login must not audit it as a denied attempt.
func TestPasskeyUserStorageFaultIsNotAWrongCredential(t *testing.T) {
	for _, table := range []string{"iam_principals", "iam_passkey_users"} {
		t.Run(table, func(t *testing.T) {
			service, harness, principalID, _ := newPasskeyService(t)
			failReadsAfter(t, harness.db, table, 0)
			_, err := service.loadPasskeyUser(t.Context(), service.repo, principalID)
			require.Error(t, err)
			assert.NotErrorIs(t, err, authnext.ErrPasskeyCredential)
		})
	}
}
