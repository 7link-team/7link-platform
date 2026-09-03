package webauthnx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCeremonyProtocol struct {
	beginRegistration func(webauthn.User, ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error)
	createCredential  func(webauthn.User, webauthn.SessionData, *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error)
	beginLogin        func(...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error)
	validateLogin     func(webauthn.DiscoverableUserHandler, webauthn.SessionData, *protocol.ParsedCredentialAssertionData) (webauthn.User, *webauthn.Credential, error)
}

func (f *fakeCeremonyProtocol) BeginRegistration(
	user webauthn.User,
	options ...webauthn.RegistrationOption,
) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return f.beginRegistration(user, options...)
}

func (f *fakeCeremonyProtocol) CreateCredential(
	user webauthn.User,
	session webauthn.SessionData,
	parsed *protocol.ParsedCredentialCreationData,
) (*webauthn.Credential, error) {
	return f.createCredential(user, session, parsed)
}

func (f *fakeCeremonyProtocol) BeginDiscoverableLogin(
	options ...webauthn.LoginOption,
) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return f.beginLogin(options...)
}

func (f *fakeCeremonyProtocol) ValidatePasskeyLogin(
	handler webauthn.DiscoverableUserHandler,
	session webauthn.SessionData,
	parsed *protocol.ParsedCredentialAssertionData,
) (webauthn.User, *webauthn.Credential, error) {
	return f.validateLogin(handler, session, parsed)
}

func TestCeremonyOptionsAndValidationBoundaries(t *testing.T) {
	adapter, err := New(Config{
		RPID: "example.com", DisplayName: "7Link", Origins: []string{"https://example.com"},
	})
	require.NoError(t, err)
	user, err := NewUser([]byte("opaque-user-handle"), "alice", "Alice", nil)
	require.NoError(t, err)
	expires := time.Now().Add(5 * time.Minute).UTC()

	creation, registrationSession, err := adapter.BeginRegistration(user, expires)
	require.NoError(t, err)
	assert.Contains(t, string(creation), `"residentKey":"required"`)
	assert.Contains(t, string(creation), `"userVerification":"required"`)
	assert.NotEmpty(t, registrationSession)
	var registrationState webauthn.SessionData
	require.NoError(t, json.Unmarshal(registrationSession, &registrationState))
	assert.True(t, expires.Equal(registrationState.Expires))
	_, err = adapter.FinishRegistration(user, registrationSession, []byte(`{"invalid":true}`))
	assert.ErrorIs(t, err, ErrInvalidCredential)

	assertion, loginSession, err := adapter.BeginLogin(expires)
	require.NoError(t, err)
	assert.Contains(t, string(assertion), `"userVerification":"required"`)
	assert.NotEmpty(t, loginSession)
	var loginState webauthn.SessionData
	require.NoError(t, json.Unmarshal(loginSession, &loginState))
	assert.True(t, expires.Equal(loginState.Expires))
	_, _, err = adapter.FinishLogin(loginSession, []byte(`{"invalid":true}`), func(_, _ []byte) (User, error) {
		return user, nil
	})
	assert.ErrorIs(t, err, ErrInvalidCredential)

	_, err = NewUser([]byte("id"), "name", "display", [][]byte{[]byte(`not-json`)})
	assert.ErrorIs(t, err, ErrInvalidCredential)
	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(creation, &decoded))
}

func TestConfigurationRequiresTrustedOrigins(t *testing.T) {
	_, err := New(Config{RPID: "example.com", DisplayName: "7Link"})
	assert.Error(t, err)
}

