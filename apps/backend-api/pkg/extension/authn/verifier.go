package authn

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/jwsx"
)

const (
	defaultHTTPTimeout         = 5 * time.Second
	defaultClockSkew           = 30 * time.Second
	minimumJWKSRefreshInterval = 5 * time.Second
	maxDiscoveryResponseBytes  = 64 << 10
	maxJWKSResponseBytes       = 1 << 20
	maxJWKSKeys                = 100
	maxErrorResponseDrainBytes = 64 << 10

	SubjectTypePrincipal      = "principal"
	SubjectTypeOAuthClient    = "oauth_client"
	SubjectTypeService        = "service"
	SubjectTypeServiceAccount = "service_account"
)

var (
	ErrDisabled               = errors.New("authn disabled")
	ErrInvalidToken           = errors.New("invalid token")
	ErrExpiredToken           = errors.New("token expired")
	ErrUnknownKey             = errors.New("unknown jwks key")
	ErrInvalidIssuer          = errors.New("invalid issuer")
	ErrInvalidAud             = errors.New("invalid audience")
	ErrInvalidCredentials     = errors.New("invalid login credentials")
	ErrAdditionalVerification = errors.New("additional verification required")
	ErrRegistrationDisabled   = errors.New("self-service registration disabled")
	ErrInvalidRegistration    = errors.New("invalid self-service registration")
	ErrRegistrationConflict   = errors.New("self-service registration identity exists")
	ErrUnavailable            = errors.New("authentication unavailable")
)

// tokenAlgorithms are the signatures a platform access token may carry: RS256
// from an upstream issuer and EdDSA from the local one (authn/web.go signs
// Ed25519). Widening it widens what this service trusts.
var tokenAlgorithms = jwsx.Algorithms{jwsx.RS256, jwsx.EdDSA}

// Config describes the local issuer and optional upstream JWT verification.
type Config struct {
	Enabled        bool          `mapstructure:"enabled" description:"enable local identity and authentication" default:"false"`
	Issuer         string        `mapstructure:"issuer" description:"canonical local OAuth/OIDC issuer URL"`
	Audience       []string      `mapstructure:"audience" description:"accepted JWT audience values; empty skips audience check"`
	JWKSURL        string        `mapstructure:"jwks_url" description:"optional upstream JWKS URL for federation adapters"`
	HTTPTimeout    time.Duration `mapstructure:"http_timeout" description:"upstream discovery/JWKS HTTP timeout" default:"5s"`
	ClockSkew      time.Duration `mapstructure:"clock_skew" description:"allowed token clock skew" default:"30s"`
	SigningKey     string        `mapstructure:"signing_key" description:"base64 Ed25519 seed; prefer signing_key_file" default:"" secret:"true"`
	SigningKeyFile string        `mapstructure:"signing_key_file" description:"file containing the base64 Ed25519 seed" default:""`
	// The previous signing keys verify tokens that were issued before the
	// current key took over. They never sign anything. A rotation that dropped
	// the old key in the same step would reject every token still in flight, so
	// the old key stays listed here until one access token lifetime has passed
	// and nothing carrying it can still be presented.
	PreviousSigningKeys     []string                `mapstructure:"previous_signing_keys" description:"retired base64 Ed25519 seeds that still verify unexpired tokens; prefer previous_signing_key_files" secret:"true"`
	PreviousSigningKeyFiles []string                `mapstructure:"previous_signing_key_files" description:"files containing retired base64 Ed25519 seeds"`
	AccessTokenTTL          time.Duration           `mapstructure:"access_token_ttl" description:"local OAuth access token lifetime" default:"15m"`
	Web                     WebConfig               `mapstructure:"web" group:"web"`
	Password                PasswordConfig          `mapstructure:"password" group:"password"`
	MFAPolicy               MFAPolicy               `mapstructure:"mfa_policy" group:"mfa_policy"`
	MFA                     MFAConfig               `mapstructure:"mfa" group:"mfa"`
	Passkey                 PasskeyConfig           `mapstructure:"passkey" group:"passkey"`
	Notification            NotificationConfig      `mapstructure:"notification" group:"notification"`
	Recovery                RecoveryConfig          `mapstructure:"recovery" group:"recovery"`
	EmailVerification       EmailVerificationConfig `mapstructure:"email_verification" group:"email_verification"`
	Registration            RegistrationConfig      `mapstructure:"registration" group:"registration"`
	SharedSecurity          SharedSecurityConfig    `mapstructure:"shared_security" group:"shared_security"`
}

