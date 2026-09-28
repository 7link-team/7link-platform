package federation

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"7link.com/platform/pkg/extension/httpx"
)

const wellKnownPath = "/.well-known/openid-configuration"

// oidcClient talks to one or more upstream identity providers over real HTTP.
// Discovery, JWKS, and ID-token signature checks are implemented with the
// standard library so the trust boundary stays small and auditable.
type oidcClient struct {
	client    *http.Client
	policy    httpx.Policy
	clockSkew time.Duration
	cacheTTL  time.Duration
	mu        sync.Mutex
	cache     map[string]oidcCacheEntry
}

type oidcCacheEntry struct {
	discovery *oidcDiscovery
	keys      map[string]crypto.PublicKey
	fetchedAt time.Time
}

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func newOIDCClient(client *http.Client, policy httpx.Policy, clockSkew, cacheTTL time.Duration) *oidcClient {
	return &oidcClient{client: client, policy: policy, clockSkew: clockSkew, cacheTTL: cacheTTL, cache: map[string]oidcCacheEntry{}}
}

func (c *oidcClient) discovery(ctx context.Context, issuer string) (oidcDiscovery, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	now := time.Now()
	c.mu.Lock()
	cached, ok := c.cache[issuer]
	c.mu.Unlock()
	if ok && now.Sub(cached.fetchedAt) < c.cacheTTL && cached.discovery != nil {
		return *cached.discovery, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+wellKnownPath, nil)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("%w: build discovery request: %w", ErrOIDCDiscovery, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return oidcDiscovery{}, fmt.Errorf("%w: %w", ErrOIDCDiscovery, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return oidcDiscovery{}, fmt.Errorf("%w: status %d", ErrOIDCDiscovery, resp.StatusCode)
	}
	var doc oidcDiscovery
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return oidcDiscovery{}, fmt.Errorf("%w: decode metadata: %w", ErrOIDCDiscovery, err)
	}
	// The issuer URL was judged when it was stored, but these endpoints come
	// out of the provider's own document, so a deployment that refuses
	// plaintext would still send the client secret and the authorization code
	// to an http:// token endpoint without this. Judge them with the same
	// policy rather than only asking whether they parse.
	for _, endpoint := range []string{doc.AuthorizationEndpoint, doc.TokenEndpoint, doc.JWKSURI} {
		if err := c.policy.ValidateURL(endpoint); err != nil {
			return oidcDiscovery{}, fmt.Errorf("%w: %w", ErrOIDCDiscovery, err)
		}
	}
	c.mu.Lock()
	c.cache[issuer] = oidcCacheEntry{discovery: &doc, fetchedAt: now}
	c.mu.Unlock()
	return doc, nil
}

func (c *oidcClient) keys(ctx context.Context, issuer string, refresh bool) (map[string]crypto.PublicKey, error) {
	discovery, err := c.discovery(ctx, issuer)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	c.mu.Lock()
	cached, ok := c.cache[issuer]
	c.mu.Unlock()
	if ok && now.Sub(cached.fetchedAt) < c.cacheTTL && len(cached.keys) > 0 && !refresh {
		return cached.keys, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discovery.JWKSURI, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build jwks request: %w", ErrOIDCTokenInvalid, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch jwks: %w", ErrOIDCTokenInvalid, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w: jwks status %d", ErrOIDCTokenInvalid, resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("%w: decode jwks: %w", ErrOIDCTokenInvalid, err)
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, key := range set.Keys {
		public, err := key.publicKey()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrOIDCTokenInvalid, err)
		}
		keys[key.Kid] = public
	}
	c.mu.Lock()
	c.cache[issuer] = oidcCacheEntry{discovery: &discovery, keys: keys, fetchedAt: now}
	c.mu.Unlock()
	return keys, nil
}

