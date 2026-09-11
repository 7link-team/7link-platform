package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/iam/oauth/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/passwordx"
)

var (
	ErrInvalidRequest = errors.New("invalid OAuth request")
	errInvalidClient  = fmt.Errorf("OAuth client authentication failed: %w", ErrInvalidRequest)
)

// errServiceAccountCredentialMissing routes client_credentials attempts that
// are not service-account credentials back to the ordinary OAuth client flow.
var errServiceAccountCredentialMissing = errors.New("service account credential not found")

// Service coordinates OAuth protocol and client-administration workflows.
type Service struct {
	repo        *Repository
	authn       authn.WebService
	consent     ConsentConfig
	consentPage *consentRenderer
	now         func() time.Time
}

const (
	defaultConsentPath = "/oauth/consent"
	defaultConsentTTL  = 10 * time.Minute
)

// ConsentConfig locates and shapes the consent screen. Zero values fall back
// to the built-in screen, so a caller with no opinion still gets a working
// flow.
type ConsentConfig struct {
	// Path is where the authorization endpoint sends a browser that has to
	// consent. Pointing it at an application hosted elsewhere replaces the
	// built-in screen; that application posts the decision back to
	// POST /oauth/consent, which stays registered either way.
	Path string
	// TemplateFile replaces the built-in screen's HTML template.
	TemplateFile string
	// TTL bounds how long a parked authorization request stays answerable.
	TTL time.Duration
}

func (c ConsentConfig) normalized() ConsentConfig {
	if strings.TrimSpace(c.Path) == "" {
		c.Path = defaultConsentPath
	}
	if c.TTL <= 0 {
		c.TTL = defaultConsentTTL
	}
	return c
}