func TestAttestationConveyancePreference(t *testing.T) {
	for _, tc := range []struct {
		preference AttestationPreference
		want       string
	}{
		{AttestationNone, `"attestation":"none"`},
		{AttestationIndirect, `"attestation":"indirect"`},
		{AttestationDirect, `"attestation":"direct"`},
		{AttestationEnterprise, `"attestation":"enterprise"`},
	} {
		adapter, err := New(Config{
			RPID: "example.com", DisplayName: "7Link", Origins: []string{"https://example.com"},
			AttestationPreference: tc.preference,
		})
		require.NoError(t, err)
		user, err := NewUser([]byte("opaque-user-handle"), "alice", "Alice", nil)
		require.NoError(t, err)
		creation, _, err := adapter.BeginRegistration(user, time.Now().Add(5*time.Minute).UTC())
		require.NoError(t, err)
		assert.Contains(t, string(creation), tc.want)
	}

	_, err := New(Config{
		RPID: "example.com", DisplayName: "7Link", Origins: []string{"https://example.com"},
		AttestationPreference: "unsupported",
	})
	assert.Error(t, err)
}

func TestCredentialSurfacesAttestation(t *testing.T) {
	stored := webauthn.Credential{
		ID: []byte{1, 2, 3, 4}, PublicKey: []byte{5, 6, 7},
		AttestationFormat: "packed", AttestationType: "basic_full",
		Flags:         webauthn.NewCredentialFlags(protocol.FlagUserPresent | protocol.FlagUserVerified),
		Authenticator: webauthn.Authenticator{AAGUID: []byte{9, 9, 9}, SignCount: 7},
	}
	encoded, err := encodeCredential(&stored)
	require.NoError(t, err)
	assert.Equal(t, "packed", encoded.AttestationFormat)
	assert.Equal(t, "basic_full", encoded.AttestationType)
	assert.Equal(t, []byte{9, 9, 9}, encoded.AAGUID)
	var roundTrip webauthn.Credential
	require.NoError(t, json.Unmarshal(encoded.Data, &roundTrip))
	assert.Equal(t, "packed", roundTrip.AttestationFormat)
	assert.Equal(t, []byte{9, 9, 9}, roundTrip.Authenticator.AAGUID)
}

func TestStoredUserCredentialAndSessionEncoding(t *testing.T) {
	stored := webauthn.Credential{
		ID:        []byte{1, 2, 3, 4},
		PublicKey: []byte{5, 6, 7},
		Flags:     webauthn.NewCredentialFlags(protocol.FlagUserPresent | protocol.FlagUserVerified),
		Authenticator: webauthn.Authenticator{
			AAGUID: []byte{8, 9}, SignCount: 42, CloneWarning: true,
		},
	}
	data, err := json.Marshal(stored)
	require.NoError(t, err)
	handle := []byte("stable-user-handle")
	user, err := NewUser(handle, "alice", "Alice Example", [][]byte{data})
	require.NoError(t, err)

	assert.Equal(t, handle, user.WebAuthnID())
	assert.Equal(t, "alice", user.WebAuthnName())
	assert.Equal(t, "Alice Example", user.WebAuthnDisplayName())
	require.Len(t, user.WebAuthnCredentials(), 1)
	assert.Equal(t, stored.ID, user.WebAuthnCredentials()[0].ID)
	handle[0] = 'X'
	assert.Equal(t, byte('s'), user.WebAuthnID()[0], "the adapter owns an independent user handle")

	encoded, err := encodeCredential(&stored)
	require.NoError(t, err)
	assert.Equal(t, stored.ID, encoded.ID)
	assert.Equal(t, uint32(42), encoded.SignCount)
	assert.True(t, encoded.CloneWarning)
	var roundTrip webauthn.Credential
	require.NoError(t, json.Unmarshal(encoded.Data, &roundTrip))
	assert.Equal(t, stored.PublicKey, roundTrip.PublicKey)
	marked, err := MarkCloneWarning(data)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(marked, &roundTrip))
	assert.True(t, roundTrip.Authenticator.CloneWarning)
	_, err = MarkCloneWarning([]byte(`not-json`))
	assert.ErrorIs(t, err, ErrInvalidCredential)

	expires := time.Now().Add(time.Minute).UTC().Truncate(time.Nanosecond)
	sessionData, err := json.Marshal(webauthn.SessionData{
		Challenge: "challenge", RelyingPartyID: "example.com", UserID: user.WebAuthnID(),
		Expires: expires, UserVerification: protocol.VerificationRequired,
	})
	require.NoError(t, err)
	decoded, err := decodeSession(sessionData)
	require.NoError(t, err)
	assert.Equal(t, "challenge", decoded.Challenge)
	assert.Equal(t, expires, decoded.Expires)
	_, err = decodeSession([]byte(`{"expires":`))
	assert.ErrorIs(t, err, ErrInvalidCredential)

	adapter, err := New(Config{RPID: "example.com", DisplayName: "7Link", Origins: []string{"https://example.com"}})
	require.NoError(t, err)
	_, err = adapter.FinishRegistration(user, []byte(`not-json`), nil)
	assert.ErrorIs(t, err, ErrInvalidCredential)
	_, _, err = adapter.FinishLogin([]byte(`not-json`), nil, func(_, _ []byte) (User, error) { return user, nil })
	assert.ErrorIs(t, err, ErrInvalidCredential)
}