// exchangeCode performs the authorization-code token exchange and returns the
// raw ID token. Client authentication uses basic auth when a secret exists and
// PKCE is always sent so the code stays bound to the browser flow.
func (c *oidcClient) exchangeCode(ctx context.Context, issuer, clientID, clientSecret, code, redirectURI, codeVerifier string) (string, error) {
	discovery, err := c.discovery(ctx, issuer)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("code_verifier", codeVerifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discovery.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: build token request: %w", ErrOIDCToken, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if clientSecret != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrOIDCToken, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("%w: decode token response: %w", ErrOIDCToken, err)
	}
	if body.Error != "" || body.IDToken == "" {
		return "", fmt.Errorf("%w: %s", ErrOIDCToken, body.Error)
	}
	return body.IDToken, nil
}

// verifyIDToken validates signature, issuer, audience, and time claims and
// returns the normalized claims. A failed lookup retries the JWKS once so key
// rotation does not require a manual provider update.
func (c *oidcClient) verifyIDToken(ctx context.Context, issuer, clientID, raw string, now time.Time) (idTokenClaims, error) {
	keys, err := c.keys(ctx, issuer, false)
	if err != nil {
		return idTokenClaims{}, err
	}
	claims, err := c.verifyWithKeys(issuer, clientID, raw, keys, now)
	if errors.Is(err, errUnknownKey) {
		keys, refreshErr := c.keys(ctx, issuer, true)
		if refreshErr != nil {
			return idTokenClaims{}, refreshErr
		}
		claims, err = c.verifyWithKeys(issuer, clientID, raw, keys, now)
	}
	if err != nil {
		return idTokenClaims{}, err
	}
	return claims, nil
}

var errUnknownKey = errors.New("unknown signing key")

func (c *oidcClient) verifyWithKeys(issuer, clientID, raw string, keys map[string]crypto.PublicKey, now time.Time) (idTokenClaims, error) {
	header, payload, signature, err := splitJWT(raw)
	if err != nil {
		return idTokenClaims{}, fmt.Errorf("%w: %w", ErrOIDCTokenInvalid, err)
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJSONPart(header, &head); err != nil {
		return idTokenClaims{}, fmt.Errorf("%w: decode header: %w", ErrOIDCTokenInvalid, err)
	}
	var claims idTokenClaims
	if err := decodeJSONPart(payload, &claims); err != nil {
		return idTokenClaims{}, fmt.Errorf("%w: decode claims: %w", ErrOIDCTokenInvalid, err)
	}
	if claims.Issuer != issuer || claims.Subject == "" || len(claims.Subject) > 128 {
		return idTokenClaims{}, fmt.Errorf("%w: issuer or subject mismatch", ErrOIDCTokenInvalid)
	}
	if !containsAudience(claims.Audience, clientID) {
		return idTokenClaims{}, fmt.Errorf("%w: audience mismatch", ErrOIDCTokenInvalid)
	}
	// OIDC Core 3.1.3.7: multiple audiences require an authorized party;
	// whenever supplied, that party must identify this relying party.
	if (len(claims.Audience) > 1 && claims.AuthorizedParty == nil) ||
		(claims.AuthorizedParty != nil && *claims.AuthorizedParty != clientID) {
		return idTokenClaims{}, fmt.Errorf("%w: authorized party mismatch", ErrOIDCTokenInvalid)
	}
	nowUnix := now.Unix()
	skew := int64(c.clockSkew.Seconds())
	if claims.ExpiresAt <= nowUnix-skew || (claims.NotBefore > 0 && claims.NotBefore > nowUnix+skew) {
		return idTokenClaims{}, fmt.Errorf("%w: token is outside its validity window", ErrOIDCTokenInvalid)
	}
	key, ok := keys[head.Kid]
	if !ok {
		return idTokenClaims{}, fmt.Errorf("%w: %s", errUnknownKey, head.Kid)
	}
	if err := verifySignature(head.Alg, key, []byte(header+"."+payload), signature); err != nil {
		return idTokenClaims{}, err
	}
	return claims, nil
}

type audClaim []string

func (a *audClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*a = many
		return nil
	}
	return errors.New("invalid aud claim")
}