func NewService(
	runtime *appenv.Runtime,
	authenticator authn.WebService,
	ids guid.Source,
	appender audit.Appender,
	principals identity.PrincipalStateStore,
	consent ConsentConfig,
) *Service {
	if runtime == nil || authenticator == nil || ids == nil || appender == nil || principals.Status == nil {
		panic("OAuth service requires runtime, authentication, id generator, audit appender, and principal state store")
	}
	repo := NewRepository(runtime, ids, appender)
	repo.principals = principals
	return &Service{
		repo: repo, authn: authenticator,
		consent: consent.normalized(), consentPage: &consentRenderer{}, now: time.Now,
	}
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ConsentMode selects how the authorization endpoint obtains consent for a
// client. It is a named type rather than a second bool so that it cannot be
// transposed with the neighboring public-client flag at a call site: getting
// those two the wrong way round would be a silent security change.
type ConsentMode string

const (
	// ConsentImplicit lets the authorization endpoint record consent on the
	// subject's behalf, which is what every client did before this flag
	// existed. It stays the default because the server hosts no consent
	// screen; see ConsentExplicit.
	ConsentImplicit ConsentMode = "implicit"
	// ConsentExplicit requires a consent record that already covers the
	// requested scope. Until a consent screen exists there is nothing that can
	// create the first such record, so an explicit client's authorization
	// requests are refused with consent_required.
	ConsentExplicit ConsentMode = "explicit"
)

func (mode ConsentMode) valid() bool {
	return mode == ConsentImplicit || mode == ConsentExplicit
}

type Client struct {
	ID           string      `json:"id"`
	TenantID     string      `json:"tenant_id"`
	Name         string      `json:"name"`
	RedirectURIs []string    `json:"redirect_uris"`
	GrantTypes   []string    `json:"grant_types"`
	Scopes       []string    `json:"scopes"`
	PublicClient bool        `json:"public_client"`
	ConsentMode  ConsentMode `json:"consent_mode"`
	Status       string      `json:"status"`
}

func (s *Service) CreateClient(
	ctx context.Context,
	tenantID guid.ID,
	name string,
	redirects, grants, scopes []string,
	public bool,
	consentMode ConsentMode,
) (Client, string, error) {
	if err := validateClient(tenantID, name, redirects, grants, scopes, public, consentMode); err != nil {
		return Client{}, "", err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Client{}, "", err
	}
	secret, secretHash := "", ""
	if !public {
		secret, err = secureToken(32)
		if err != nil {
			return Client{}, "", err
		}
		secretHash, err = passwordx.Hash(secret)
		if err != nil {
			return Client{}, "", err
		}
	}
	redirectJSON, err := json.Marshal(redirects)
	if err != nil {
		return Client{}, "", err
	}
	now := oauthTime(s.now())
	row := model.Client{
		ID: id, TenantID: tenantID, SecretHash: secretHash, Name: strings.TrimSpace(name),
		RedirectURIs: string(redirectJSON), GrantTypes: strings.Join(grants, ","),
		Scopes: strings.Join(scopes, " "), PublicClient: public,
		ConsentMode: string(consentMode), Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	event := audit.NewDomainEvent(ctx, tenantID, "oauth_client_created", "oauth_client", id)
	event.Detail["public_client"] = public
	event.Detail["consent_mode"] = string(consentMode)
	event.Detail["grant_types"] = grants
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.insertClient(ctx, &row); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Client{}, "", fmt.Errorf("create OAuth client: %w", err)
	}
	return clientFromRow(&row), secret, nil
}

func (s *Service) ListClients(ctx context.Context, tenantID guid.ID) ([]Client, error) {
	if tenantID.Zero() {
		return nil, ErrInvalidRequest
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows, err := repo.listClients(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	clients := make([]Client, len(rows))
	for index, row := range rows {
		clients[index] = clientFromRow(row)
	}
	return clients, nil
}

func (s *Service) UpdateClient(
	ctx context.Context,
	tenantID, id guid.ID,
	name string,
	redirects, grants, scopes []string,
	public bool,
	consentMode ConsentMode,
	status string,
) (Client, error) {
	if id.Zero() || status != "active" && status != "disabled" {
		return Client{}, ErrInvalidRequest
	}
	if err := validateClient(tenantID, name, redirects, grants, scopes, public, consentMode); err != nil {
		return Client{}, err
	}
	redirectJSON, err := json.Marshal(redirects)
	if err != nil {
		return Client{}, err
	}
	updates := map[string]any{
		"name": strings.TrimSpace(name), "redirect_uris": string(redirectJSON),
		"grant_types": strings.Join(grants, ","), "scopes": strings.Join(scopes, " "),
		"public_client": public, "consent_mode": string(consentMode),
		"status": status, "updated_at": oauthTime(s.now()),
	}
	// A secret must never become usable again after a client was public. The
	// rotate endpoint is the only operation that establishes a new secret.
	if public {
		updates["secret_hash"] = ""
	}
	event := audit.NewDomainEvent(ctx, tenantID, "oauth_client_updated", "oauth_client", id)
	event.Detail["public_client"] = public
	event.Detail["consent_mode"] = string(consentMode)
	event.Detail["grant_types"] = grants
	event.Detail["status"] = status
	var row *model.Client
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err = repo.updateClient(ctx, tenantID, id, updates)
		if err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Client{}, err
	}
	return clientFromRow(row), nil
}

func (s *Service) RotateClientSecret(ctx context.Context, tenantID, id guid.ID) (string, error) {
	if tenantID.Zero() || id.Zero() {
		return "", ErrInvalidRequest
	}
	secret, err := secureToken(32)
	if err != nil {
		return "", err
	}
	secretHash, err := passwordx.Hash(secret)
	if err != nil {
		return "", err
	}
	event := audit.NewDomainEvent(ctx, tenantID, "oauth_client_secret_rotated", "oauth_client", id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		_, err := repo.updateClient(ctx, tenantID, id, map[string]any{
			"secret_hash": secretHash, "public_client": false, "updated_at": oauthTime(s.now()),
		})
		if err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}

func (s *Service) DeleteClient(ctx context.Context, tenantID, id guid.ID) error {
	if tenantID.Zero() || id.Zero() {
		return ErrInvalidRequest
	}
	event := audit.NewDomainEvent(ctx, tenantID, "oauth_client_deleted", "oauth_client", id)
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.revokeClientRefreshTokens(ctx, id, oauthTime(s.now())); err != nil {
			return err
		}
		if err := repo.deleteClient(ctx, tenantID, id); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
}

// GrantedConsent is one standing authorization as its subject sees it: which
// application holds it, what that application may do, and when it last did.
type GrantedConsent struct {
	ClientID   string    `json:"client_id"`
	TenantID   string    `json:"tenant_id"`
	ClientName string    `json:"client_name"`
	Scopes     []string  `json:"scopes"`
	GrantedAt  time.Time `json:"granted_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// ListConsents returns every application the principal has authorized. It is
// deliberately not scoped to a tenant: a consent belongs to the person who gave
// it rather than to whichever tenant they are looking at, and a screen that
// showed only one tenant's grants would let the rest survive precisely because
// their subject never saw them.
func (s *Service) ListConsents(ctx context.Context, principalID guid.ID) ([]GrantedConsent, error) {
	if principalID.Zero() {
		return nil, ErrInvalidRequest
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows, err := repo.consentsForPrincipal(ctx, principalID)
	if err != nil {
		return nil, err
	}
	consents := make([]GrantedConsent, len(rows))
	for index, row := range rows {
		consents[index] = GrantedConsent{
			ClientID: row.ClientID.String(), TenantID: row.TenantID.String(), ClientName: row.ClientName,
			Scopes: strings.Fields(row.Scope), GrantedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt,
		}
	}
	return consents, nil
}

// RevokeConsent withdraws one standing authorization on its subject's own
// behalf. Everything the grant already produced goes with it in the same
// transaction, so a revocation that returns success has actually ended the
// application's access rather than only stopped it being renewed.
func (s *Service) RevokeConsent(ctx context.Context, principalID, clientID guid.ID) error {
	if principalID.Zero() || clientID.Zero() {
		return ErrInvalidRequest
	}
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		row, err := repo.lockConsent(ctx, principalID, clientID)
		if err != nil {
			return err
		}
		if row == nil {
			return ErrInvalidRequest
		}
		if err := repo.revokeGrantedTokens(ctx, principalID, clientID, oauthTime(s.now())); err != nil {
			return err
		}
		if err := repo.deleteConsent(ctx, principalID, clientID); err != nil {
			return err
		}
		// The scopes are recorded because the row is about to be gone: without
		// them the trail says access was withdrawn but not which access.
		event := audit.NewDomainEvent(ctx, row.TenantID, "oauth_consent_revoked", "oauth_consent", clientID)
		event.Detail["scopes"] = strings.Fields(row.Scope)
		return repo.appendAudit(ctx, event)
	})
}

func (s *Service) Authorize(
	ctx context.Context,
	cookieHeader, clientID, redirectURI, responseType, scope, state,
	challenge, challengeMethod, nonce, prompt string,
) (string, error) {
	if responseType != "code" || challengeMethod != "S256" || !validPKCEChallenge(challenge) {
		return "", ErrInvalidRequest
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return "", err
	}
	parsedClientID, err := guid.Parse(clientID)
	if err != nil {
		return "", ErrInvalidRequest
	}
	client, err := repo.activeClient(ctx, parsedClientID)
	if err != nil || !containsWord(client.GrantTypes, "authorization_code") ||
		!allowedRedirect(client.RedirectURIs, redirectURI) || !allowedScopes(client.Scopes, scope) {
		return "", ErrInvalidRequest
	}
	claims, err := s.authn.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		// Only an absent or invalid browser credential calls for login. Storage
		// failures and cancellation must retain the protocol server_error path.
		if errors.Is(err, authnext.ErrUnavailable) ||
			(!errors.Is(err, authnext.ErrInvalidSession) && !errors.Is(err, authnext.ErrMissingBearer) &&
				!errors.Is(err, authnext.ErrInvalidCredentials)) {
			return "", err
		}
		if prompt == "none" {
			return authorizationErrorLocation(redirectURI, state, "login_required")
		}
		return "", authnext.ErrInvalidCredentials
	}
	if active, activeErr := repo.activeMember(ctx, client.TenantID, claims.PrincipalID); activeErr != nil || !active {
		return "", ErrInvalidRequest
	}
	consent, err := repo.consent(ctx, client.ID, claims.PrincipalID)
	if err != nil {
		return "", fmt.Errorf("check OAuth consent: %w", err)
	}
	// Three inputs decide what happens next: whether a stored consent already
	// covers the scope, whether the client may skip the screen, and what the
	// request asked for.
	satisfied := consent != nil && containsAllWords(consent.Scope, scope)
	switch prompt {
	case "none":
		// prompt=none forbids any interaction, so an unmet consent is
		// reported instead of collected (OIDC Core 3.1.2.6).
		if !satisfied {
			return authorizationErrorLocation(redirectURI, state, "consent_required")
		}
	case "consent":
		// prompt=consent asks for the screen even when a stored consent would
		// have covered the scope, and it overrides a client's permission to
		// skip it. That override is the only reason the parameter exists.
		return s.startConsent(ctx, repo, client, claims, redirectURI, scope, state, challenge, nonce)
	case "":
		if !satisfied && ConsentMode(client.ConsentMode) != ConsentImplicit {
			return s.startConsent(ctx, repo, client, claims, redirectURI, scope, state, challenge, nonce)
		}
	default:
		// An unrecognized prompt is invalid_request rather than a value that
		// silently inherits the permissive path.
		return "", ErrInvalidRequest
	}
	return s.issueAuthorizationCode(
		ctx, repo, claims, client, redirectURI, scope, state, challenge, nonce,
	)
}

// startConsent parks the authorization request and sends the browser to the
// consent screen. The challenge in the redirect is the only handle on that
// row, and only its hash is stored, so possessing the redirect is what proves
// the browser is answering this particular request.
func (s *Service) startConsent(
	ctx context.Context,
	repo *Repository,
	client *model.Client,
	claims *authnext.Claims,
	redirectURI, scope, state, challenge, nonce string,
) (string, error) {
	value, err := secureToken(32)
	if err != nil {
		return "", err
	}
	now := oauthTime(s.now())
	row := model.ConsentRequest{
		ChallengeHash: hashToken(value), ClientID: client.ID, PrincipalID: claims.PrincipalID,
		RedirectURI: redirectURI, Scope: normalizeWords(scope), State: state,
		CodeChallenge: challenge, Nonce: nonce,
		CreatedAt: now, ExpiresAt: now.Add(s.consentTTL()),
	}
	if err := repo.runInTx(ctx, func(tx *Repository) error {
		if err := tx.lockGrantAdmission(ctx, claims, client.TenantID); err != nil {
			return err
		}
		return tx.insertConsentRequest(ctx, &row)
	}); err != nil {
		return "", err
	}
	return s.consentLocation(value)
}

// ConsentRequest is what a consent screen needs to render one parked
// authorization request.
type ConsentRequest struct {
	Challenge  string    `json:"challenge"`
	ClientID   string    `json:"client_id"`
	ClientName string    `json:"client_name"`
	Scopes     []string  `json:"scopes"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// PendingConsent reads one parked authorization request for rendering. It
// never reveals anything the caller's own session did not start.
func (s *Service) PendingConsent(ctx context.Context, cookieHeader, challenge string) (ConsentRequest, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return ConsentRequest{}, err
	}
	row, client, _, err := s.loadPendingConsent(ctx, repo, cookieHeader, challenge)
	if err != nil {
		return ConsentRequest{}, err
	}
	return ConsentRequest{
		Challenge: challenge, ClientID: client.ID.String(), ClientName: client.Name,
		Scopes: strings.Fields(row.Scope), ExpiresAt: row.ExpiresAt,
	}, nil
}

// ResolveConsent records the subject's decision and returns where the browser
// goes next: back to the client with a code, or with access_denied.
func (s *Service) ResolveConsent(
	ctx context.Context,
	cookieHeader, challenge string,
	approved bool,
) (string, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return "", err
	}
	row, client, claims, err := s.loadPendingConsent(ctx, repo, cookieHeader, challenge)
	if err != nil {
		return "", err
	}
	location := ""
	err = repo.runInTx(ctx, func(tx *Repository) error {
		// Match exit/closure lock order: principal before pending request.
		if err := tx.lockGrantAdmission(ctx, claims, client.TenantID); err != nil {
			return err
		}
		// Burning the challenge first means a replayed decision loses the
		// compare-and-set rather than minting a second authorization code.
		if resolveErr := tx.resolveConsentRequest(ctx, hashToken(challenge), oauthTime(s.now())); resolveErr != nil {
			return resolveErr
		}
		if !approved {
			event := audit.NewDomainEvent(ctx, client.TenantID, "oauth_consent_denied", "oauth_client", client.ID)
			event.Detail["scope"] = row.Scope
			if auditErr := tx.appendAudit(ctx, event); auditErr != nil {
				return auditErr
			}
			var deniedErr error
			location, deniedErr = deniedConsentLocation(row.RedirectURI, row.State)
			return deniedErr
		}
		var issueErr error
		location, issueErr = s.issueAuthorizationCode(
			ctx, tx, claims, client, row.RedirectURI, row.Scope, row.State, row.CodeChallenge, row.Nonce,
		)
		return issueErr
	})
	if err != nil {
		return "", err
	}
	return location, nil
}

// loadPendingConsent resolves a challenge to its parked request, its client,
// and the claims of the session answering it. Every guard here is
// load-bearing: the answering subject must be the one who started the request,
// and the request must still be open and unexpired.
func (s *Service) loadPendingConsent(
	ctx context.Context,
	repo *Repository,
	cookieHeader, challenge string,
) (*model.ConsentRequest, *model.Client, *authnext.Claims, error) {
	claims, err := s.authn.Authenticate(ctx, "", cookieHeader)
	if err != nil {
		return nil, nil, nil, authnext.ErrInvalidCredentials
	}
	if !validConsentChallenge(challenge) {
		return nil, nil, nil, ErrInvalidRequest
	}
	row, err := repo.consentRequest(ctx, hashToken(challenge))
	if err != nil {
		return nil, nil, nil, err
	}
	if row.PrincipalID != claims.PrincipalID || row.ResolvedAt != nil ||
		!row.ExpiresAt.After(oauthTime(s.now())) {
		return nil, nil, nil, ErrInvalidRequest
	}
	client, err := repo.activeClient(ctx, row.ClientID)
	if err != nil {
		return nil, nil, nil, ErrInvalidRequest
	}
	// The client may have been narrowed while the subject was deciding, so the
	// parked request is re-checked against the client as it stands now.
	if !containsWord(client.GrantTypes, "authorization_code") ||
		!allowedRedirect(client.RedirectURIs, row.RedirectURI) ||
		!allowedScopes(client.Scopes, row.Scope) {
		return nil, nil, nil, ErrInvalidRequest
	}
	if active, activeErr := repo.activeMember(ctx, client.TenantID, claims.PrincipalID); activeErr != nil || !active {
		return nil, nil, nil, ErrInvalidRequest
	}
	return row, client, claims, nil
}

func (s *Service) consentTTL() time.Duration { return s.consent.TTL }

// ConsentPath reports where the authorization endpoint sends a browser that
// has to consent, so the HTTP layer can tell whether the built-in screen is
// still the one being served.
func (s *Service) ConsentPath() string { return s.consent.Path }

func (s *Service) consentLocation(challenge string) (string, error) {
	target, err := url.Parse(s.consent.Path)
	if err != nil {
		return "", ErrInvalidRequest
	}
	query := target.Query()
	query.Set("consent_challenge", challenge)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

// deniedConsentLocation reports a refusal to the client the way RFC 6749
// 4.1.2.1 requires, instead of leaving the browser on an error page.
func deniedConsentLocation(redirectURI, state string) (string, error) {
	return authorizationErrorLocation(redirectURI, state, "access_denied")
}

// authorizationErrorLocation is used only after the client redirect is validated.
func authorizationErrorLocation(redirectURI, state, code string) (string, error) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return "", ErrInvalidRequest
	}
	query := target.Query()
	query.Set("error", code)
	if state != "" {
		query.Set("state", state)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

// validConsentChallenge matches what secureToken(32) produces, so a malformed
// value is refused before it ever reaches a database lookup.
func validConsentChallenge(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (s *Service) issueAuthorizationCode(
	ctx context.Context,
	repo *Repository,
	claims *authnext.Claims,
	client *model.Client,
	redirectURI, scope, state, challenge, nonce string,
) (string, error) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return "", ErrInvalidRequest
	}
	code, err := secureToken(32)
	if err != nil {
		return "", err
	}
	grant, err := secureToken(32)
	if err != nil {
		return "", err
	}
	now := oauthTime(s.now())
	codeRow := model.AuthorizationCode{
		CodeHash: hashToken(code), ClientID: client.ID, PrincipalID: claims.PrincipalID,
		RedirectURI: redirectURI, Scope: normalizeWords(scope), CodeChallenge: challenge, Nonce: nonce,
		AuthTime: oauthTime(claims.AuthTime), ACR: claims.ACR, AMR: strings.Join(claims.AMR, " "),
		CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
	consentRow := model.Consent{
		GrantID:     grant,
		PrincipalID: claims.PrincipalID, ClientID: client.ID, TenantID: client.TenantID,
		Scope: normalizeWords(scope), CreatedAt: now, LastUsedAt: now,
	}
	event := audit.NewDomainEvent(ctx, client.TenantID, "oauth_consent_granted", "oauth_client", client.ID)
	event.Detail["scope"] = scope
	err = repo.runInTx(ctx, func(tx *Repository) error {
		if err := tx.lockGrantAdmission(ctx, claims, client.TenantID); err != nil {
			return err
		}
		if err := tx.upsertConsent(ctx, &consentRow); err != nil {
			return err
		}
		current, err := tx.consent(ctx, client.ID, claims.PrincipalID)
		if err != nil {
			return err
		}
		if current.GrantID == "" {
			// Upgrade a legacy standing consent without adopting its old tokens.
			consents := query.BindNames(tx.prefix()).Consents.String()
			if err := tx.writer().WithContext(ctx).Table(consents).
				Where("principal_id = ? AND client_id = ?", claims.PrincipalID, client.ID).
				Update("grant_id", grant).Error; err != nil {
				return err
			}
			current.GrantID = grant
		}
		codeRow.GrantID = current.GrantID
		if err := tx.insertAuthorizationCode(ctx, &codeRow); err != nil {
			return err
		}
		return tx.appendAudit(ctx, event)
	})
	if err != nil {
		return "", err
	}
	query := target.Query()
	query.Set("code", code)
	if state != "" {
		query.Set("state", state)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (s *Service) Token(ctx context.Context, form url.Values, authorization string) (TokenResponse, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return TokenResponse{}, err
	}
	if form.Get("grant_type") == "client_credentials" {
		id, secret, credentialErr := clientCredentials(form.Get("client_id"), form.Get("client_secret"), authorization)
		if credentialErr == nil {
			token, tokenErr := s.serviceAccountToken(ctx, repo, id, secret, form.Get("scope"))
			if tokenErr == nil {
				return token, nil
			}
			if errors.Is(tokenErr, ErrInvalidRequest) {
				return TokenResponse{}, tokenErr
			}
			if !errors.Is(tokenErr, errServiceAccountCredentialMissing) {
				return TokenResponse{}, tokenErr
			}
		}
	}
	client, err := s.authenticateClient(ctx, repo, form.Get("client_id"), form.Get("client_secret"), authorization)
	if err != nil {
		return TokenResponse{}, err
	}
	switch form.Get("grant_type") {
	case "authorization_code":
		return s.authorizationCode(ctx, repo, client, form)
	case "refresh_token":
		return s.refresh(ctx, repo, client, form.Get("refresh_token"))
	case "client_credentials":
		if client.PublicClient || !containsWord(client.GrantTypes, "client_credentials") ||
			!allowedScopes(client.Scopes, form.Get("scope")) {
			return TokenResponse{}, ErrInvalidRequest
		}
		scope := normalizeWords(form.Get("scope"))
		token, expires, issueErr := s.authn.IssueTenantOAuthClientToken(
			ctx, client.ID.String(), client.TenantID.String(), "", scope, client.Name,
		)
		return TokenResponse{AccessToken: token, TokenType: "Bearer", ExpiresIn: expires, Scope: scope}, issueErr
	default:
		return TokenResponse{}, ErrInvalidRequest
	}
}

func (s *Service) serviceAccountToken(
	ctx context.Context,
	repo *Repository,
	id guid.ID,
	secret, requestedScope string,
) (TokenResponse, error) {
	account, err := repo.serviceAccountClient(ctx, id)
	if err != nil {
		return TokenResponse{}, err
	}
	now := oauthTime(s.now())
	valid, verifyErr := passwordx.Verify(account.SecretHash, secret)
	if verifyErr != nil || !valid || account.CredentialRevokedAt != nil ||
		account.CredentialExpiresAt != nil && !account.CredentialExpiresAt.After(now) ||
		account.AccountExpiresAt != nil && !account.AccountExpiresAt.After(now) ||
		account.AccountStatus != "active" || account.PrincipalStatus != "active" ||
		account.MemberStatus != "active" || account.TenantStatus != "active" {
		return TokenResponse{}, errInvalidClient
	}
	scope := normalizeWords(requestedScope)
	if scope == "" {
		scope = normalizeWords(account.Scopes)
	}
	if !allowedScopes(account.Scopes, scope) {
		return TokenResponse{}, ErrInvalidRequest
	}
	token, expires, err := s.authn.IssueTenantServiceAccountToken(
		ctx, account.PrincipalID.String(), account.TenantID.String(), "", scope,
		account.LoginName, account.TokenVersion,
	)
	if err != nil {
		return TokenResponse{}, err
	}
	if err := repo.touchServiceAccountCredential(ctx, account.CredentialID, now); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{AccessToken: token, TokenType: "Bearer", ExpiresIn: expires, Scope: scope}, nil
}

func (s *Service) authorizationCode(
	ctx context.Context,
	repo *Repository,
	client *model.Client,
	form url.Values,
) (TokenResponse, error) {
	code, verifier := form.Get("code"), form.Get("code_verifier")
	if code == "" || !validPKCEVerifier(verifier) {
		return TokenResponse{}, ErrInvalidRequest
	}
	row, err := repo.authorizationCode(ctx, hashToken(code), client.ID)
	if err != nil {
		return TokenResponse{}, ErrInvalidRequest
	}
	now := oauthTime(s.now())
	if row.ConsumedAt != nil || !row.ExpiresAt.After(now) || row.RedirectURI != form.Get("redirect_uri") ||
		!verifyPKCE(row.CodeChallenge, verifier) {
		return TokenResponse{}, ErrInvalidRequest
	}
	if active, activeErr := repo.activeMember(ctx, client.TenantID, row.PrincipalID); activeErr != nil || !active {
		return TokenResponse{}, ErrInvalidRequest
	}
	assurance := oauthAssurance(row.AuthTime, row.ACR, row.AMR, client.ID.String())
	if err := s.checkGrant(ctx, repo, row.PrincipalID, client.ID, client.TenantID, row.GrantID); err != nil {
		return TokenResponse{}, err
	}
	assurance.OAuthGrantID = row.GrantID
	access, expires, err := s.authn.IssueTenantAccessTokenWithAssurance(
		ctx, row.PrincipalID, client.TenantID.String(), "", row.Scope, assurance,
	)
	if err != nil {
		return TokenResponse{}, err
	}
	response := TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: expires, Scope: row.Scope}
	var refresh *model.RefreshToken
	if containsWord(client.GrantTypes, "refresh_token") {
		response.RefreshToken, refresh, err = s.makeRefresh(row.PrincipalID, client.ID, row.Scope, "", assurance)
		if err != nil {
			return TokenResponse{}, err
		}
	}
	if containsWord(row.Scope, "openid") {
		response.IDToken, err = s.authn.IssueTenantIDTokenWithAssurance(
			ctx, row.PrincipalID, client.TenantID.String(), client.ID.String(), row.Nonce, row.Scope, assurance,
		)
		if err != nil {
			return TokenResponse{}, err
		}
	}
	if err := repo.consumeAuthorizationCode(ctx, row.CodeHash, now, refresh); err != nil {
		return TokenResponse{}, err
	}
	return response, nil
}

func (s *Service) refresh(
	ctx context.Context,
	repo *Repository,
	client *model.Client,
	token string,
) (TokenResponse, error) {
	if token == "" || !containsWord(client.GrantTypes, "refresh_token") {
		return TokenResponse{}, ErrInvalidRequest
	}
	row, err := repo.refreshToken(ctx, hashToken(token), client.ID)
	if err != nil {
		return TokenResponse{}, ErrInvalidRequest
	}
	now := oauthTime(s.now())
	if row.UsedAt != nil || row.RevokedAt != nil {
		if err := repo.revokeRefreshFamily(ctx, row.FamilyID, now); err != nil {
			return TokenResponse{}, err
		}
		return TokenResponse{}, ErrInvalidRequest
	}
	if !row.ExpiresAt.After(now) {
		return TokenResponse{}, ErrInvalidRequest
	}
	if active, activeErr := repo.activeMember(ctx, client.TenantID, row.PrincipalID); activeErr != nil || !active {
		return TokenResponse{}, ErrInvalidRequest
	}
	assurance := oauthAssurance(row.AuthTime, row.ACR, row.AMR, client.ID.String())
	if err := s.checkGrant(ctx, repo, row.PrincipalID, client.ID, client.TenantID, row.GrantID); err != nil {
		return TokenResponse{}, err
	}
	assurance.OAuthGrantID = row.GrantID
	next, nextRow, err := s.makeRefresh(row.PrincipalID, client.ID, row.Scope, row.FamilyID, assurance)
	if err != nil {
		return TokenResponse{}, err
	}
	access, expires, err := s.authn.IssueTenantAccessTokenWithAssurance(
		ctx, row.PrincipalID, client.TenantID.String(), "", row.Scope, assurance,
	)
	if err != nil {
		return TokenResponse{}, err
	}
	if err := repo.rotateRefreshToken(ctx, row.IDHash, row.FamilyID, now, nextRow); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: expires,
		RefreshToken: next, Scope: row.Scope,
	}, nil
}

func (s *Service) makeRefresh(
	principalID, clientID guid.ID,
	scope, familyID string,
	assurance authnext.Assurance,
) (string, *model.RefreshToken, error) {
	token, err := secureToken(32)
	if err != nil {
		return "", nil, err
	}
	if familyID == "" {
		familyID, err = secureToken(18)
		if err != nil {
			return "", nil, err
		}
	}
	now := oauthTime(s.now())
	return token, &model.RefreshToken{
		GrantID: assurance.OAuthGrantID,
		IDHash:  hashToken(token), FamilyID: familyID, PrincipalID: principalID, ClientID: clientID,
		Scope: scope, AuthTime: oauthTime(assurance.AuthTime), ACR: assurance.Level,
		AMR: strings.Join(assurance.Methods, " "), CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}, nil
}

func (s *Service) Revoke(ctx context.Context, form url.Values, authorization string) error {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return err
	}
	client, err := s.authenticateClient(ctx, repo, form.Get("client_id"), form.Get("client_secret"), authorization)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			return ErrInvalidRequest
		}
		return err
	}
	token := form.Get("token")
	if token == "" {
		return ErrInvalidRequest
	}
	row, err := repo.refreshToken(ctx, hashToken(token), client.ID)
	if errors.Is(err, ErrInvalidRequest) {
		return nil
	}
	if err != nil {
		return err
	}
	return repo.revokeRefreshFamily(ctx, row.FamilyID, oauthTime(s.now()))
}