func TestAdapterCeremonySuccessAndProtocolFailures(t *testing.T) {
	user, err := NewUser([]byte("user-handle"), "alice", "Alice", nil)
	require.NoError(t, err)
	wantCredential := &webauthn.Credential{
		ID: []byte("credential-id"), Authenticator: webauthn.Authenticator{SignCount: 5},
	}
	protocolFailure := errors.New("protocol failed")
	fake := &fakeCeremonyProtocol{
		beginRegistration: func(webauthn.User, ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
			return nil, nil, protocolFailure
		},
		createCredential: func(webauthn.User, webauthn.SessionData, *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
			return wantCredential, nil
		},
		beginLogin: func(...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
			return nil, nil, protocolFailure
		},
		validateLogin: func(
			handler webauthn.DiscoverableUserHandler,
			_ webauthn.SessionData,
			parsed *protocol.ParsedCredentialAssertionData,
		) (webauthn.User, *webauthn.Credential, error) {
			resolved, err := handler(parsed.RawID, parsed.Response.UserHandle)
			return resolved, wantCredential, err
		},
	}
	adapter := &Adapter{protocol: fake}

	_, _, err = adapter.BeginRegistration(user, time.Now())
	assert.ErrorIs(t, err, protocolFailure)
	_, _, err = adapter.BeginLogin(time.Now())
	assert.ErrorIs(t, err, protocolFailure)

	session, err := json.Marshal(webauthn.SessionData{Challenge: "challenge", UserID: user.WebAuthnID()})
	require.NoError(t, err)
	credential, err := adapter.FinishRegistration(user, session, validRegistrationResponse(t))
	require.NoError(t, err)
	assert.Equal(t, wantCredential.ID, credential.ID)

	loginSession, err := json.Marshal(webauthn.SessionData{Challenge: "challenge"})
	require.NoError(t, err)
	resolved, credential, err := adapter.FinishLogin(loginSession, validLoginResponse(t), func(credentialID, userHandle []byte) (User, error) {
		assert.Equal(t, []byte("credential-id"), credentialID)
		assert.Equal(t, []byte("user-handle"), userHandle)
		return user, nil
	})
	require.NoError(t, err)
	assert.Equal(t, user.WebAuthnName(), resolved.WebAuthnName())
	assert.Equal(t, wantCredential.ID, credential.ID)

	fake.createCredential = func(webauthn.User, webauthn.SessionData, *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
		return nil, protocolFailure
	}
	_, err = adapter.FinishRegistration(user, session, validRegistrationResponse(t))
	assert.ErrorIs(t, err, ErrInvalidCredential)
	fake.validateLogin = func(
		webauthn.DiscoverableUserHandler,
		webauthn.SessionData,
		*protocol.ParsedCredentialAssertionData,
	) (webauthn.User, *webauthn.Credential, error) {
		return nil, nil, protocolFailure
	}
	_, _, err = adapter.FinishLogin(loginSession, validLoginResponse(t), func(_, _ []byte) (User, error) {
		return user, nil
	})
	assert.ErrorIs(t, err, ErrInvalidCredential)
}