// SharedSecurityConfig shares captcha answers and request limits across replicas.
// Enabling it requires an explicit outage policy; availability policy is owned by
// the deployment, not silently selected by the authentication implementation.
type SharedSecurityConfig struct {
	RedisMode            string        `mapstructure:"redis_mode" description:"standalone, cluster, or sentinel; empty preserves the legacy URL-file configuration"`
	RedisAddresses       []string      `mapstructure:"redis_addresses" description:"Redis nodes or Sentinel discovery addresses as host:port, without credentials"`
	RedisMasterName      string        `mapstructure:"redis_master_name" description:"Sentinel master name; required only in sentinel mode"`
	RedisDB              int           `mapstructure:"redis_db" description:"database number; cluster requires zero"`
	RedisUsername        string        `mapstructure:"redis_username" description:"Redis data-node ACL username"`
	RedisPasswordFile    string        `mapstructure:"redis_password_file" description:"file containing the Redis data-node password"`
	SentinelUsername     string        `mapstructure:"sentinel_username" description:"Sentinel ACL username"`
	SentinelPasswordFile string        `mapstructure:"sentinel_password_file" description:"file containing the Sentinel password"`
	RedisTLSEnabled      bool          `mapstructure:"redis_tls_enabled" description:"verify TLS on every Redis and Sentinel connection"`
	RedisTLSCAFile       string        `mapstructure:"redis_tls_ca_file" description:"PEM trust roots for Redis and Sentinel; empty uses system roots"`
	RedisTLSCertFile     string        `mapstructure:"redis_tls_cert_file" description:"PEM client certificate for mutual TLS"`
	RedisTLSKeyFile      string        `mapstructure:"redis_tls_key_file" description:"PEM private key paired with redis_tls_cert_file"`
	RedisTLSServerName   string        `mapstructure:"redis_tls_server_name" description:"optional certificate identity override; normally each discovered node hostname is verified"`
	RedisURLFile         string        `mapstructure:"redis_url_file" description:"file containing redis:// or rediss:// URL; empty retains single-instance captcha storage"`
	Namespace            string        `mapstructure:"namespace" description:"shared namespace unique to this deployment" default:"iam-security"`
	FailurePolicy        string        `mapstructure:"failure_policy" description:"required with Redis: closed blocks authentication attempts on Redis failure; open permits attempts and logs a warning; captcha always fails closed"`
	RequestTimeout       time.Duration `mapstructure:"request_timeout" description:"Redis operation deadline" default:"2s"`
	Window               time.Duration `mapstructure:"window" description:"fixed authentication request limit window" default:"1m"`
	PerIP                int           `mapstructure:"per_ip" description:"maximum POST /authn requests per socket peer per window" default:"60"`
	Global               int           `mapstructure:"global" description:"maximum POST /authn requests across all replicas per window" default:"3000"`
	OAuthPerIP           int           `mapstructure:"oauth_per_ip" description:"maximum POST /oauth token, revocation and introspection requests per socket peer per window" default:"600"`
	OAuthGlobal          int           `mapstructure:"oauth_global" description:"maximum POST /oauth token, revocation and introspection requests across all replicas per window" default:"30000"`
}