// ValidateAccessGrant is called by authn for every application-bound principal
// bearer, including direct resource requests rather than only introspection.
func (s *Service) ValidateAccessGrant(ctx context.Context, claims *authnext.Claims) error {
	if claims == nil || claims.PrincipalID.Zero() {
		return authnext.ErrInvalidToken
	}
	clientID, err := guid.Parse(claims.ClientID)
	if err != nil {
		return authnext.ErrInvalidToken
	}
	tenantID, err := guid.Parse(claims.OrganizationID)
	if err != nil {
		return authnext.ErrInvalidToken
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return fmt.Errorf("%w: %w", authnext.ErrUnavailable, err)
	}
	if err := s.checkGrant(ctx, repo, claims.PrincipalID, clientID, tenantID, claims.OAuthGrantID); err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			return authnext.ErrInvalidToken
		}
		return fmt.Errorf("%w: %w", authnext.ErrUnavailable, err)
	}
	return nil
}

func (*Service) checkGrant(ctx context.Context, repo *Repository, principalID, clientID, tenantID guid.ID, grant string) error {
	if grant == "" {
		return ErrInvalidRequest
	}
	client, err := repo.activeClient(ctx, clientID)
	if err != nil {
		return err
	}
	if client.TenantID != tenantID {
		return ErrInvalidRequest
	}
	row, err := repo.consent(ctx, clientID, principalID)
	if err != nil {
		return err
	}
	if row == nil || row.GrantID != grant || row.TenantID != tenantID {
		return ErrInvalidRequest
	}
	return nil
}