func TestBeginCeremonyMarshalFailures(t *testing.T) {
	user, err := NewUser([]byte("user-handle"), "alice", "Alice", nil)
	require.NoError(t, err)
	badExtension := protocol.AuthenticationExtensions{"unsupported": make(chan int)}
	fake := &fakeCeremonyProtocol{
		beginRegistration: func(webauthn.User, ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
			return &protocol.CredentialCreation{Response: protocol.PublicKeyCredentialCreationOptions{
				Extensions: badExtension,
			}}, &webauthn.SessionData{}, nil
		},
		beginLogin: func(...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
			return &protocol.CredentialAssertion{Response: protocol.PublicKeyCredentialRequestOptions{
				Extensions: badExtension,
			}}, &webauthn.SessionData{}, nil
		},
	}
	adapter := &Adapter{protocol: fake}
	_, _, err = adapter.BeginRegistration(user, time.Now())
	assert.Error(t, err)
	_, _, err = adapter.BeginLogin(time.Now())
	assert.Error(t, err)

	fake.beginRegistration = func(webauthn.User, ...webauthn.RegistrationOption) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
		return &protocol.CredentialCreation{}, &webauthn.SessionData{Extensions: badExtension}, nil
	}
	fake.beginLogin = func(...webauthn.LoginOption) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
		return &protocol.CredentialAssertion{}, &webauthn.SessionData{Extensions: badExtension}, nil
	}
	_, _, err = adapter.BeginRegistration(user, time.Now())
	assert.Error(t, err)
	_, _, err = adapter.BeginLogin(time.Now())
	assert.Error(t, err)
}

func validRegistrationResponse(t *testing.T) []byte {
	t.Helper()
	const attestationObject = "o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YVjEdKbqkhPJnC90siSSsyDPQCYqlMGpUKA5fyklC2CEHvBBAAAAAAAAAAAAAAAAAAAAAAAAAAAAQOsa7QYSUFukFOLTmgeK6x2ktirNMgwy_6vIwwtegxI2flS1X-JAkZL5dsadg-9bEz2J7PnsbB0B08txvsyUSvKlAQIDJiABIVggLKF5xS0_BntttUIrm2Z2tgZ4uQDwllbdIfrrBMABCNciWCDHwin8Zdkr56iSIh0MrB5qZiEzYLQpEOREhMUkY6q4Vw"
	clientData := base64.RawURLEncoding.EncodeToString([]byte(
		`{"type":"webauthn.create","challenge":"challenge","origin":"https://example.com"}`,
	))
	data, err := json.Marshal(map[string]any{
		"id": "Y3JlZGVudGlhbC1pZA", "rawId": "Y3JlZGVudGlhbC1pZA", "type": "public-key",
		"response": map[string]any{"clientDataJSON": clientData, "attestationObject": attestationObject},
	})
	require.NoError(t, err)
	return data
}

func validLoginResponse(t *testing.T) []byte {
	t.Helper()
	clientData := base64.RawURLEncoding.EncodeToString([]byte(
		`{"type":"webauthn.get","challenge":"challenge","origin":"https://example.com"}`,
	))
	data, err := json.Marshal(map[string]any{
		"id": "Y3JlZGVudGlhbC1pZA", "rawId": "Y3JlZGVudGlhbC1pZA", "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    clientData,
			"authenticatorData": base64.RawURLEncoding.EncodeToString(make([]byte, 37)),
			"signature":         base64.RawURLEncoding.EncodeToString([]byte("signature")),
			"userHandle":        base64.RawURLEncoding.EncodeToString([]byte("user-handle")),
		},
	})
	require.NoError(t, err)
	return data
}
