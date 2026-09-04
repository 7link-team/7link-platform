package federation

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/secretx"
	saml "github.com/crewjam/saml"
	"gorm.io/gorm"
)

// Config controls the inbound federation bridge. The encryption key protects
// upstream client secrets and login state cookies; resolve it from a secret
// manager via encryption_key_file in production.
type Config struct {
	Enabled           bool          `mapstructure:"enabled" description:"enable inbound OIDC identity provider federation" default:"false"`
	EncryptionKey     string        `mapstructure:"encryption_key" description:"base64 32-byte key protecting provider client secrets and login state; prefer encryption_key_file" default:""`
	EncryptionKeyFile string        `mapstructure:"encryption_key_file" description:"file containing the base64 32-byte federation encryption key" default:""`
	HTTPTimeout       time.Duration `mapstructure:"http_timeout" description:"upstream discovery, JWKS, and token endpoint timeout" default:"10s"`
	ClockSkew         time.Duration `mapstructure:"clock_skew" description:"allowed ID token clock skew" default:"30s"`
	StateTTL          time.Duration `mapstructure:"state_ttl" description:"login state cookie lifetime" default:"10m"`
	SAML              SAMLConfig    `mapstructure:"saml" group:"saml"`
}

const federationCipherVersion = "v1"

// LoginStart is the result of beginning a browser login: where to redirect and
// the state cookie the browser must send back.
type LoginStart struct {
	AuthorizationURL string
	StateCookie      string
}

// LoginComplete is the result of finishing the OIDC callback: the opaque
// browser session token and the application URL to return to.
type LoginComplete struct {
	SessionToken string
	ReturnURL    string
}

type Service struct {
	repo       *Repository
	audit      audit.Appender
	identities identity.IdentityProvisioner
	authn      authnextWebService
	nextID     func() (guid.ID, error)
	key        []byte
	oidc       *oidcClient
	stateTTL   time.Duration
	now        func() time.Time
	samlMu     sync.Mutex
	samlCfg    SAMLConfig
	samlKey    *rsa.PrivateKey
	samlCert   *x509.Certificate
	samlSPs    sync.Map
	// SAML SP-initiated login state: cached upstream IdP metadata keyed by
	// provider ID, plus the lazily generated SP signing key for AuthnRequests.
	samlIDPCache sync.Map // providerID -> *saml.EntityDescriptor
	spMu         sync.Mutex
	spKey        *rsa.PrivateKey
	spCert       *x509.Certificate
	replays      *replayRegistry
}

// authnextWebService is an alias-free local view of authn.WebService. Keeping
// the consumer contract in the provider package is required by module.Services.
type authnextWebService interface {
	Authenticate(context.Context, string, string) (*authnext.Claims, error)
	ClearCookie() string
	CookieSecure() bool
	CreateSession(context.Context, guid.ID, time.Time, authnext.Assurance) (string, error)
	Logout(context.Context, string) string
	ResolveReturnURL(string) (string, error)
	SessionCookie(string) string
	SessionCookieName() string
}

// ParseEncryptionKey decodes the federation encryption key from the accepted
// base64 variants; it must decode to exactly 32 bytes.
func ParseEncryptionKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("federation encryption key is required when federation is enabled")
	}
	for _, decoder := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		key, err := decoder.DecodeString(encoded)
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, errors.New("federation encryption key must be base64-encoded 32 bytes")
}

// ResolveEncryptionKey loads and validates the configured federation key so
// startup fails fast instead of surfacing a runtime secret error.
func ResolveEncryptionKey(cfg Config) ([]byte, error) {
	encoded, err := secretx.Resolve("federation.encryption_key", cfg.EncryptionKey, cfg.EncryptionKeyFile, 4096)
	if err != nil {
		return nil, err
	}
	return ParseEncryptionKey(encoded)
}