type idTokenClaims struct {
	Nonce             string   `json:"nonce"`
	AuthorizedParty   *string  `json:"azp"`
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	Audience          audClaim `json:"aud"`
	ExpiresAt         int64    `json:"exp"`
	NotBefore         int64    `json:"nbf"`
	IssuedAt          int64    `json:"iat"`
	AuthTime          int64    `json:"auth_time"`
	Email             string   `json:"email"`
	EmailVerified     *bool    `json:"email_verified"`
	Name              string   `json:"name"`
	PreferredUsername string   `json:"preferred_username"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	if k.Kid == "" {
		return nil, errors.New("jwks key missing kid")
	}
	switch k.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("decode rsa modulus: %w", err)
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("decode rsa exponent: %w", err)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		curve, err := curveFor(k.Crv)
		if err != nil {
			return nil, err
		}
		x, err := decodeCoordinate(k.X)
		if err != nil {
			return nil, fmt.Errorf("decode ec x: %w", err)
		}
		y, err := decodeCoordinate(k.Y)
		if err != nil {
			return nil, fmt.Errorf("decode ec y: %w", err)
		}
		// Parse through the SEC 1 uncompressed form so a point that is not on
		// the curve is rejected here instead of being handed to the verifier.
		size := (curve.Params().BitSize + 7) / 8
		if x.BitLen() > size*8 || y.BitLen() > size*8 {
			return nil, errors.New("ec coordinate exceeds curve size")
		}
		point := make([]byte, 1+2*size)
		point[0] = 4
		x.FillBytes(point[1 : 1+size])
		y.FillBytes(point[1+size:])
		public, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return nil, fmt.Errorf("parse ec key: %w", err)
		}
		return public, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported okp curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("decode ed25519 key: %w", err)
		}
		if len(x) != ed25519.PublicKeySize {
			return nil, errors.New("invalid ed25519 public key")
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, fmt.Errorf("unsupported jwks kty %q", k.Kty)
	}
}

func curveFor(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported ec curve %q", crv)
	}
}

func decodeCoordinate(encoded string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(raw), nil
}

func splitJWT(raw string) (header, payload string, signature []byte, err error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", "", nil, errors.New("malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", nil, errors.New("malformed signature")
	}
	return parts[0], parts[1], sig, nil
}

func decodeJSONPart(part string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func verifySignature(alg string, key crypto.PublicKey, signingInput, signature []byte) error {
	switch alg {
	case "RS256", "RS512":
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: RSA key required for %s", ErrOIDCTokenInvalid, alg)
		}
		var digest []byte
		hash := crypto.SHA256
		if alg == "RS512" {
			hash = crypto.SHA512
			sum := sha512.Sum512(signingInput)
			digest = sum[:]
		} else {
			sum := sha256.Sum256(signingInput)
			digest = sum[:]
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, hash, digest, signature); err != nil {
			return fmt.Errorf("%w: RSA signature invalid", ErrOIDCTokenInvalid)
		}
		return nil
	case "ES256", "ES512":
		ecKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: EC key required for %s", ErrOIDCTokenInvalid, alg)
		}
		size := (ecKey.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return fmt.Errorf("%w: malformed ECDSA signature", ErrOIDCTokenInvalid)
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		var digest []byte
		if alg == "ES512" {
			sum := sha512.Sum512(signingInput)
			digest = sum[:]
		} else {
			sum := sha256.Sum256(signingInput)
			digest = sum[:]
		}
		if !ecdsa.Verify(ecKey, digest, r, s) {
			return fmt.Errorf("%w: ECDSA signature invalid", ErrOIDCTokenInvalid)
		}
		return nil
	case "EdDSA":
		edKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: Ed25519 key required for EdDSA", ErrOIDCTokenInvalid)
		}
		if !ed25519.Verify(edKey, signingInput, signature) {
			return fmt.Errorf("%w: EdDSA signature invalid", ErrOIDCTokenInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported alg %s", ErrOIDCTokenInvalid, alg)
	}
}

func containsAudience(got audClaim, want string) bool {
	for _, value := range got {
		if value == want {
			return true
		}
	}
	return false
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