// RegistrationConfig enables email-verified self-service global principals.
// Tenant membership is deliberately granted only through invitations.
type RegistrationConfig struct {
	Enabled bool                      `mapstructure:"enabled" description:"enable email-verified self-service registration" default:"false"`
	Captcha RegistrationCaptchaConfig `mapstructure:"captcha" group:"captcha"`
}

// RegistrationCaptchaConfig is which challenge registration presents and, for
// an environment that has to be driven by a script, what it will accept instead
// of one being solved.
type RegistrationCaptchaConfig struct {
	// Kinds is a set rather than one value so a deployment can change what it
	// presents without a code change, and so the harder challenges stay
	// available to a deployment that wants them without being the default.
	Kinds []string `mapstructure:"kinds" description:"registration captcha kinds, picked at random per challenge: text, slide, click; empty offers text only"`
	// TestAnswer is the mechanism Cloudflare Turnstile calls a testing key:
	// the challenge still renders and behaves normally, only its outcome is
	// known ahead of time. It is a configured value rather than a constant in
	// the source so that no string in this repository is a bypass everywhere it
	// was left switched on.
	TestAnswer string `mapstructure:"test_answer" description:"literal answer accepted for any registration captcha so an automated check can register; must be empty in any deployment real people use" default:"" secret:"true"`
}

type Capabilities struct {
	Registration     bool `json:"registration"`
	PasswordRecovery bool `json:"password_recovery"`
	Passkey          bool `json:"passkey"`
}

// NotificationConfig controls the shared durable webhook delivery used by
// authentication notifications. Payloads remain encrypted while queued.
type NotificationConfig struct {
	URL               string        `mapstructure:"url" description:"HTTPS webhook receiving authentication notification events"`
	Authorization     string        `mapstructure:"authorization" description:"optional Authorization header; prefer authorization_file" default:""`
	AuthorizationFile string        `mapstructure:"authorization_file" description:"file containing the optional notification Authorization header" default:""`
	PollInterval      time.Duration `mapstructure:"poll_interval" description:"notification outbox polling interval" default:"5s"`
	RequestTimeout    time.Duration `mapstructure:"request_timeout" description:"notification webhook request timeout" default:"10s"`
	MaxAttempts       int           `mapstructure:"max_attempts" description:"notification delivery attempts before permanent failure" default:"10"`
}

// PasswordConfig controls password policy including lockout thresholds.
// Adjust lockout thresholds based on your threat model; defaults are
// conservative for a typical web-facing identity server.
type PasswordConfig struct {
	MaxFailedAttempts int           `mapstructure:"max_failed_attempts" description:"consecutive failed login attempts before lockout" default:"5"`
	LockDuration      time.Duration `mapstructure:"lock_duration" description:"account lockout duration after max failed attempts" default:"15m"`
}

// MFAPolicy controls whether MFA enrollment is required for authentication.
// When RequireMFA is true, users without MFA enrolled are redirected to the
// enrollment flow after password authentication and cannot access protected
// resources until MFA is configured.
type MFAPolicy struct {
	RequireMFA bool `mapstructure:"require_mfa" description:"require all users to enroll MFA before accessing protected resources" default:"false"`
}

// RecoveryConfig controls password recovery policy. Delivery uses the shared
// Notification configuration.
type RecoveryConfig struct {
	Enabled  bool          `mapstructure:"enabled" description:"enable password recovery" default:"false"`
	TokenTTL time.Duration `mapstructure:"token_ttl" description:"single-use password recovery credential lifetime" default:"15m"`
	Cooldown time.Duration `mapstructure:"cooldown" description:"post-recovery restriction window for high-risk account changes" default:"24h"`
	ResetURL string        `mapstructure:"reset_url" description:"absolute browser URL used to complete password recovery"`
}

// EmailVerificationConfig controls proof of ownership for the current primary
// email. Delivery uses the shared Notification configuration.
type EmailVerificationConfig struct {
	Enabled   bool          `mapstructure:"enabled" description:"enable primary email verification" default:"false"`
	TokenTTL  time.Duration `mapstructure:"token_ttl" description:"single-use email verification credential lifetime" default:"24h"`
	VerifyURL string        `mapstructure:"verify_url" description:"absolute browser URL used to complete email verification"`
}