func (s *Service) Introspect(
	ctx context.Context,
	form url.Values,
	authorization string,
) (map[string]any, error) {
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	client, err := s.authenticateClient(ctx, repo, form.Get("client_id"), form.Get("client_secret"), authorization)
	if errors.Is(err, ErrInvalidRequest) || err == nil && client.PublicClient {
		return nil, ErrInvalidRequest
	}
	if err != nil {
		return nil, err
	}
	if form.Get("token") == "" {
		return nil, ErrInvalidRequest
	}
	claims, err := s.authn.Authenticate(ctx, "Bearer "+form.Get("token"), "")
	if err != nil {
		switch {
		case errors.Is(err, authnext.ErrUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, err
		case errors.Is(err, authnext.ErrInvalidToken), errors.Is(err, authnext.ErrExpiredToken),
			errors.Is(err, authnext.ErrUnknownKey), errors.Is(err, authnext.ErrInvalidIssuer), errors.Is(err, authnext.ErrInvalidAud),
			errors.Is(err, authnext.ErrInvalidCredentials), errors.Is(err, authnext.ErrMissingBearer),
			errors.Is(err, authnext.ErrInvalidBearer), errors.Is(err, authnext.ErrInvalidSession):
			// RFC 7662 represents invalid credentials, not failed validation, as inactive.
			return map[string]any{"active": false}, nil
		default:
			return nil, err
		}
	}
	if claims == nil || claims.ClientID != client.ID.String() || claims.OrganizationID != client.TenantID.String() {
		return map[string]any{"active": false}, nil
	}
	return map[string]any{
		"active": true, "sub": claims.Subject, "iss": claims.Issuer,
		"aud": claims.Audience, "exp": claims.ExpiresAt.Unix(),
	}, nil
}

func (s *Service) authenticateClient(
	ctx context.Context,
	repo *Repository,
	formID, formSecret, authorization string,
) (*model.Client, error) {
	id, secret, err := clientCredentials(formID, formSecret, authorization)
	if err != nil {
		return nil, errInvalidClient
	}
	client, err := repo.activeClient(ctx, id)
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			return nil, errInvalidClient
		}
		return nil, err
	}
	if client.PublicClient {
		if secret != "" {
			return nil, errInvalidClient
		}
		return client, nil
	}
	valid, err := passwordx.Verify(client.SecretHash, secret)
	if err != nil || !valid {
		return nil, errInvalidClient
	}
	return client, nil
}