func NewService(runtime *appenv.Runtime, appender audit.Appender, identities identity.IdentityProvisioner, authn authnextWebService, cfg Config, key []byte, ids guid.Source) *Service {
	if runtime == nil || appender == nil || identities == nil || authn == nil || ids == nil {
		panic("federation service requires database, audit appender, identity provisioner, authentication service, and id generator")
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	skew := cfg.ClockSkew
	if skew <= 0 {
		skew = 30 * time.Second
	}
	stateTTL := cfg.StateTTL
	if stateTTL <= 0 {
		stateTTL = 10 * time.Minute
	}
	return &Service{
		repo: NewRepository(runtime), audit: appender, identities: identities, authn: authn,
		key: key, stateTTL: stateTTL, now: time.Now,
		nextID: func() (guid.ID, error) {
			id, err := ids.Next()
			return guid.ID(id), err
		},
		oidc:    newOIDCClient(&http.Client{Timeout: timeout}, skew, 5*time.Minute),
		replays: newReplayRegistry(10_000),
	}
}

func (s *Service) ListProviders(ctx context.Context, tenantID guid.ID) ([]Provider, error) {
	if tenantID.Zero() {
		return nil, ErrInvalidProvider
	}
	rows, err := s.repo.listProviders(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]Provider, 0, len(rows))
	for _, row := range rows {
		result = append(result, providerFromRow(row))
	}
	return result, nil
}