// PasskeyConfig pins the WebAuthn relying party trust boundary. Values are
// deployment configuration and are never inferred from browser headers.
type PasskeyConfig struct {
	Enabled                   bool          `mapstructure:"enabled" description:"enable passkey registration and passwordless login" default:"false"`
	RPID                      string        `mapstructure:"rp_id" description:"WebAuthn relying party domain without scheme or port"`
	DisplayName               string        `mapstructure:"display_name" description:"relying party name shown by authenticators; defaults to the configured app name" default:""`
	Origins                   []string      `mapstructure:"origins" description:"exact trusted WebAuthn browser origins"`
	ChallengeTTL              time.Duration `mapstructure:"challenge_ttl" description:"one-time registration and login challenge lifetime" default:"5m"`
	MaxCredentials            int           `mapstructure:"max_credentials" description:"maximum active passkeys per principal" default:"10"`
	AttestationPreference     string        `mapstructure:"attestation_preference" description:"WebAuthn attestation conveyance: none, indirect, direct, or enterprise" default:"none"`
	AllowedAttestationFormats []string      `mapstructure:"allowed_attestation_formats" description:"accepted attestation statement formats; empty allows every format"`
	AllowedAAGUIDs            []string      `mapstructure:"allowed_aaguid" description:"accepted 16-byte authenticator AAGUID hex values; empty allows every authenticator"`
}

// DefaultSessionCookieName is the browser session cookie name a deployment
// gets when it configures none. It is named for what it holds rather than
// for whoever runs the deployment, so one build serves them all. It is
// exported because the OpenAPI document has to advertise the same name the
// request path reads, and those two live in different packages.
const DefaultSessionCookieName = "session"

// WebConfig enables database-backed opaque browser sessions.
type WebConfig struct {
	Enabled           bool          `mapstructure:"enabled" description:"enable browser OIDC BFF" default:"false"`
	PostLoginURL      string        `mapstructure:"post_login_url" description:"default frontend URL after login"`
	PostLogoutURL     string        `mapstructure:"post_logout_url" description:"frontend URL after logout"`
	AllowedReturnURLs []string      `mapstructure:"allowed_return_urls" description:"frontend return URL allowlist; each entry admits its exact origin at or below its path"`
	AllowedOrigins    []string      `mapstructure:"allowed_origins" description:"origins allowed for cookie-authenticated writes"`
	CookieName        string        `mapstructure:"cookie_name" description:"opaque session cookie name" default:"session"`
	CookieSecure      bool          `mapstructure:"cookie_secure" description:"require HTTPS for session cookies" default:"true"`
	SessionTTL        time.Duration `mapstructure:"session_ttl" description:"absolute browser session lifetime" default:"8h"`
	IdleTTL           time.Duration `mapstructure:"idle_ttl" description:"browser session inactivity lifetime" default:"30m"`
}

// MFAConfig controls trusted storage and short-lived state for local MFA.
type MFAConfig struct {
	Issuer            string `mapstructure:"issuer" description:"TOTP issuer shown by authenticator applications; defaults to the configured app name" default:""`
	EncryptionKey     string `mapstructure:"encryption_key" description:"base64 32-byte AES key; prefer encryption_key_file" default:"" secret:"true"`
	EncryptionKeyFile string `mapstructure:"encryption_key_file" description:"file containing the base64 32-byte MFA encryption key" default:""`
	// The previous keys open what the key before this one sealed and still
	// recognize the authenticators it derived. They never seal anything, so
	// listing one costs a deployment nothing beyond taking it out again once
	// nothing depends on it.
	PreviousEncryptionKeys     []string      `mapstructure:"previous_encryption_keys" description:"retired base64 32-byte MFA keys that still open stored secrets and recovery codes; prefer previous_encryption_key_files" secret:"true"`
	PreviousEncryptionKeyFiles []string      `mapstructure:"previous_encryption_key_files" description:"files containing retired base64 32-byte MFA keys"`
	EnrollmentTTL              time.Duration `mapstructure:"enrollment_ttl" description:"lifetime of an unconfirmed TOTP enrollment" default:"10m"`
	ChallengeTTL               time.Duration `mapstructure:"challenge_ttl" description:"lifetime of a password-verified MFA login challenge" default:"5m"`
	RecoveryCodes              int           `mapstructure:"recovery_codes" description:"one-time recovery codes issued after enrollment" default:"10"`
	MaxAttempts                int           `mapstructure:"max_attempts" description:"failed codes allowed per login challenge" default:"5"`
}