func clientCredentials(formID, formSecret, authorization string) (guid.ID, string, error) {
	id, secret := formID, formSecret
	if strings.HasPrefix(strings.ToLower(authorization), "basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(authorization[6:]))
		if err != nil {
			return 0, "", err
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			return 0, "", ErrInvalidRequest
		}
		id, err = url.QueryUnescape(parts[0])
		if err != nil {
			return 0, "", ErrInvalidRequest
		}
		secret, err = url.QueryUnescape(parts[1])
		if err != nil {
			return 0, "", ErrInvalidRequest
		}
	}
	parsedID, err := guid.Parse(id)
	if err != nil {
		return 0, "", ErrInvalidRequest
	}
	return parsedID, secret, nil
}

func validateClient(
	tenantID guid.ID,
	name string,
	redirects, grants, scopes []string,
	public bool,
	consentMode ConsentMode,
) error {
	if tenantID.Zero() || strings.TrimSpace(name) == "" || len(name) > 128 || len(grants) == 0 || len(scopes) == 0 {
		return ErrInvalidRequest
	}
	if !consentMode.valid() {
		return ErrInvalidRequest
	}
	allowedGrants := []string{"authorization_code", "refresh_token", "client_credentials"}
	for _, grant := range grants {
		if !slices.Contains(allowedGrants, grant) || public && grant == "client_credentials" {
			return ErrInvalidRequest
		}
	}
	if containsWord(strings.Join(grants, " "), "authorization_code") && len(redirects) == 0 {
		return ErrInvalidRequest
	}
	for _, value := range redirects {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Fragment != "" {
			return ErrInvalidRequest
		}
	}
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == "" || strings.ContainsAny(scope, " \t\r\n") {
			return ErrInvalidRequest
		}
	}
	return nil
}