func (s *Service) CreateProvider(ctx context.Context, tenantID guid.ID, input ProviderInput) (Provider, error) {
	row, secret, err := s.normalizeProvider(tenantID, input)
	if err != nil {
		return Provider{}, err
	}
	if err := s.validateDefaultRole(ctx, s.repo, row.TenantID, optionalFederationID(row.DefaultRoleID)); err != nil {
		return Provider{}, err
	}
	id, err := s.nextID()
	if err != nil {
		return Provider{}, err
	}
	if id.Zero() {
		return Provider{}, ErrInvalidProvider
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	row.ID = id
	row.CreatedAt = now
	row.UpdatedAt = now
	if secret != "" {
		row.ClientSecretCiphertext, err = s.encryptSecret(id, secret)
		if err != nil {
			return Provider{}, err
		}
	}
	if err := s.repo.insertProvider(ctx, &row); err != nil {
		return Provider{}, err
	}
	return providerFromRow(row), nil
}

func (s *Service) UpdateProvider(ctx context.Context, tenantID, id guid.ID, input ProviderInput) (Provider, error) {
	if tenantID.Zero() || id.Zero() {
		return Provider{}, ErrInvalidProvider
	}
	current, err := s.getProvider(ctx, tenantID, id)
	if err != nil {
		return Provider{}, err
	}
	row, secret, err := s.normalizeProvider(tenantID, input)
	if err != nil {
		return Provider{}, err
	}
	if err := s.validateDefaultRole(ctx, s.repo, tenantID, optionalFederationID(row.DefaultRoleID)); err != nil {
		return Provider{}, err
	}
	row.ID = current.ID
	row.TenantID = tenantID
	row.CreatedAt = current.CreatedAt
	row.UpdatedAt = s.now().UTC().Truncate(time.Millisecond)
	row.ClientSecretCiphertext = current.ClientSecretCiphertext
	if secret != "" {
		row.ClientSecretCiphertext, err = s.encryptSecret(row.ID, secret)
		if err != nil {
			return Provider{}, err
		}
	}
	if err := s.repo.updateProvider(ctx, &row); err != nil {
		return Provider{}, err
	}
	return providerFromRow(row), nil
}

func (s *Service) DeleteProvider(ctx context.Context, tenantID, id guid.ID) error {
	if tenantID.Zero() || id.Zero() {
		return ErrInvalidProvider
	}
	return s.repo.deleteProvider(ctx, tenantID, id)
}

// StartLogin begins a browser login for an upstream identity provider: the
// OIDC authorization-code flow, or the SAML SP-initiated flow for SAML
// providers. It resolves the provider, seals the state cookie, and returns the
// upstream redirect URL.
func (s *Service) StartLogin(ctx context.Context, providerID guid.ID, returnURL, callback string) (LoginStart, error) {
	provider, err := s.getProviderByID(ctx, providerID)
	if err != nil {
		return LoginStart{}, err
	}
	if provider.Status != ProviderActive {
		return LoginStart{}, ErrProviderDisabled
	}
	if provider.ProviderType == ProviderSAML {
		redirectURL, stateCookie, err := s.StartSAMLLogin(ctx, providerID, returnURL, callback)
		if err != nil {
			return LoginStart{}, err
		}
		return LoginStart{AuthorizationURL: redirectURL, StateCookie: stateCookie}, nil
	}
	if !validHTTPURL(callback) || !strings.HasSuffix(callback, callbackURL(provider.ID)) {
		return LoginStart{}, ErrInvalidProvider
	}
	resolvedReturnURL, err := s.authn.ResolveReturnURL(returnURL)
	if err != nil {
		return LoginStart{}, err
	}
	state, err := newLoginState(provider.ID.String(), provider.TenantID.String(), resolvedReturnURL, s.stateTTL, s.now().UTC())
	if err != nil {
		return LoginStart{}, err
	}
	sealed, err := sealState(s.key, state)
	if err != nil {
		return LoginStart{}, err
	}
	discovery, err := s.oidc.discovery(ctx, provider.Issuer)
	if err != nil {
		return LoginStart{}, err
	}
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", provider.ClientID)
	query.Set("redirect_uri", callback)
	query.Set("scope", provider.Scopes)
	query.Set("state", state.Nonce)
	query.Set("code_challenge", codeChallenge(state.CodeVerifier))
	query.Set("code_challenge_method", "S256")
	separator := "?"
	if strings.Contains(discovery.AuthorizationEndpoint, "?") {
		separator = "&"
	}
	return LoginStart{
		AuthorizationURL: discovery.AuthorizationEndpoint + separator + query.Encode(),
		StateCookie:      stateCookie(sealed, s.authn.CookieSecure(), int(s.stateTTL.Seconds())),
	}, nil
}

// CompleteLogin finishes the callback: it validates the state cookie, exchanges
// the code, verifies the ID token, provisions or links the identity, and issues
// a browser session.
func (s *Service) CompleteLogin(ctx context.Context, providerID guid.ID, code, state, cookieHeader, callback string) (LoginComplete, error) {
	provider, err := s.getProviderByID(ctx, providerID)
	if err != nil {
		return LoginComplete{}, err
	}
	if provider.Status != ProviderActive {
		return LoginComplete{}, ErrProviderDisabled
	}
	stateValue, err := cookieValue(cookieHeader, stateCookieName)
	if err != nil {
		return LoginComplete{}, ErrOIDCState
	}
	sealedState, err := openState(s.key, stateValue)
	if err != nil {
		return LoginComplete{}, ErrOIDCState
	}
	now := s.now().UTC()
	if sealedState.ProviderID != provider.ID.String() || sealedState.TenantID != provider.TenantID.String() ||
		sealedState.Nonce != state || sealedState.ExpiresAt < now.Unix() ||
		!s.replays.claim("state:"+sealedState.Nonce, time.Unix(sealedState.ExpiresAt, 0), now) {
		return LoginComplete{}, ErrOIDCState
	}
	var clientSecret string
	if provider.ClientSecretCiphertext != "" {
		clientSecret, err = s.decryptSecret(provider.ID, provider.ClientSecretCiphertext)
		if err != nil {
			return LoginComplete{}, err
		}
	}
	rawIDToken, err := s.oidc.exchangeCode(ctx, provider.Issuer, provider.ClientID, clientSecret, code, callback, sealedState.CodeVerifier)
	if err != nil {
		return LoginComplete{}, err
	}
	claims, err := s.oidc.verifyIDToken(ctx, provider.Issuer, provider.ClientID, rawIDToken, s.now().UTC())
	if err != nil {
		return LoginComplete{}, err
	}
	sessionToken, err := s.authenticate(ctx, provider, claims, "oidc")
	if err != nil {
		return LoginComplete{}, err
	}
	return LoginComplete{SessionToken: sessionToken, ReturnURL: sealedState.ReturnURL}, nil
}

// StateClearCookie returns a cookie that expires the login state after the
// callback completes (successfully or not).
func (s *Service) StateClearCookie() string {
	return stateClearCookie(s.authn.CookieSecure())
}

// StartSAMLLogin begins an SP-initiated SAML login for a SAML identity
// provider. It builds a signed HTTP-Redirect AuthnRequest to the IdP's SSO
// endpoint and returns the redirect URL plus the sealed state cookie the
// browser must send back to the callback.
func (s *Service) StartSAMLLogin(ctx context.Context, providerID guid.ID, returnURL, callback string) (string, string, error) {
	provider, err := s.getProviderByID(ctx, providerID)
	if err != nil {
		return "", "", err
	}
	if provider.Status != ProviderActive {
		return "", "", ErrProviderDisabled
	}
	if provider.ProviderType != ProviderSAML {
		return "", "", ErrInvalidProvider
	}
	if !validHTTPURL(callback) || !strings.HasSuffix(callback, callbackURL(provider.ID)) {
		return "", "", ErrInvalidProvider
	}
	resolvedReturnURL, err := s.authn.ResolveReturnURL(returnURL)
	if err != nil {
		return "", "", err
	}
	now := s.now().UTC()
	// The state nonce doubles as the AuthnRequest ID so the callback can bind
	// the response's InResponseTo to the exact request the browser started.
	nonce, err := randomToken(18)
	if err != nil {
		return "", "", err
	}
	verifier, err := randomToken(32)
	if err != nil {
		return "", "", err
	}
	state := loginState{
		ProviderID: provider.ID.String(), TenantID: provider.TenantID.String(), Nonce: "id-" + nonce, CodeVerifier: verifier,
		ReturnURL: resolvedReturnURL, ExpiresAt: now.Add(s.stateTTL).Unix(),
	}
	sealed, err := sealState(s.key, state)
	if err != nil {
		return "", "", err
	}
	sp, err := s.samlServiceProvider(ctx, provider, callback)
	if err != nil {
		return "", "", err
	}
	authnRequest := &saml.AuthnRequest{
		ID:                          state.Nonce,
		Version:                     "2.0",
		IssueInstant:                now,
		Destination:                 sp.GetSSOBindingLocation(saml.HTTPRedirectBinding),
		Issuer:                      &saml.Issuer{Format: samlNameIDEntity, Value: sp.EntityID},
		AssertionConsumerServiceURL: callback,
		ProtocolBinding:             saml.HTTPPostBinding,
	}
	redirectURL, err := authnRequest.Redirect(state.Nonce, sp)
	if err != nil {
		return "", "", fmt.Errorf("%w: build redirect request: %w", ErrSAMLResponse, err)
	}
	return redirectURL.String(), stateCookie(sealed, s.authn.CookieSecure(), int(s.stateTTL.Seconds())), nil
}

// CompleteSAMLLogin finishes an SP-initiated SAML login: it verifies the state
// cookie, parses and verifies the IdP's SAMLResponse, provisions or links the
// identity, and issues a browser session.
func (s *Service) CompleteSAMLLogin(ctx context.Context, providerID guid.ID, samlResponse, cookieHeader, callback string) (string, string, error) {
	provider, err := s.getProviderByID(ctx, providerID)
	if err != nil {
		return "", "", err
	}
	if provider.Status != ProviderActive {
		return "", "", ErrProviderDisabled
	}
	if provider.ProviderType != ProviderSAML {
		return "", "", ErrInvalidProvider
	}
	if !validHTTPURL(callback) || !strings.HasSuffix(callback, callbackURL(provider.ID)) {
		return "", "", ErrInvalidProvider
	}
	stateValue, err := cookieValue(cookieHeader, stateCookieName)
	if err != nil {
		return "", "", ErrOIDCState
	}
	sealedState, err := openState(s.key, stateValue)
	if err != nil {
		return "", "", ErrOIDCState
	}
	now := s.now().UTC()
	if sealedState.ProviderID != provider.ID.String() || sealedState.TenantID != provider.TenantID.String() ||
		sealedState.ExpiresAt < now.Unix() ||
		!s.replays.claim("state:"+sealedState.Nonce, time.Unix(sealedState.ExpiresAt, 0), now) {
		return "", "", ErrOIDCState
	}
	assertion, err := s.parseSAMLResponse(ctx, provider, samlResponse, callback, sealedState.Nonce)
	if err != nil {
		return "", "", err
	}
	if assertion.ID == "" || !s.replays.claim("assertion:"+assertion.ID, time.Unix(sealedState.ExpiresAt, 0), now) {
		return "", "", ErrSAMLResponse
	}
	email := strings.TrimSpace(samlAttribute(assertion, "email"))
	if email == "" {
		email = strings.TrimSpace(samlAttribute(assertion, "Email"))
	}
	displayName := strings.TrimSpace(samlAttribute(assertion, "display_name"))
	if displayName == "" {
		displayName = strings.TrimSpace(samlAttribute(assertion, "displayName"))
	}
	if displayName == "" {
		displayName = strings.TrimSpace(samlAttribute(assertion, "uid"))
	}
	verified := true
	var authTime int64
	if len(assertion.AuthnStatements) > 0 {
		authTime = assertion.AuthnStatements[0].AuthnInstant.Unix()
	}
	claims := idTokenClaims{
		Subject: assertion.Subject.NameID.Value, Email: email, EmailVerified: &verified,
		Name: displayName, PreferredUsername: displayName, AuthTime: authTime,
	}
	sessionToken, err := s.authenticate(ctx, provider, claims, "saml")
	if err != nil {
		return "", "", err
	}
	return sessionToken, sealedState.ReturnURL, nil
}

func (s *Service) authenticate(ctx context.Context, provider providerRow, token idTokenClaims, method string) (string, error) {
	var principalID guid.ID
	now := s.now().UTC()
	provisioned := false
	defaultRoleMissing := false
	err := s.repo.runInTx(ctx, func(tx *Repository) error {
		link, err := tx.identityLink(ctx, provider.ID, provider.TenantID, token.Subject)
		switch {
		case err == nil:
			principalID = link.PrincipalID
		case errors.Is(err, gorm.ErrRecordNotFound):
			if !provider.AutoProvision {
				return ErrProvisioningDisabled
			}
			if token.Email == "" || token.EmailVerified == nil || !*token.EmailVerified {
				return ErrProvisioningUnverified
			}
			displayName := strings.TrimSpace(token.Name)
			if displayName == "" {
				displayName = strings.TrimSpace(token.PreferredUsername)
			}
			id, created, err := s.identities.EnsureExternalPrincipal(ctx, tx.writer(), provider.TenantID, token.Email, displayName, now)
			if err != nil {
				return err
			}
			principalID = id
			provisioned = created
			roleID := optionalFederationID(provider.DefaultRoleID)
			if created && !roleID.Zero() {
				granted, err := tx.grantDefaultRole(ctx, provider.TenantID, roleID, principalID, now)
				if err != nil {
					return err
				}
				defaultRoleMissing = !granted
			}
			link = identityLinkRow{
				ProviderID: provider.ID, TenantID: provider.TenantID, ExternalSubject: token.Subject,
				PrincipalID: principalID, Email: token.Email, DisplayName: displayName,
				LastLoginAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.insertIdentityLink(ctx, &link); err != nil {
				return err
			}
		default:
			return err
		}
		if err := tx.verifyPrincipalAndMember(ctx, provider.TenantID, principalID); err != nil {
			return err
		}
		if err := tx.touchIdentityLink(ctx, provider.ID, provider.TenantID, token.Subject, now); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, provider.TenantID, "federation_login", "identity_provider", provider.ID)
		event.PrincipalID = principalID
		event.Detail["external_subject"] = token.Subject
		event.Detail["provider"] = provider.Name
		event.Detail["email"] = token.Email
		event.Detail["provisioned"] = provisioned
		if defaultRoleMissing {
			event.Detail["default_role_missing"] = true
		}
		return s.audit(ctx, tx.writer(), event)
	})
	if err != nil {
		return "", err
	}
	assurance := authnext.Assurance{AuthTime: now, Level: 1, Methods: []string{method}}
	if token.AuthTime > 0 {
		assurance.AuthTime = time.Unix(token.AuthTime, 0).UTC()
	}
	return s.authn.CreateSession(ctx, principalID, now, assurance)
}

func (s *Service) normalizeProvider(tenantID guid.ID, input ProviderInput) (providerRow, string, error) {
	name := strings.TrimSpace(input.Name)
	providerType := strings.TrimSpace(input.ProviderType)
	if providerType == "" {
		providerType = ProviderOIDC
	}
	issuer, err := normalizeIssuer(input.Issuer)
	clientID := strings.TrimSpace(input.ClientID)
	scopes := normalizeScopes(input.Scopes)
	defaultRoleID := input.DefaultRoleID
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = ProviderActive
	}
	secret := strings.TrimSpace(input.ClientSecret)
	if tenantID.Zero() || name == "" || len(name) > 128 ||
		(providerType != ProviderOIDC && providerType != ProviderSAML) ||
		err != nil || clientID == "" || len(clientID) > 128 ||
		(status != ProviderActive && status != ProviderDisabled) || len(secret) > 2048 {
		return providerRow{}, "", ErrInvalidProvider
	}
	// SAML providers don't require scopes (assertion-driven); OIDC does.
	if providerType == ProviderOIDC && (scopes == "" || len(scopes) > 255) {
		return providerRow{}, "", ErrInvalidProvider
	}
	return providerRow{
		TenantID: tenantID, Name: name, ProviderType: providerType, Issuer: issuer, ClientID: clientID,
		Scopes: scopes, AutoProvision: input.AutoProvision, DefaultRoleID: federationIDPointer(defaultRoleID), Status: status,
	}, secret, nil
}