// Claims contains the identity fields required by authentication and guards.
type Claims struct {
	Issuer            string    `json:"iss"`
	Subject           string    `json:"sub"`
	SubjectType       string    `json:"subject_type,omitempty"`
	Audience          []string  `json:"aud"`
	ExpiresAt         time.Time `json:"exp"`
	NotBefore         time.Time `json:"nbf,omitempty"`
	IssuedAt          time.Time `json:"iat,omitempty"`
	PreferredUsername string    `json:"preferred_username,omitempty"`
	Email             string    `json:"email,omitempty"`
	EmailVerified     bool      `json:"email_verified,omitempty"`
	// Subject is the OIDC protocol subject. Internal database identity and
	// authorization scope use the snowflake fields below.
	//
	// TenantID is the organization root the request is authorized in: a
	// tenant, or an operating platform once the operating-platform guard has
	// rewritten it to the platform. It is not necessarily a tenant.
	TenantID          guid.ID        `json:"tenant_id,omitempty"`
	EntityID          guid.ID        `json:"entity_id,omitempty"`
	PrincipalID       guid.ID        `json:"principal_id,omitempty"`
	OrganizationID    string         `json:"organization_id,omitempty"` // external/federated organization claim
	CredentialVersion int64          `json:"credential_version,omitempty"`
	AuthTime          time.Time      `json:"auth_time,omitempty"`
	ACR               int            `json:"acr,omitempty"`
	AMR               []string       `json:"amr,omitempty"`
	ClientID          string         `json:"client_id,omitempty"`
	OAuthGrantID      string         `json:"oauth_grant_id,omitempty"`
	NetworkZone       string         `json:"network_zone,omitempty"`
	Raw               map[string]any `json:"raw,omitempty"`
	// AccountView is attached only after validating a dedicated server record.
	// Ordinary JWT claims cannot supply this identity.
	AccountView *AccountViewIdentity `json:"-"`
}

// AccountViewIdentity preserves the operator independently of the viewed subject.
type AccountViewIdentity struct {
	ID         guid.ID
	ActorID    guid.ID
	ReadOnly   bool
	ScopeType  string
	ScopeID    guid.ID
	TargetType string
}

func (v *AccountViewIdentity) Valid() bool {
	if v == nil || v.ID <= 0 || v.ActorID <= 0 || v.ScopeID <= 0 {
		return false
	}
	if v.TargetType != "" && v.TargetType != "account" &&
		(v.TargetType != v.ScopeType || (v.TargetType != "platform" && v.TargetType != "tenant")) {
		return false
	}
	switch v.ScopeType {
	case "system", "platform", "tenant", "entity", "personal":
		return true
	default:
		return false
	}
}

func (v *AccountViewIdentity) OrganizationView() bool {
	return v.Valid() && (v.TargetType == "platform" || v.TargetType == "tenant")
}

type Assurance struct {
	OAuthGrantID string
	AuthTime     time.Time
	Level        int
	Methods      []string
	ClientID     string
	NetworkZone  string
}