func clientFromRow(row *model.Client) Client {
	var redirects []string
	_ = json.Unmarshal([]byte(row.RedirectURIs), &redirects)
	return Client{
		ID: row.ID.String(), TenantID: row.TenantID.String(), Name: row.Name,
		RedirectURIs: redirects, GrantTypes: strings.Fields(strings.ReplaceAll(row.GrantTypes, ",", " ")),
		Scopes: strings.Fields(row.Scopes), PublicClient: row.PublicClient,
		ConsentMode: ConsentMode(row.ConsentMode), Status: row.Status,
	}
}

func allowedRedirect(encoded, redirectURI string) bool {
	var values []string
	return json.Unmarshal([]byte(encoded), &values) == nil && slices.Contains(values, redirectURI)
}

func allowedScopes(allowed, requested string) bool {
	for _, scope := range strings.Fields(requested) {
		if !containsWord(allowed, scope) {
			return false
		}
	}
	return true
}

func containsWord(words, target string) bool {
	return slices.Contains(strings.Fields(strings.ReplaceAll(words, ",", " ")), target)
}

func normalizeWords(value string) string {
	words := strings.Fields(value)
	slices.Sort(words)
	words = slices.Compact(words)
	return strings.Join(words, " ")
}

func containsAllWords(granted, required string) bool {
	for _, word := range strings.Fields(required) {
		if !containsWord(granted, word) {
			return false
		}
	}
	return true
}

func verifyPKCE(challenge, verifier string) bool {
	if !validPKCEChallenge(challenge) || !validPKCEVerifier(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}

func validPKCEChallenge(value string) bool {
	return validPKCEValue(value, 43, 43, "-_")
}

func validPKCEVerifier(value string) bool {
	return validPKCEValue(value, 43, 128, "-._~")
}

func validPKCEValue(value string, minimum, maximum int, symbols string) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune(symbols, character) {
			continue
		}
		return false
	}
	return true
}

func secureToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func oauthAssurance(authTime time.Time, acr int, amr, clientID string) authnext.Assurance {
	return authnext.Assurance{
		AuthTime: authTime.UTC(), Level: acr, Methods: strings.Fields(amr), ClientID: clientID,
	}
}

func oauthTime(value time.Time) time.Time { return value.UTC().Truncate(time.Millisecond) }