// validateDefaultRole rejects provider changes that point at a role the tenant
// does not have, so JIT provisioning never silently grants nothing.
func (s *Service) validateDefaultRole(ctx context.Context, repo *Repository, tenantID, roleID guid.ID) error {
	if roleID.Zero() {
		return nil
	}
	exists, err := repo.roleExists(ctx, tenantID, roleID)
	if err != nil {
		return fmt.Errorf("validate default role: %w", err)
	}
	if !exists {
		return ErrProviderRoleMissing
	}
	return nil
}

func (s *Service) getProvider(ctx context.Context, tenantID, id guid.ID) (providerRow, error) {
	return s.repo.getProvider(ctx, tenantID, id)
}

func (s *Service) getProviderByID(ctx context.Context, id guid.ID) (providerRow, error) {
	if id.Zero() {
		return providerRow{}, ErrProviderNotFound
	}
	return s.repo.getProviderByID(ctx, id)
}

func (s *Service) encryptSecret(providerID guid.ID, secret string) (string, error) {
	block, err := aes.NewCipher(purposeKey(s.key, "client-secret"))
	if err != nil {
		return "", fmt.Errorf("create federation cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create federation AEAD: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate federation nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(secret), []byte("federation:secret\x00"+providerID.String()))
	return federationCipherVersion + "." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Service) decryptSecret(providerID guid.ID, encoded string) (string, error) {
	version, payload, ok := strings.Cut(encoded, ".")
	if !ok || version != federationCipherVersion || payload == "" {
		return "", errors.New("unsupported federation client secret ciphertext")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", errors.New("invalid federation client secret ciphertext")
	}
	block, err := aes.NewCipher(purposeKey(s.key, "client-secret"))
	if err != nil {
		return "", fmt.Errorf("create federation cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create federation AEAD: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("invalid federation client secret ciphertext")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte("federation:secret\x00"+providerID.String()))
	if err != nil {
		return "", errors.New("decrypt federation client secret")
	}
	return string(plain), nil
}

func providerFromRow(row providerRow) Provider {
	return Provider{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name, ProviderType: row.ProviderType,
		Issuer: row.Issuer, ClientID: row.ClientID, ClientSecretSet: row.ClientSecretCiphertext != "",
		Scopes: row.Scopes, AutoProvision: row.AutoProvision, DefaultRoleID: optionalFederationID(row.DefaultRoleID),
		Status: row.Status, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func federationIDPointer(id guid.ID) *guid.ID {
	if id.Zero() {
		return nil
	}
	return &id
}

func optionalFederationID(id *guid.ID) guid.ID {
	if id == nil {
		return 0
	}
	return *id
}

func normalizeIssuer(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("invalid issuer")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeScopes(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultScopes
	}
	fields := strings.Fields(value)
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) > 64 || strings.ContainsAny(field, " \t\r\n") {
			return ""
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	if len(result) == 0 {
		return ""
	}
	return strings.Join(result, " ")
}

// purposeKey derives a domain-separated key so one federation encryption key
// never encrypts two different kinds of payload with the same key material.
func purposeKey(key []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("federation:key:" + purpose))
	return mac.Sum(nil)
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func cookieValue(header, name string) (string, error) {
	request := &http.Request{Header: http.Header{"Cookie": []string{header}}}
	cookie, err := request.Cookie(name)
	if err != nil || cookie.Value == "" {
		return "", http.ErrNoCookie
	}
	return cookie.Value, nil
}

// callbackURL is the canonical callback path. The API layer prepends the
// externally visible scheme and host before sending it to the IdP.
func callbackURL(providerID guid.ID) string {
	return "/federation/" + providerID.String() + "/callback"
}