// Verifier verifies compact JWTs signed by the issuer's JWKS.
type Verifier struct {
	cfg    Config
	client *http.Client
	now    func() time.Time

	mu      sync.RWMutex
	jwksURL string
	keys    map[string]crypto.PublicKey

	refreshMu            sync.Mutex
	lastRefreshAttempt   time.Time
	lastRefreshErr       error
	lastSignatureRefresh time.Time
}

func NewVerifier(cfg Config) (*Verifier, error) {
	if !cfg.Enabled {
		return &Verifier{cfg: cfg}, nil
	}
	cfg.Issuer = strings.TrimRight(cfg.Issuer, "/")
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("authn issuer is required")
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = defaultHTTPTimeout
	}
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = defaultClockSkew
	}
	return &Verifier{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
		now:    time.Now,
		keys:   map[string]crypto.PublicKey{},
	}, nil
}

func (v *Verifier) VerifyAuthorization(ctx context.Context, header string) (*Claims, error) {
	if !v.cfg.Enabled {
		return nil, ErrDisabled
	}
	token, err := bearerToken(header)
	if err != nil {
		return nil, err
	}
	return v.Verify(ctx, token)
}

// Authenticate lets the JWT verifier act as a bearer-only request
// authenticator when browser sessions are disabled.
func (v *Verifier) Authenticate(ctx context.Context, authorization, _ string) (*Claims, error) {
	return v.VerifyAuthorization(ctx, authorization)
}

func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	// A disabled verifier carries no HTTP client or key cache, so this guard
	// must mirror VerifyAuthorization: without it a direct Verify call panics
	// on a nil client instead of reporting that authentication is off.
	if !v.cfg.Enabled {
		return nil, ErrDisabled
	}
	parsed, err := jwsx.Parse(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	header := parsed.Header
	if header.Kid == "" || header.Alg == "" {
		return nil, ErrInvalidToken
	}
	if !tokenAlgorithms.Allows(header.Alg) {
		return nil, fmt.Errorf("%w: unsupported alg %s", ErrInvalidToken, header.Alg)
	}

	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	if err := tokenAlgorithms.Verify(header.Alg, key, parsed.SigningInput, parsed.Signature); err != nil {
		key, refreshErr := v.refreshKey(ctx, header.Kid, true)
		if refreshErr != nil {
			return nil, refreshErr
		}
		if retryErr := tokenAlgorithms.Verify(header.Alg, key, parsed.SigningInput, parsed.Signature); retryErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidToken, retryErr)
		}
	}

	var raw map[string]any
	if err := json.Unmarshal(parsed.Payload, &raw); err != nil {
		return nil, fmt.Errorf("%w: claims: %w", ErrInvalidToken, err)
	}
	claims, err := parseClaims(raw)
	if err != nil {
		return nil, err
	}
	if err := v.validate(claims, v.now()); err != nil {
		return nil, err
	}
	return claims, nil
}

func (v *Verifier) validate(claims *Claims, now time.Time) error {
	if subtle.ConstantTimeCompare([]byte(claims.Issuer), []byte(v.cfg.Issuer)) != 1 {
		return ErrInvalidIssuer
	}
	if claims.Subject == "" {
		return ErrInvalidToken
	}
	if now.After(claims.ExpiresAt.Add(v.cfg.ClockSkew)) {
		return ErrExpiredToken
	}
	if !claims.NotBefore.IsZero() && now.Add(v.cfg.ClockSkew).Before(claims.NotBefore) {
		return ErrInvalidToken
	}
	if len(v.cfg.Audience) > 0 && !audienceAllowed(claims.Audience, v.cfg.Audience) {
		return ErrInvalidAud
	}
	return nil
}

func (v *Verifier) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}
	return v.refreshKey(ctx, kid, false)
}

func (v *Verifier) cachedKey(kid string) (crypto.PublicKey, bool) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	v.mu.RUnlock()
	return key, ok
}

