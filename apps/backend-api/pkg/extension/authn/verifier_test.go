package authn

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/jwsx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyAuthorization(t *testing.T) {
	key := newTestKey(t)
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": issuerURL(r) + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key.jwk()}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	issuer = srv.URL
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: issuer, Audience: []string{"api"}})
	require.NoError(t, err)

	token := key.sign(t, map[string]any{
		"iss":                issuer,
		"sub":                "u123",
		"aud":                []string{"api"},
		"exp":                time.Now().Add(time.Hour).Unix(),
		"preferred_username": "alice",
		"email":              "alice@example.test",
	})

	claims, err := verifier.VerifyAuthorization(context.Background(), "Bearer "+token)
	require.NoError(t, err)
	assert.Equal(t, "u123", claims.Subject)
	assert.Equal(t, "u123", claims.Subject)
	assert.Equal(t, "alice", claims.PreferredUsername)
	authenticated, err := verifier.Authenticate(context.Background(), "Bearer "+token, "")
	require.NoError(t, err)
	assert.Equal(t, claims.Subject, authenticated.Subject)
}

func TestVerifyAuthorizationRejectsInvalidInputs(t *testing.T) {
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example"})
	require.NoError(t, err)

	_, err = verifier.VerifyAuthorization(context.Background(), "")
	assert.ErrorIs(t, err, ErrMissingBearer)
	_, err = verifier.VerifyAuthorization(context.Background(), "Basic abc")
	assert.ErrorIs(t, err, ErrInvalidBearer)
	_, err = verifier.Verify(context.Background(), "not-a-jwt")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestNewVerifierValidation(t *testing.T) {
	disabled, err := NewVerifier(Config{})
	require.NoError(t, err)
	_, err = disabled.VerifyAuthorization(context.Background(), "Bearer abc")
	assert.ErrorIs(t, err, ErrDisabled)

	_, err = NewVerifier(Config{Enabled: true})
	assert.Error(t, err)
}

func TestVerifyRejectsBadSignatureAndUnknownKey(t *testing.T) {
	key := newTestKey(t)
	other := newTestKey(t)
	other.kid = "kid2"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key.jwk()}})
	}))
	defer srv.Close()

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)

	unknownKid := other.sign(t, map[string]any{"iss": "https://issuer.example", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	_, err = verifier.Verify(context.Background(), unknownKid)
	assert.ErrorIs(t, err, ErrUnknownKey)

	token := key.sign(t, map[string]any{"iss": "https://issuer.example", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	parts := strings.Split(token, ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	signature[0] ^= 1
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	_, err = verifier.Verify(context.Background(), strings.Join(parts, "."))
	assert.ErrorIs(t, err, ErrInvalidToken)
	assert.ErrorIs(t, err, jwsx.ErrSignature)
}

func TestParseClaims(t *testing.T) {
	claims, err := parseClaims(map[string]any{
		"iss": "issuer",
		"sub": "subject",
		"aud": "api",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, claims.Audience)

	_, err = parseClaims(map[string]any{"iss": "issuer"})
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateClaims(t *testing.T) {
	verifier, err := NewVerifier(Config{
		Enabled:  true,
		Issuer:   "https://issuer.example",
		Audience: []string{"api"},
	})
	require.NoError(t, err)

	valid := &Claims{Issuer: "https://issuer.example", Subject: "u1", Audience: []string{"api"}, ExpiresAt: time.Now().Add(time.Hour)}
	assert.NoError(t, verifier.validate(valid, time.Now()))

	assert.ErrorIs(t, verifier.validate(&Claims{Issuer: "bad", Subject: "u1", Audience: []string{"api"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()), ErrInvalidIssuer)
	assert.ErrorIs(t, verifier.validate(&Claims{Issuer: "https://issuer.example", Subject: "u1", Audience: []string{"other"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()), ErrInvalidAud)
	assert.ErrorIs(t, verifier.validate(&Claims{Issuer: "https://issuer.example", Subject: "u1", Audience: []string{"api"}, ExpiresAt: time.Now().Add(-time.Hour)}, time.Now()), ErrExpiredToken)
	assert.ErrorIs(t, verifier.validate(&Claims{Issuer: "https://issuer.example", Audience: []string{"api"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()), ErrInvalidToken)
	assert.ErrorIs(t, verifier.validate(&Claims{Issuer: "https://issuer.example", Subject: "u1", Audience: []string{"api"}, ExpiresAt: time.Now().Add(time.Hour), NotBefore: time.Now().Add(time.Hour)}, time.Now()), ErrInvalidToken)
}

func TestContextHelpers(t *testing.T) {
	_, ok := SubjectFromContext(context.Background())
	assert.False(t, ok)
	assert.Zero(t, TenantIDFromContext(context.Background()))
	assert.Zero(t, EntityIDFromContext(context.Background()))
	assert.Zero(t, PrincipalIDFromContext(context.Background()))

	ctx := WithClaims(context.Background(), &Claims{
		Subject:     "u1",
		TenantID:    guid.ID(11),
		EntityID:    guid.ID(12),
		PrincipalID: guid.ID(13),
	})
	claims, ok := FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "u1", claims.Subject)
	subject, ok := SubjectFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "u1", subject)
	assert.Equal(t, guid.ID(11), TenantIDFromContext(ctx))
	assert.Equal(t, guid.ID(12), EntityIDFromContext(ctx))
	assert.Equal(t, guid.ID(13), PrincipalIDFromContext(ctx))
}

func TestJWKPublicKeysAndSignatureAlgorithms(t *testing.T) {
	rsaKey := newTestKey(t)
	parsedRSA, err := (&jwk{
		Kid: rsaKey.kid,
		Kty: "RSA",
		N:   base64.RawURLEncoding.EncodeToString(rsaKey.private.PublicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(bigEndian(rsaKey.private.PublicKey.E)),
	}).publicKey()
	require.NoError(t, err)
	assert.Equal(t, &rsaKey.private.PublicKey, parsedRSA)

	message := []byte("signed message")
	hash := sha256.Sum256(message)
	rsaSignature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey.private, crypto.SHA256, hash[:])
	require.NoError(t, err)
	assert.NoError(t, tokenAlgorithms.Verify("RS256", parsedRSA, message, rsaSignature))
	assert.ErrorIs(t, tokenAlgorithms.Verify("RS256", parsedRSA, message, append([]byte(nil), rsaSignature[:len(rsaSignature)-1]...)), jwsx.ErrSignature)

	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	parsedEd, err := (&jwk{
		Kid: "ed25519-key",
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(edPublic),
	}).publicKey()
	require.NoError(t, err)
	assert.Equal(t, edPublic, parsedEd)

	edSignature := ed25519.Sign(edPrivate, message)
	assert.NoError(t, tokenAlgorithms.Verify("EdDSA", parsedEd, message, edSignature))
	assert.ErrorIs(t, tokenAlgorithms.Verify("EdDSA", parsedEd, []byte("tampered"), edSignature), jwsx.ErrSignature)
	assert.ErrorIs(t, tokenAlgorithms.Verify("EdDSA", parsedRSA, message, edSignature), jwsx.ErrKeyType)

	_, err = (&jwk{Kid: "bad-curve", Kty: "OKP", Crv: "X25519", X: base64.RawURLEncoding.EncodeToString(edPublic)}).publicKey()
	assert.Error(t, err)
	_, err = (&jwk{Kid: "bad-encoding", Kty: "OKP", Crv: "Ed25519", X: "!"}).publicKey()
	assert.Error(t, err)
	_, err = (&jwk{Kid: "bad-length", Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString([]byte("short"))}).publicKey()
	assert.Error(t, err)
}

func TestIdentityClaims(t *testing.T) {
	raw := map[string]any{
		"iss":                "issuer",
		"sub":                "subject",
		"subject_type":       SubjectTypePrincipal,
		"aud":                []any{"api", "web"},
		"exp":                float64(time.Now().Add(time.Hour).Unix()),
		"tenant_id":          "11",
		"entity_id":          "12",
		"principal_id":       "13",
		"organization_id":    "external-org",
		"credential_version": float64(7),
		"auth_time":          float64(1_700_000_000),
		"acr":                float64(2),
		"amr":                []any{"pwd", "totp", 3},
		"client_id":          "client",
		"oauth_grant_id":     "current-grant",
		"network_zone":       "office",
		"email_verified":     true,
	}
	claims, err := parseClaims(raw)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(11), claims.TenantID)
	assert.Equal(t, guid.ID(12), claims.EntityID)
	assert.Equal(t, guid.ID(13), claims.PrincipalID)
	assert.Equal(t, int64(7), claims.CredentialVersion)
	assert.Equal(t, 2, claims.ACR)
	assert.Equal(t, []string{"pwd", "totp"}, claims.AMR)
	assert.Equal(t, []string{"api", "web"}, claims.Audience)
	assert.Equal(t, "external-org", claims.OrganizationID)
	assert.Equal(t, "client", claims.ClientID)
	assert.Equal(t, "current-grant", claims.OAuthGrantID)
	assert.Equal(t, "office", claims.NetworkZone)
	assert.True(t, claims.EmailVerified)

	for _, field := range []string{"tenant_id", "entity_id", "principal_id"} {
		invalid := make(map[string]any, len(raw))
		for key, value := range raw {
			invalid[key] = value
		}
		invalid[field] = "not-an-id"
		_, parseErr := parseClaims(invalid)
		assert.ErrorIs(t, parseErr, ErrInvalidToken, field)
	}

	assert.Zero(t, func() guid.ID {
		id, claimErr := guidClaim(nil)
		require.NoError(t, claimErr)
		return id
	}())
	_, err = guidClaim(float64(11))
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestVerifierErrorBranches(t *testing.T) {
	_, err := (&jwk{Kid: "bad", Kty: "RSA", N: "!", E: "AQAB"}).publicKey()
	assert.Error(t, err)
	_, err = (&jwk{Kid: "bad", Kty: "RSA", N: "AQAB", E: "!"}).publicKey()
	assert.Error(t, err)
	_, err = (&jwk{Kid: "bad", Kty: "oct"}).publicKey()
	assert.Error(t, err)
	_, err = (&jwk{Kty: "RSA"}).publicKey()
	assert.Error(t, err)
	assert.ErrorIs(t, tokenAlgorithms.Verify("RS256", "not-rsa", []byte("a.b"), []byte("sig")), jwsx.ErrKeyType)
	assert.ErrorIs(t, tokenAlgorithms.Verify("HS256", &rsa.PublicKey{}, []byte("a.b"), []byte("sig")), jwsx.ErrUnsupportedAlgorithm)

	var n json.Number = "123"
	assert.False(t, unixClaim(n).IsZero())
	assert.True(t, unixClaim("bad").IsZero())

}

func TestRefreshAndDiscoveryErrors(t *testing.T) {
	statusSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer statusSrv.Close()
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: statusSrv.URL})
	require.NoError(t, err)
	assert.Error(t, verifier.refreshKeys(context.Background()))

	badJWKS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[{"kid":"x","kty":"oct"}]}`))
	}))
	defer badJWKS.Close()
	verifier, err = NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: badJWKS.URL})
	require.NoError(t, err)
	assert.Error(t, verifier.refreshKeys(context.Background()))

	noKey := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer noKey.Close()
	verifier, err = NewVerifier(Config{Enabled: true, Issuer: noKey.URL})
	require.NoError(t, err)
	_, err = verifier.jwksURLValue(context.Background())
	assert.Error(t, err)

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	verifier, err = NewVerifier(Config{Enabled: true, Issuer: closedURL})
	require.NoError(t, err)
	_, err = verifier.jwksURLValue(context.Background())
	assert.Error(t, err)

	badDiscovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{invalid`))
	}))
	defer badDiscovery.Close()
	verifier, err = NewVerifier(Config{Enabled: true, Issuer: badDiscovery.URL})
	require.NoError(t, err)
	_, err = verifier.jwksURLValue(context.Background())
	assert.ErrorContains(t, err, "decode oidc discovery")

	badJWKSJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{invalid`))
	}))
	defer badJWKSJSON.Close()
	verifier, err = NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: badJWKSJSON.URL})
	require.NoError(t, err)
	assert.ErrorContains(t, verifier.refreshKeys(context.Background()), "decode jwks")
}

type testKey struct {
	private *rsa.PrivateKey
	kid     string
}

// TestInt64Claim covers the numeric claim decoder. JSON numbers arrive as
// float64 from encoding/json by default but as json.Number when the decoder is
// configured to preserve precision, and anything else must not be coerced.
func TestInt64Claim(t *testing.T) {
	assert.Equal(t, int64(1700000000), int64Claim(float64(1700000000)))
	assert.Equal(t, int64(1700000000), int64Claim(json.Number("1700000000")))
	assert.Equal(t, int64(0), int64Claim(json.Number("not-a-number")))
	assert.Equal(t, int64(0), int64Claim("1700000000"), "a string must not be coerced into a timestamp")
	assert.Equal(t, int64(0), int64Claim(nil))
	assert.Equal(t, int64(0), int64Claim(map[string]any{}))
}

// TestVerifyRejectsMalformedTokenStructure covers malformed segments and the
// checks that prevent incomplete headers from reaching key selection.
func TestVerifyRejectsMalformedTokenStructure(t *testing.T) {
	key := newTestKey(t)
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": issuer + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{key.jwk()}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	issuer = srv.URL

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: issuer, Audience: []string{"api"}})
	require.NoError(t, err)
	ctx := t.Context()

	// Wrong number of segments.
	for _, token := range []string{"", "onlyone", "two.parts", "a.b.c.d"} {
		_, verifyErr := verifier.Verify(ctx, token)
		assert.ErrorIs(t, verifyErr, ErrInvalidToken, token)
	}

	// Header is not base64url JSON.
	_, err = verifier.Verify(ctx, "!!!."+mustJSONPart(t, map[string]any{"sub": "s"})+".sig")
	assert.ErrorIs(t, err, ErrInvalidToken)

	// Header parses but omits kid or alg, so no key can be selected.
	for _, header := range []map[string]any{
		{"alg": "RS256", "typ": "JWT"},
		{"kid": key.kid, "typ": "JWT"},
		{"typ": "JWT"},
	} {
		token := mustJSONPart(t, header) + "." + mustJSONPart(t, map[string]any{"sub": "s"}) + ".sig"
		_, verifyErr := verifier.Verify(ctx, token)
		assert.ErrorIs(t, verifyErr, ErrInvalidToken)
	}

	// Header is complete but the signature segment is not base64url.
	token := mustJSONPart(t, map[string]any{"alg": "RS256", "kid": key.kid, "typ": "JWT"}) +
		"." + mustJSONPart(t, map[string]any{"sub": "s"}) + ".!!!"
	_, err = verifier.Verify(ctx, token)
	assert.ErrorIs(t, err, ErrInvalidToken)

	// A disabled verifier has no HTTP client, so Verify must report that
	// authentication is off rather than dereference nil.
	disabled, err := NewVerifier(Config{})
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		_, verifyErr := disabled.Verify(ctx, token)
		assert.ErrorIs(t, verifyErr, ErrDisabled)
	})
}

// TestJWKSURLValueCachesDiscovery proves the OIDC discovery document is fetched
// once and then served from cache, so token verification does not issue a
// discovery request per call.
func TestJWKSURLValueCachesDiscovery(t *testing.T) {
	key := newTestKey(t)
	var issuer string
	discoveries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			discoveries++
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": issuer + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{key.jwk()}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	issuer = srv.URL

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: issuer, Audience: []string{"api"}})
	require.NoError(t, err)
	ctx := t.Context()

	resolved, err := verifier.jwksURLValue(ctx)
	require.NoError(t, err)
	assert.Equal(t, issuer+"/jwks", resolved)
	assert.Equal(t, 1, discoveries)

	// The second call is served from the cached value.
	resolved, err = verifier.jwksURLValue(ctx)
	require.NoError(t, err)
	assert.Equal(t, issuer+"/jwks", resolved)
	assert.Equal(t, 1, discoveries, "discovery must not be repeated")

	// An explicitly configured JWKS URL skips discovery entirely.
	explicit, err := NewVerifier(Config{Enabled: true, Issuer: issuer, Audience: []string{"api"}, JWKSURL: issuer + "/jwks"})
	require.NoError(t, err)
	resolved, err = explicit.jwksURLValue(ctx)
	require.NoError(t, err)
	assert.Equal(t, issuer+"/jwks", resolved)
	assert.Equal(t, 1, discoveries)

	// A successful refresh still leaves an unknown kid unresolvable.
	_, err = verifier.key(ctx, "absent-kid")
	assert.ErrorIs(t, err, ErrUnknownKey)
}

func TestConcurrentKeyMissesShareOneRefresh(t *testing.T) {
	key := newTestKey(t)
	body, err := json.Marshal(map[string]any{"keys": []any{key.jwk()}})
	require.NoError(t, err)
	const callers = 20
	var requests atomic.Int32
	var entered atomic.Int32
	allEntered := make(chan struct{})
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/keys"})
	require.NoError(t, err)
	verifier.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		<-allEntered
		return jsonResponse(request, body), nil
	})}
	start := make(chan struct{})
	errorsByCaller := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			if entered.Add(1) == callers {
				close(allEntered)
			}
			_, keyErr := verifier.key(t.Context(), key.kid)
			errorsByCaller <- keyErr
		}()
	}
	close(start)
	for range callers {
		require.NoError(t, <-errorsByCaller)
	}
	assert.Equal(t, int32(1), requests.Load(), "a cold-cache burst must share one JWKS refresh")
}

func TestUnknownKidsAreGloballyRefreshLimited(t *testing.T) {
	key := newTestKey(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key.jwk()}})
	}))
	t.Cleanup(srv.Close)

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return now }
	_, err = verifier.key(t.Context(), "random-1")
	assert.ErrorIs(t, err, ErrUnknownKey)
	_, err = verifier.key(t.Context(), "random-2")
	assert.ErrorIs(t, err, ErrUnknownKey)
	assert.Equal(t, int32(1), requests.Load(), "changing an unknown kid must not bypass the refresh limit")

	now = now.Add(minimumJWKSRefreshInterval)
	_, err = verifier.key(t.Context(), "random-3")
	assert.ErrorIs(t, err, ErrUnknownKey)
	assert.Equal(t, int32(2), requests.Load(), "refresh must resume when the cooldown expires")
}

func TestRefreshesNeverOverlap(t *testing.T) {
	key := newTestKey(t)
	body, err := json.Marshal(map[string]any{"keys": []any{key.jwk()}})
	require.NoError(t, err)
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/keys"})
	require.NoError(t, err)
	verifier.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-releaseResponse
		return jsonResponse(request, body), nil
	})}
	result := make(chan error, 1)
	go func() {
		result <- verifier.refreshKeys(t.Context())
	}()
	<-requestStarted
	lockWasFree := verifier.refreshMu.TryLock()
	if lockWasFree {
		verifier.refreshMu.Unlock()
	}
	assert.False(t, lockWasFree, "the refresh coordinator must stay held through cache replacement")
	close(releaseResponse)
	require.NoError(t, <-result)
}

func TestVerifyRecoversWhenIssuerReusesKeyID(t *testing.T) {
	oldKey := newTestKey(t)
	newKey := newTestKey(t)
	newKey.kid = oldKey.kid
	var current atomic.Pointer[testKey]
	current.Store(&oldKey)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{current.Load().jwk()}})
	}))
	t.Cleanup(srv.Close)

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	claims := map[string]any{"iss": "https://issuer.example", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix()}
	_, err = verifier.Verify(t.Context(), oldKey.sign(t, claims))
	require.NoError(t, err)

	current.Store(&newKey)
	_, err = verifier.Verify(t.Context(), newKey.sign(t, claims))
	require.NoError(t, err, "a signature failure on a cached kid must retry after refreshing keys")
}

func TestSignatureFailuresAcrossCachedKidsShareRefreshCooldown(t *testing.T) {
	first := newTestKey(t)
	second := newTestKey(t)
	second.kid = "kid2"
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{first.jwk(), second.jwk()}})
	}))
	t.Cleanup(srv.Close)
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	require.NoError(t, verifier.refreshKeys(t.Context()))

	invalidToken := func(kid string) string {
		header := mustJSONPart(t, map[string]any{"alg": "RS256", "kid": kid})
		return header + ".e30." + base64.RawURLEncoding.EncodeToString([]byte("invalid signature"))
	}
	_, err = verifier.Verify(t.Context(), invalidToken(first.kid))
	assert.Error(t, err)
	_, err = verifier.Verify(t.Context(), invalidToken(second.kid))
	assert.Error(t, err)
	assert.Equal(t, int32(2), requests.Load(), "one forced refresh updates every cached kid")
}

func TestRefreshRediscoversMovedJWKSEndpoint(t *testing.T) {
	oldKey := newTestKey(t)
	newKey := newTestKey(t)
	newKey.kid = "kid2"
	var issuerURL string
	var moved atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			path := "/keys-v1"
			if moved.Load() {
				path = "/keys-v2"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"jwks_uri": issuerURL + path})
		case "/keys-v1":
			if moved.Load() {
				http.Error(w, "moved", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{oldKey.jwk()}})
		case "/keys-v2":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{newKey.jwk()}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	issuerURL = srv.URL

	verifier, err := NewVerifier(Config{Enabled: true, Issuer: issuerURL})
	require.NoError(t, err)
	require.NoError(t, verifier.refreshKeys(t.Context()))
	_, ok := verifier.cachedKey(oldKey.kid)
	require.True(t, ok)

	moved.Store(true)
	require.NoError(t, verifier.refreshKeys(t.Context()))
	_, ok = verifier.cachedKey(newKey.kid)
	assert.True(t, ok)
	assert.Equal(t, issuerURL+"/keys-v2", verifier.jwksURL)
}

func TestRemoteMetadataResourceLimits(t *testing.T) {
	t.Run("jwks body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat(" ", maxJWKSResponseBytes+1)))
		}))
		t.Cleanup(srv.Close)
		verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
		require.NoError(t, err)
		assert.ErrorContains(t, verifier.refreshKeys(t.Context()), "response exceeds")
	})

	t.Run("discovery body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat(" ", maxDiscoveryResponseBytes+1)))
		}))
		t.Cleanup(srv.Close)
		verifier, err := NewVerifier(Config{Enabled: true, Issuer: srv.URL})
		require.NoError(t, err)
		_, err = verifier.jwksURLValue(t.Context())
		assert.ErrorContains(t, err, "response exceeds")
	})

	t.Run("key count", func(t *testing.T) {
		key := newTestKey(t)
		keys := make([]map[string]string, maxJWKSKeys+1)
		for index := range keys {
			keys[index] = key.jwk()
			keys[index]["kid"] = fmt.Sprintf("kid-%d", index)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		}))
		t.Cleanup(srv.Close)
		verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
		require.NoError(t, err)
		assert.ErrorContains(t, verifier.refreshKeys(t.Context()), "key count")
	})

	t.Run("error body drain", func(t *testing.T) {
		body := strings.NewReader(strings.Repeat("x", maxErrorResponseDrainBytes+1))
		drainErrorResponse(body)
		assert.Equal(t, 1, body.Len(), "error response draining must remain bounded")
	})
}

func TestVerificationClassifiesMetadataFailureAsUnavailable(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	header := mustJSONPart(t, map[string]any{"alg": "RS256", "kid": "new-key"})
	claims := mustJSONPart(t, map[string]any{"iss": "https://issuer.example", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix()})
	_, err = verifier.Verify(t.Context(), header+"."+claims+".c2ln")
	assert.ErrorIs(t, err, ErrUnavailable)
	header = mustJSONPart(t, map[string]any{"alg": "RS256", "kid": "another-new-key"})
	_, err = verifier.Verify(t.Context(), header+"."+claims+".c2ln")
	assert.ErrorIs(t, err, ErrUnavailable, "the refresh cooldown must preserve dependency failure classification")
	assert.Equal(t, int32(1), requests.Load())
}

func TestCanceledRefreshDoesNotThrottleHealthyRequest(t *testing.T) {
	key := newTestKey(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key.jwk()}})
	}))
	t.Cleanup(srv.Close)
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = verifier.key(canceled, key.kid)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = verifier.key(t.Context(), key.kid)
	require.NoError(t, err)
	assert.Equal(t, int32(1), requests.Load(), "one caller's canceled context must not create a shared cooldown")
}

func TestCanceledSignatureRefreshPreservesSharedBackoff(t *testing.T) {
	key := newTestKey(t)
	other := newTestKey(t)
	other.kid = key.kid
	var requests atomic.Int32
	var unavailable atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{key.jwk()}})
	}))
	t.Cleanup(srv.Close)
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	verifier.now = func() time.Time { return now }
	_, err = verifier.key(t.Context(), key.kid)
	require.NoError(t, err)

	now = now.Add(minimumJWKSRefreshInterval)
	unavailable.Store(true)
	_, err = verifier.key(t.Context(), "missing")
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int32(2), requests.Load())

	claims := map[string]any{"iss": "https://issuer.example", "sub": "u1", "exp": now.Add(time.Hour).Unix()}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = verifier.Verify(canceled, other.sign(t, claims))
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = verifier.key(t.Context(), "another-missing")
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, int32(2), requests.Load(), "a canceled forced refresh must not erase an existing dependency backoff")
}

func TestUnsupportedAlgorithmDoesNotFetchKeys(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(srv.Close)
	verifier, err := NewVerifier(Config{Enabled: true, Issuer: "https://issuer.example", JWKSURL: srv.URL})
	require.NoError(t, err)
	// Platform tokens are RS256 or EdDSA only. Every other algorithm --
	// including ones the shared JWS package implements for federation -- is
	// refused before a key is fetched.
	for _, alg := range []string{"HS256", "HS512", "none", "", "rs256", "RS384", "RS512", "PS256", "ES256", "ES384", "ES512", "EdDSA25519"} {
		header := mustJSONPart(t, map[string]any{"alg": alg, "kid": "key"})
		_, err = verifier.Verify(t.Context(), header+".e30.c2ln")
		assert.ErrorIs(t, err, ErrInvalidToken, alg)
	}
	assert.Zero(t, requests.Load())
	assert.Equal(t, jwsx.Algorithms{jwsx.RS256, jwsx.EdDSA}, tokenAlgorithms)
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return testKey{private: key, kid: "kid1"}
}

func (k testKey) jwk() map[string]string {
	e := bigEndian(k.private.PublicKey.E)
	return map[string]string{
		"kty": "RSA",
		"kid": k.kid,
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(k.private.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}
}

func (k testKey) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": k.kid, "typ": "JWT"}
	head := mustJSONPart(t, header)
	body := mustJSONPart(t, claims)
	signed := head + "." + body
	hash := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.private, crypto.SHA256, hash[:])
	require.NoError(t, err)
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func mustJSONPart(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(data)
}

func bigEndian(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte(n)}, out...)
		n >>= 8
	}
	return out
}

func issuerURL(r *http.Request) string {
	return "http://" + r.Host
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(request *http.Request, body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    request,
	}
}

// TestSessionCookieDefaultHasOneSource pins the configuration default to the
// exported constant. They used to be two independent literals in two packages,
// which is exactly how the request path and the published API document came to
// name different cookies.
func TestSessionCookieDefaultHasOneSource(t *testing.T) {
	field, found := reflect.TypeOf(WebConfig{}).FieldByName("CookieName")
	require.True(t, found)
	assert.Equal(t, DefaultSessionCookieName, field.Tag.Get("default"),
		"the struct tag default and the exported constant must be the same name")

	assert.NotContains(t, DefaultSessionCookieName, "_",
		"a bare noun names what the cookie holds; a prefix would name a deployment")
}