func (v *Verifier) refreshKey(ctx context.Context, kid string, force bool) (crypto.PublicKey, error) {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	if !force {
		if key, ok := v.cachedKey(kid); ok {
			return key, nil
		}
	}
	now := v.now()
	previousRefreshAttempt := v.lastRefreshAttempt
	previousRefreshErr := v.lastRefreshErr
	previousSignatureRefresh := v.lastSignatureRefresh
	if force {
		if !v.lastSignatureRefresh.IsZero() && now.Sub(v.lastSignatureRefresh) < minimumJWKSRefreshInterval {
			if v.lastRefreshErr != nil {
				return nil, errors.Join(ErrUnavailable, v.lastRefreshErr)
			}
			if key, ok := v.cachedKey(kid); ok {
				return key, nil
			}
			return nil, ErrUnknownKey
		}
		v.lastSignatureRefresh = now
	} else if !v.lastRefreshAttempt.IsZero() && now.Sub(v.lastRefreshAttempt) < minimumJWKSRefreshInterval {
		if v.lastRefreshErr != nil {
			return nil, errors.Join(ErrUnavailable, v.lastRefreshErr)
		}
		return nil, ErrUnknownKey
	}
	v.lastRefreshAttempt = now
	if err := v.refreshKeysLocked(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			v.lastRefreshAttempt = previousRefreshAttempt
			v.lastRefreshErr = previousRefreshErr
			if force {
				v.lastSignatureRefresh = previousSignatureRefresh
			}
			return nil, errors.Join(ErrUnavailable, err)
		}
		v.lastRefreshErr = err
		return nil, errors.Join(ErrUnavailable, err)
	}
	v.lastRefreshErr = nil
	if key, ok := v.cachedKey(kid); ok {
		return key, nil
	}
	return nil, ErrUnknownKey
}

func (v *Verifier) refreshKeys(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	previousRefreshAttempt := v.lastRefreshAttempt
	previousRefreshErr := v.lastRefreshErr
	v.lastRefreshAttempt = v.now()
	err := v.refreshKeysLocked(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		v.lastRefreshAttempt = previousRefreshAttempt
		v.lastRefreshErr = previousRefreshErr
	} else {
		v.lastRefreshErr = err
	}
	return err
}

func (v *Verifier) refreshKeysLocked(ctx context.Context) error {
	jwksURL, err := v.jwksURLValue(ctx)
	if err != nil {
		return err
	}
	keys, err := v.fetchKeys(ctx, jwksURL)
	if err != nil && v.cfg.JWKSURL == "" {
		v.invalidateDiscoveredJWKSURL(jwksURL)
		rediscoveredURL, discoveryErr := v.jwksURLValue(ctx)
		if discoveryErr != nil {
			return errors.Join(err, discoveryErr)
		}
		keys, err = v.fetchKeys(ctx, rediscoveredURL)
	}
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

func (v *Verifier) fetchKeys(ctx context.Context, jwksURL string) (map[string]crypto.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		drainErrorResponse(resp.Body)
		return nil, fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := decodeLimitedJSON(resp.Body, maxJWKSResponseBytes, &set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	if len(set.Keys) > maxJWKSKeys {
		return nil, fmt.Errorf("decode jwks: key count %d exceeds limit %d", len(set.Keys), maxJWKSKeys)
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		key, err := k.publicKey()
		if err != nil {
			return nil, err
		}
		keys[k.Kid] = key
	}
	return keys, nil
}

func (v *Verifier) invalidateDiscoveredJWKSURL(url string) {
	v.mu.Lock()
	if v.jwksURL == url {
		v.jwksURL = ""
	}
	v.mu.Unlock()
}

func (v *Verifier) jwksURLValue(ctx context.Context) (string, error) {
	if v.cfg.JWKSURL != "" {
		return v.cfg.JWKSURL, nil
	}
	v.mu.RLock()
	cached := v.jwksURL
	v.mu.RUnlock()
	if cached != "" {
		return cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("discover oidc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		drainErrorResponse(resp.Body)
		return "", fmt.Errorf("discover oidc: status %d", resp.StatusCode)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := decodeLimitedJSON(resp.Body, maxDiscoveryResponseBytes, &doc); err != nil {
		return "", fmt.Errorf("decode oidc discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("oidc discovery has no jwks_uri")
	}
	v.mu.Lock()
	v.jwksURL = doc.JWKSURI
	v.mu.Unlock()
	return doc.JWKSURI, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	if k.Kid == "" {
		return nil, fmt.Errorf("jwks key missing kid")
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
		return &rsa.PublicKey{N: intFromBytes(n), E: int(intFromBytes(e).Int64())}, nil
	case "OKP":
		if k.Crv != "" && k.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported okp crv %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("decode okp public key")
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, fmt.Errorf("unsupported jwks kty %q", k.Kty)
	}
}

func decodeLimitedJSON(reader io.Reader, limit int64, dst any) error {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	return json.Unmarshal(data, dst)
}

func drainErrorResponse(reader io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, maxErrorResponseDrainBytes))
}

func parseClaims(raw map[string]any) (*Claims, error) {
	claims := &Claims{Raw: raw}
	claims.Issuer, _ = raw["iss"].(string)
	claims.Subject, _ = raw["sub"].(string)
	claims.SubjectType, _ = raw["subject_type"].(string)
	claims.PreferredUsername, _ = raw["preferred_username"].(string)
	claims.Email, _ = raw["email"].(string)
	claims.EmailVerified, _ = raw["email_verified"].(bool)
	claims.OrganizationID, _ = raw["organization_id"].(string)
	var err error
	if claims.TenantID, err = guidClaim(raw["tenant_id"]); err != nil {
		return nil, fmt.Errorf("%w: tenant_id", ErrInvalidToken)
	}
	if claims.EntityID, err = guidClaim(raw["entity_id"]); err != nil {
		return nil, fmt.Errorf("%w: entity_id", ErrInvalidToken)
	}
	if claims.PrincipalID, err = guidClaim(raw["principal_id"]); err != nil {
		return nil, fmt.Errorf("%w: principal_id", ErrInvalidToken)
	}
	claims.CredentialVersion = int64Claim(raw["credential_version"])
	claims.AuthTime = unixClaim(raw["auth_time"])
	claims.ACR = int(int64Claim(raw["acr"]))
	claims.AMR = stringSliceClaim(raw["amr"])
	claims.ClientID, _ = raw["client_id"].(string)
	claims.OAuthGrantID, _ = raw["oauth_grant_id"].(string)
	claims.NetworkZone, _ = raw["network_zone"].(string)
	claims.Audience = stringSliceClaim(raw["aud"])
	claims.ExpiresAt = unixClaim(raw["exp"])
	claims.NotBefore = unixClaim(raw["nbf"])
	claims.IssuedAt = unixClaim(raw["iat"])
	if claims.Issuer == "" || claims.Subject == "" || claims.ExpiresAt.IsZero() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func guidClaim(value any) (guid.ID, error) {
	if value == nil {
		return 0, nil
	}
	text, ok := value.(string)
	if !ok {
		return 0, ErrInvalidToken
	}
	return guid.Parse(text)
}

func stringSliceClaim(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func unixClaim(v any) time.Time {
	switch x := v.(type) {
	case float64:
		return time.Unix(int64(x), 0)
	case json.Number:
		n, _ := x.Int64()
		return time.Unix(n, 0)
	default:
		return time.Time{}
	}
}

func int64Claim(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	default:
		return 0
	}
}

func audienceAllowed(tokenAud, allowed []string) bool {
	for _, a := range allowed {
		for _, t := range tokenAud {
			if subtle.ConstantTimeCompare([]byte(a), []byte(t)) == 1 {
				return true
			}
		}
	}
	return false
}

func intFromBytes(data []byte) *big.Int {
	return new(big.Int).SetBytes(data)
}
