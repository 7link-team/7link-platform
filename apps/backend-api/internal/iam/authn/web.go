package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/jwsx"
	"7link.com/platform/pkg/extension/keyringx"
	"7link.com/platform/pkg/extension/passwordx"
	"7link.com/platform/pkg/extension/secretx"
	"7link.com/platform/pkg/extension/webauthnx"
	"gorm.io/gorm"
)

var (
	ErrInvalidSession  = authnext.ErrInvalidSession
	ErrSessionNotFound = authnext.ErrSessionNotFound
	ErrInvalidPassword = authnext.ErrInvalidPassword
	ErrPasswordReused  = authnext.ErrPasswordReused
	ErrCSRF            = errors.New("cross-site request rejected")
)

const mfaEnrollmentSessionMethod = "mfa_enrollment"

// RegistrationPrincipalCreator creates a pending principal and credential on
// the transaction owned by the registration workflow.
type RegistrationPrincipalCreator func(
	context.Context,
	*gorm.DB,
	string,
	string,
	string,
	time.Time,
	guid.Source,
) (guid.ID, error)

// Service coordinates local credentials, sessions, tokens, MFA, passkeys, and
// account recovery through the authn module's repository.
type Service struct {
	cfg                   authnext.Config
	web                   authnext.WebConfig
	repo                  *Repository
	signingKeys           *signingKeyring
	mfaKeys               *keyringx.Keyring
	returnURLs            returnURLAllowlist
	captcha               *captchaOffering
	sharedSecurity        *sharedSecurity
	oauthGrants           OAuthGrantValidator
	personalPermissions   func(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error)
	personalRegistry      *authz.Registry
	passkeys              *webauthnx.Adapter
	now                   func() time.Time
	enricher              claimEnricher
	registrationCreator   RegistrationPrincipalCreator
	memberTenant          core.MemberTenantResolver
	fields                authz.FieldPermissionChecker
	verifiedHook          func(context.Context, *gorm.DB, guid.ID, string, time.Time) error
	recoveryEligibility   func(context.Context, *gorm.DB, guid.ID) error
	federatedProofTrust   func(context.Context, *gorm.DB, guid.ID, string, string) error
	accountViewAuthorizer AccountViewAuthorizer

	notificationAuthorization string
	notificationClient        *http.Client
	notificationMu            sync.Mutex
	notificationCancel        context.CancelFunc
	notificationDone          chan struct{}

	notificationPollFailures      atomic.Int64
	notificationRetryableFailures atomic.Int64
	notificationTerminalFailures  atomic.Int64
}

type ClaimContext struct {
	TokenType string `json:"token_type"`
	Subject   string `json:"subject"`
	TenantID  string `json:"tenant_id,omitempty"`
	Audience  string `json:"audience"`
	Scope     string `json:"scope,omitempty"`
}

type claimEnricher interface {
	Enrich(context.Context, ClaimContext) (map[string]any, error)
}

type WebOption func(*Service)

func WithMemberTenantResolver(resolver core.MemberTenantResolver) WebOption {
	return func(service *Service) { service.memberTenant = resolver }
}

func WithClaimEnricher(enricher claimEnricher) WebOption {
	return func(service *Service) { service.enricher = enricher }
}

func WithVerifiedHook(hook func(context.Context, *gorm.DB, guid.ID, string, time.Time) error) WebOption {
	return func(service *Service) { service.verifiedHook = hook }
}

func WithRegistrationPrincipalCreator(creator RegistrationPrincipalCreator) WebOption {
	return func(service *Service) { service.registrationCreator = creator }
}

func NewWebService(cfg authnext.Config, repository *Repository, options ...WebOption) (*Service, error) {
	service := &Service{
		cfg: cfg, web: cfg.Web, repo: repository, now: time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	// The captcha offering is read before the enabled check so a deployment
	// that misconfigures it is told at startup either way. A challenge kind
	// nobody can spell is a typo whenever it is discovered, and discovering it
	// at somebody's first attempt to register discovers it through them.
	captcha, err := parseCaptchaOffering(cfg.Registration.Captcha)
	if err != nil {
		return nil, err
	}
	service.captcha = captcha
	if !cfg.Enabled {
		return service, nil
	}
	if repository == nil || repository.writer() == nil {
		return nil, errors.New("local authentication requires a writable database")
	}
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("local authentication issuer is required")
	}
	service.cfg.Issuer = strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if len(service.cfg.Audience) == 0 {
		service.cfg.Audience = []string{"7link-api"}
	}
	if service.cfg.AccessTokenTTL <= 0 {
		service.cfg.AccessTokenTTL = 15 * time.Minute
	}
	if service.cfg.ClockSkew <= 0 {
		service.cfg.ClockSkew = 30 * time.Second
	}
	if service.cfg.Password.MaxFailedAttempts <= 0 {
		service.cfg.Password.MaxFailedAttempts = 5
	}
	if service.cfg.Password.LockDuration <= 0 {
		service.cfg.Password.LockDuration = 15 * time.Minute
	}
	if service.web.CookieName == "" {
		service.web.CookieName = authnext.DefaultSessionCookieName
	}
	if service.web.SessionTTL <= 0 {
		service.web.SessionTTL = 8 * time.Hour
	}
	if service.web.IdleTTL <= 0 {
		service.web.IdleTTL = 30 * time.Minute
	}
	if service.web.IdleTTL > service.web.SessionTTL {
		service.web.IdleTTL = service.web.SessionTTL
	}
	returnURLs, err := parseReturnURLAllowlist(service.web.AllowedReturnURLs)
	if err != nil {
		return nil, err
	}
	service.returnURLs = returnURLs
	service.web.PostLoginURL = strings.TrimSpace(service.web.PostLoginURL)
	if service.web.Enabled && (service.web.PostLoginURL == "" || !returnURLs.allows(service.web.PostLoginURL)) {
		return nil, errors.New("authn web post_login_url must be allowed by allowed_return_urls")
	}
	if service.cfg.MFA.Issuer == "" {
		service.cfg.MFA.Issuer = "Identity Platform"
	}
	if service.cfg.MFA.EnrollmentTTL <= 0 {
		service.cfg.MFA.EnrollmentTTL = 10 * time.Minute
	}
	if service.cfg.MFA.ChallengeTTL <= 0 {
		service.cfg.MFA.ChallengeTTL = 5 * time.Minute
	}
	if service.cfg.MFA.RecoveryCodes <= 0 {
		service.cfg.MFA.RecoveryCodes = 10
	}
	if service.cfg.MFA.MaxAttempts <= 0 {
		service.cfg.MFA.MaxAttempts = 5
	}
	if service.cfg.Passkey.ChallengeTTL <= 0 {
		service.cfg.Passkey.ChallengeTTL = 5 * time.Minute
	}
	if service.cfg.Passkey.MaxCredentials <= 0 {
		service.cfg.Passkey.MaxCredentials = 10
	}
	if service.cfg.Passkey.DisplayName == "" {
		service.cfg.Passkey.DisplayName = "Identity Platform"
	}
	if service.cfg.Recovery.TokenTTL <= 0 {
		service.cfg.Recovery.TokenTTL = 15 * time.Minute
	}
	if service.cfg.Recovery.Cooldown <= 0 {
		service.cfg.Recovery.Cooldown = 24 * time.Hour
	}
	if service.cfg.EmailVerification.TokenTTL <= 0 {
		service.cfg.EmailVerification.TokenTTL = 24 * time.Hour
	}
	if service.cfg.Notification.PollInterval <= 0 {
		service.cfg.Notification.PollInterval = 5 * time.Second
	}
	if service.cfg.Notification.RequestTimeout <= 0 {
		service.cfg.Notification.RequestTimeout = 10 * time.Second
	}
	if service.cfg.Notification.MaxAttempts <= 0 {
		service.cfg.Notification.MaxAttempts = 10
	}
	if service.cfg.MFA.RecoveryCodes > 20 || service.cfg.MFA.MaxAttempts > 10 ||
		service.cfg.MFA.EnrollmentTTL > time.Hour || service.cfg.MFA.ChallengeTTL > 15*time.Minute {
		return nil, errors.New("authn MFA configuration exceeds security limits")
	}
	if service.cfg.Passkey.Enabled && (!service.web.Enabled || service.cfg.Passkey.ChallengeTTL > 15*time.Minute || service.cfg.Passkey.MaxCredentials > 20) {
		return nil, errors.New("authn passkey configuration exceeds security limits")
	}
	if service.web.Enabled {
		keys, err := resolveMFAKeyring(service.cfg.MFA)
		if err != nil {
			return nil, err
		}
		service.mfaKeys = keys
	}
	if service.cfg.Passkey.Enabled {
		for _, origin := range service.cfg.Passkey.Origins {
			if !slices.Contains(service.web.AllowedOrigins, origin) {
				return nil, fmt.Errorf("authn passkey origin %q is not an allowed web origin", origin)
			}
		}
		var err error
		service.passkeys, err = webauthnx.New(webauthnx.Config{
			RPID: service.cfg.Passkey.RPID, DisplayName: service.cfg.Passkey.DisplayName,
			Origins:               service.cfg.Passkey.Origins,
			AttestationPreference: webauthnx.AttestationPreference(service.cfg.Passkey.AttestationPreference),
		})
		if err != nil {
			return nil, fmt.Errorf("configure passkeys: %w", err)
		}
		for _, allowed := range service.cfg.Passkey.AllowedAAGUIDs {
			decoded, err := hex.DecodeString(allowed)
			if err != nil {
				return nil, fmt.Errorf("authn passkey allowed_aaguid %q is not hex: %w", allowed, err)
			}
			if len(decoded) != 16 {
				return nil, fmt.Errorf("authn passkey allowed_aaguid %q must encode exactly 16 bytes", allowed)
			}
		}
	}
	if err := service.configureNotification(); err != nil {
		return nil, err
	}
	if err := service.configureRecovery(); err != nil {
		return nil, err
	}
	if err := service.configureEmailVerification(); err != nil {
		return nil, err
	}
	if err := service.configureRegistration(); err != nil {
		return nil, err
	}
	signingKeys, err := resolveSigningKeyring(cfg)
	if err != nil {
		return nil, err
	}
	service.signingKeys = signingKeys
	shared, err := newSharedSecurity(cfg.SharedSecurity)
	if err != nil {
		return nil, err
	}
	service.sharedSecurity = shared
	if shared != nil {
		if service.captcha.testAnswer != "" {
			_ = shared.client.Close()
			return nil, errors.New("shared authentication security cannot enable a captcha test answer")
		}
		service.captcha.store = shared
	}
	return service, nil
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled && s.web.Enabled
}

func (s *Service) SessionCookie(token string) string {
	return (&http.Cookie{
		Name: s.web.CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.web.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.web.SessionTTL.Seconds()),
	}).String()
}

func (s *Service) SessionCookieName() string { return s.web.CookieName }

func (s *Service) ClearCookie() string {
	return (&http.Cookie{
		Name: s.web.CookieName, Path: "/", HttpOnly: true, Secure: s.web.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	}).String()
}

func (s *Service) ResolveReturnURL(returnURL string) (string, error) {
	if !s.Enabled() {
		return "", authnext.ErrDisabled
	}
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" {
		returnURL = s.web.PostLoginURL
	}
	if !s.returnURLs.allows(returnURL) {
		return "", authnext.ErrReturnURL
	}
	return returnURL, nil
}

func (s *Service) CookieSecure() bool { return s != nil && s.web.CookieSecure }

func (s *Service) PostLogoutURL() string {
	if s == nil {
		return ""
	}
	return s.web.PostLogoutURL
}

func (s *Service) Issuer() string {
	if s == nil {
		return ""
	}
	return s.cfg.Issuer
}

func (s *Service) Authenticate(
	ctx context.Context,
	authorization, cookieHeader string,
) (*authnext.Claims, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return nil, authnext.ErrInvalidCredentials
	}
	if strings.TrimSpace(authorization) != "" {
		if scheme, raw, ok := strings.Cut(strings.TrimSpace(authorization), " "); ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(raw, "avt_") {
			return s.AuthenticateAccountView(ctx, raw)
		}
		if scheme, raw, ok := strings.Cut(strings.TrimSpace(authorization), " "); ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(raw, "pat_") {
			return s.authenticatePersonalToken(ctx, raw)
		}
		claims, err := s.verifyAccessToken(authorization)
		if err != nil {
			return nil, err
		}
		if err := s.verifySubjectState(ctx, claims); err != nil {
			return nil, err
		}
		return claims, nil
	}
	return s.authenticateBrowserSession(ctx, cookieHeader, false)
}

func (s *Service) authenticateBrowserSession(
	ctx context.Context,
	cookieHeader string,
	allowMFAEnrollment bool,
) (*authnext.Claims, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return nil, authnext.ErrInvalidCredentials
	}
	if !s.Enabled() || s.repo == nil {
		return nil, authnext.ErrMissingBearer
	}
	token, err := cookieValue(cookieHeader, s.web.CookieName)
	if err != nil {
		return nil, ErrInvalidSession
	}
	now := s.now().UTC()
	session, err := s.repo.activeSession(ctx, tokenHash(token), now)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidSession
		}
		return nil, fmt.Errorf("%w: load session: %w", authnext.ErrUnavailable, err)
	}
	enrollmentSession := session.ACR == 0 && slices.Contains(strings.Fields(session.AMR), mfaEnrollmentSessionMethod)
	if session.ACR < 1 && !(allowMFAEnrollment && enrollmentSession) {
		return nil, ErrInvalidSession
	}
	nextExpiry := now.Add(s.web.IdleTTL)
	if nextExpiry.After(session.AbsoluteExpiresAt) {
		nextExpiry = session.AbsoluteExpiresAt
	}
	_, _ = s.repo.touchSession(ctx, session.IDHash, now, nextExpiry)
	claims, err := s.claims(ctx, session.PrincipalID)
	if err != nil {
		return nil, err
	}
	claims.AuthTime = session.AuthTime.UTC()
	claims.ACR = session.ACR
	claims.AMR = strings.Fields(session.AMR)
	return claims, nil
}

func (s *Service) BeginLogin(
	ctx context.Context,
	loginName, password, returnURL string,
) (authnext.LoginResult, error) {
	if !s.Enabled() || s.repo == nil {
		return authnext.LoginResult{}, authnext.ErrDisabled
	}
	resolvedReturnURL, err := s.ResolveReturnURL(returnURL)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	principal, err := s.repo.findLoginPrincipal(ctx, normalizeLogin(loginName))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			verifyDummy(password)
			return authnext.LoginResult{}, authnext.ErrInvalidCredentials
		}
		return authnext.LoginResult{}, fmt.Errorf("find principal: %w", err)
	}
	credential, err := s.repo.getCredential(ctx, principal.ID)
	if err != nil {
		return authnext.LoginResult{}, fmt.Errorf("read credential: %w", err)
	}
	now := s.now().UTC()
	if principal.Status != "active" || principal.ActivationRequired ||
		(credential.LockedUntil != nil && credential.LockedUntil.After(now)) {
		s.audit(ctx, principal.ID, "login", "denied")
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	valid, err := passwordx.Verify(credential.PasswordHash, password)
	if err != nil {
		return authnext.LoginResult{}, fmt.Errorf("verify credential: %w", err)
	}
	if !valid {
		if err := s.repo.recordLoginFailure(
			ctx, principal.ID, s.cfg.Password.MaxFailedAttempts, s.cfg.Password.LockDuration, now,
		); err != nil {
			return authnext.LoginResult{}, fmt.Errorf("record login failure: %w", err)
		}
		s.audit(ctx, principal.ID, "login", "denied")
		return authnext.LoginResult{}, authnext.ErrInvalidCredentials
	}
	if err := s.repo.resetLoginFailures(ctx, principal.ID, now); err != nil {
		return authnext.LoginResult{}, fmt.Errorf("reset login failures: %w", err)
	}
	if credential.TOTPSecret == "" && (s.cfg.MFAPolicy.RequireMFA || credential.MFARequired) {
		token, err := s.insertSession(ctx, s.repo, principal.ID, now, authnext.Assurance{
			AuthTime: now, Level: 0, Methods: []string{"pwd", mfaEnrollmentSessionMethod},
		})
		if err != nil {
			return authnext.LoginResult{}, err
		}
		expiresAt := now.Add(min(s.web.IdleTTL, s.web.SessionTTL)).UTC()
		s.audit(ctx, principal.ID, "login_password", "success")
		return authnext.LoginResult{
			Status: "mfa_enrollment_required", Subject: principal.ID.String(), ReturnURL: resolvedReturnURL, SessionID: token,
			ExpiresAt: &expiresAt, Methods: []string{"totp"},
		}, nil
	}
	if credential.MFARequired {
		result, err := s.createLoginChallenge(ctx, principal.ID, resolvedReturnURL, now)
		if err != nil {
			return authnext.LoginResult{}, err
		}
		s.audit(ctx, principal.ID, "login_password", "success")
		return result, nil
	}
	token, err := s.insertSession(ctx, s.repo, principal.ID, now, authnext.Assurance{
		AuthTime: now, Level: 1, Methods: []string{"pwd"},
	})
	if err != nil {
		return authnext.LoginResult{}, err
	}
	s.audit(ctx, principal.ID, "login", "success")
	return authnext.LoginResult{Status: "authenticated", ReturnURL: resolvedReturnURL, SessionID: token}, nil
}

func (s *Service) Login(
	ctx context.Context,
	loginName, password, returnURL string,
) (string, string, error) {
	result, err := s.BeginLogin(ctx, loginName, password, returnURL)
	if err != nil {
		return "", "", err
	}
	if result.Status != "authenticated" {
		return "", result.ReturnURL, authnext.ErrAdditionalVerification
	}
	return result.SessionID, result.ReturnURL, nil
}

func (s *Service) ValidateCSRF(method, origin, cookieHeader, authorization string) error {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions ||
		method == http.MethodTrace || strings.TrimSpace(authorization) != "" {
		return nil
	}
	if _, err := cookieValue(cookieHeader, s.web.CookieName); err != nil {
		return nil //nolint:nilerr // No session cookie means no cookie-authenticated CSRF surface.
	}
	return s.ValidateLoginOrigin(origin)
}

func (s *Service) ValidateLoginOrigin(origin string) error {
	if s == nil || origin == "" || !slices.Contains(s.web.AllowedOrigins, origin) {
		return ErrCSRF
	}
	return nil
}

func (s *Service) Logout(ctx context.Context, cookieHeader string) (string, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return "", authnext.ErrInvalidCredentials
	}
	if s == nil {
		return "", authnext.ErrDisabled
	}
	if token, err := cookieValue(cookieHeader, s.web.CookieName); err == nil {
		if s.repo == nil {
			return "", errors.New("revoke browser session: authentication storage unavailable")
		}
		if _, err := s.repo.revokeSessionByHash(ctx, tokenHash(token), s.now().UTC()); err != nil {
			return "", fmt.Errorf("revoke browser session: %w", err)
		}
	}
	return s.web.PostLogoutURL, nil
}

func (s *Service) ListSessions(
	ctx context.Context,
	authorization, cookieHeader string,
) ([]authnext.BrowserSession, error) {
	claims, err := s.personalReadClaims(ctx, authorization, cookieHeader)
	if err != nil {
		return nil, err
	}
	current := currentSessionHash(cookieHeader, s.web.CookieName)
	if claims.AccountView != nil {
		current = ""
	}
	rows, err := s.repo.listActiveSessions(ctx, claims.PrincipalID, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list browser sessions: %w", err)
	}
	result := make([]authnext.BrowserSession, 0, len(rows))
	for _, row := range rows {
		result = append(result, authnext.BrowserSession{
			ID: row.IDHash, Current: row.IDHash == current,
			CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt,
			ExpiresAt: row.ExpiresAt, AbsoluteExpiresAt: row.AbsoluteExpiresAt,
			IPAddress: row.IPAddress, UserAgent: row.UserAgent,
		})
	}
	return result, nil
}

// GetSession reads an active item through the same owner-scoped projection as listing.
func (s *Service) GetSession(ctx context.Context, authorization, cookieHeader, id string) (authnext.BrowserSession, error) {
	sessions, err := s.ListSessions(ctx, authorization, cookieHeader)
	if err != nil {
		return authnext.BrowserSession{}, err
	}
	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return authnext.BrowserSession{}, ErrSessionNotFound
}

func (s *Service) RevokeSession(
	ctx context.Context,
	authorization, cookieHeader, id string,
) error {
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	if decoded, decodeErr := hex.DecodeString(id); decodeErr != nil || len(decoded) != sha256.Size {
		return ErrSessionNotFound
	}
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		changed, err := repository.revokeSession(ctx, claims.PrincipalID, id, s.now().UTC())
		if err != nil {
			return err
		}
		if !changed {
			return ErrSessionNotFound
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "session_revoke", "success"))
	})
	if err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	return nil
}

func (s *Service) LogoutAll(ctx context.Context, authorization, cookieHeader string) error {
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.revokeSessions(ctx, claims.PrincipalID, "", now); err != nil {
			return err
		}
		if _, err := repository.revokeRefreshTokens(ctx, claims.PrincipalID, now); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "logout_all", "success"))
	})
	if err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	return nil
}

func (s *Service) ChangePassword(
	ctx context.Context,
	authorization, cookieHeader, currentPassword, newPassword string,
) error {
	if currentPassword == "" || len(newPassword) < 12 || len(newPassword) > 1024 {
		return ErrInvalidPassword
	}
	claims, err := s.authenticateAccountHolder(ctx, authorization, cookieHeader)
	if err != nil {
		return err
	}
	credential, err := s.repo.getCredential(ctx, claims.PrincipalID)
	if err != nil {
		return fmt.Errorf("load credential: %w", err)
	}
	valid, err := passwordx.Verify(credential.PasswordHash, currentPassword)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !valid {
		return authnext.ErrInvalidCredentials
	}
	history, err := s.repo.listPasswordHistory(ctx, claims.PrincipalID, 5)
	if err != nil {
		return fmt.Errorf("load password history: %w", err)
	}
	passwordHashes := make([]string, 0, len(history)+1)
	passwordHashes = append(passwordHashes, credential.PasswordHash)
	for _, row := range history {
		passwordHashes = append(passwordHashes, row.PasswordHash)
	}
	for _, encoded := range passwordHashes {
		reused, verifyErr := passwordx.Verify(encoded, newPassword)
		if verifyErr != nil {
			return fmt.Errorf("verify password history: %w", verifyErr)
		}
		if reused {
			return ErrPasswordReused
		}
	}
	hash, err := passwordx.Hash(newPassword)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	currentSession := currentSessionHash(cookieHeader, s.web.CookieName)
	err = s.repo.runInTx(ctx, func(repository *Repository) error {
		if err := repository.appendPasswordHistory(ctx, claims.PrincipalID, credential.PasswordHash, now); err != nil {
			return err
		}
		if err := repository.trimPasswordHistory(ctx, claims.PrincipalID, passwordHistoryDepth); err != nil {
			return err
		}
		changed, err := repository.updatePassword(
			ctx, claims.PrincipalID, credential.PasswordHash, hash, now, true, nil,
		)
		if err != nil {
			return err
		}
		if !changed {
			return ErrInvalidPassword
		}
		if err := repository.revokeOtherAuthentication(ctx, claims.PrincipalID, currentSession, now); err != nil {
			return err
		}
		return repository.appendAudit(ctx, securityAudit(claims.PrincipalID, "password_change", "success"))
	})
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	return nil
}

func (s *Service) CreateSession(
	ctx context.Context,
	principalID guid.ID,
	now time.Time,
	assurance authnext.Assurance,
) (string, error) {
	if !s.Enabled() || s.repo == nil {
		return "", authnext.ErrDisabled
	}
	return s.insertSession(ctx, s.repo, principalID, now, assurance)
}

// CreateSessionOn shares federation's principal/binding lock and transaction.
func (s *Service) CreateSessionOn(ctx context.Context, tx *gorm.DB, principalID guid.ID, now time.Time, assurance authnext.Assurance) (string, error) {
	if !s.Enabled() || s.repo == nil || tx == nil {
		return "", authnext.ErrUnavailable
	}
	return s.insertSession(ctx, s.repo.withDB(tx, true), principalID, now, assurance)
}

func (s *Service) insertSession(
	ctx context.Context,
	repository *Repository,
	principalID guid.ID,
	now time.Time,
	assurance authnext.Assurance,
) (string, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return "", authnext.ErrInvalidCredentials
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	authTime := assurance.AuthTime.UTC()
	if authTime.IsZero() {
		authTime = now.UTC()
	}
	row := model.Session{
		IDHash: tokenHash(token), PrincipalID: principalID, CreatedAt: now.UTC(), AuthTime: authTime,
		ACR: assurance.Level, AMR: strings.Join(assurance.Methods, " "), LastSeenAt: now.UTC(),
		ExpiresAt: now.Add(s.web.IdleTTL).UTC(), AbsoluteExpiresAt: now.Add(s.web.SessionTTL).UTC(),
	}
	if err := repository.createSession(ctx, &row); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

func currentSessionHash(cookieHeader, name string) string {
	token, err := cookieValue(cookieHeader, name)
	if err != nil {
		return ""
	}
	return tokenHash(token)
}

func (s *Service) createLoginChallenge(
	ctx context.Context,
	principalID guid.ID,
	returnURL string,
	now time.Time,
) (authnext.LoginResult, error) {
	token, err := randomToken(32)
	if err != nil {
		return authnext.LoginResult{}, err
	}
	expiresAt := now.Add(s.cfg.MFA.ChallengeTTL).UTC()
	if err := s.repo.createMFAChallenge(ctx, &model.MFAChallenge{
		IDHash: tokenHash(token), PrincipalID: principalID, ReturnURL: returnURL,
		CreatedAt: now.UTC(), ExpiresAt: expiresAt,
	}, now.UTC()); err != nil {
		return authnext.LoginResult{}, fmt.Errorf("create MFA challenge: %w", err)
	}
	return authnext.LoginResult{
		Status: "mfa_required", ReturnURL: returnURL, ChallengeID: token,
		ExpiresAt: &expiresAt, Methods: []string{"totp", "recovery_code"},
	}, nil
}

func (s *Service) IssueAccessToken(
	ctx context.Context,
	principalID guid.ID,
	audience, scope string,
) (string, int64, error) {
	return s.IssueTenantAccessToken(ctx, principalID, "", audience, scope)
}

func (s *Service) IssueTenantAccessToken(
	ctx context.Context,
	principalID guid.ID,
	tenantID, audience, scope string,
) (string, int64, error) {
	return s.IssueTenantAccessTokenWithAssurance(ctx, principalID, tenantID, audience, scope, authnext.Assurance{
		AuthTime: s.now().UTC(), Level: 1, Methods: []string{"pwd"},
	})
}

func (s *Service) IssueTenantAccessTokenWithAssurance(
	ctx context.Context,
	principalID guid.ID,
	tenantID, audience, scope string,
	assurance authnext.Assurance,
) (string, int64, error) {
	claims, err := s.claims(ctx, principalID)
	if err != nil {
		return "", 0, err
	}
	return s.issueSubjectToken(
		ctx, authnext.SubjectTypePrincipal, claims.PrincipalID.String(), tenantID, audience, scope,
		claims.CredentialVersion, claims.PreferredUsername, claims.Email, claims.EmailVerified, assurance,
	)
}

func (s *Service) IssueSubjectToken(
	subject, audience, scope, username, email string,
) (string, int64, error) {
	return s.issueSubjectToken(
		context.Background(), authnext.SubjectTypeService, subject, "", audience, scope,
		0, username, email, false, authnext.Assurance{},
	)
}

func (s *Service) IssueTenantSubjectToken(
	ctx context.Context,
	subject, tenantID, audience, scope, username, email string,
) (string, int64, error) {
	return s.issueSubjectToken(
		ctx, authnext.SubjectTypeService, subject, tenantID, audience, scope,
		0, username, email, false, authnext.Assurance{},
	)
}

func (s *Service) IssueTenantServiceAccountToken(
	ctx context.Context,
	subject, tenantID, audience, scope, username string,
	tokenVersion int64,
) (string, int64, error) {
	return s.issueSubjectToken(
		ctx, authnext.SubjectTypeServiceAccount, subject, tenantID, audience, scope,
		tokenVersion, username, "", false, authnext.Assurance{},
	)
}

func (s *Service) IssueTenantOAuthClientToken(
	ctx context.Context,
	clientID, tenantID, audience, scope, name string,
) (string, int64, error) {
	if err := s.verifyOAuthClientState(ctx, clientID, tenantID); err != nil {
		return "", 0, err
	}
	return s.issueSubjectToken(
		ctx, authnext.SubjectTypeOAuthClient, "client:"+clientID, tenantID, audience, scope,
		0, name, "", false, authnext.Assurance{ClientID: clientID},
	)
}

func (s *Service) issueSubjectToken(
	ctx context.Context,
	subjectType, subject, tenantID, audience, scope string,
	credentialVersion int64,
	username, email string,
	emailVerified bool,
	assurance authnext.Assurance,
) (string, int64, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return "", 0, authnext.ErrInvalidCredentials
	}
	if s == nil || !s.cfg.Enabled || !s.signingKeys.usable() {
		return "", 0, authnext.ErrDisabled
	}
	if subject == "" {
		return "", 0, authnext.ErrInvalidToken
	}
	if audience == "" {
		audience = s.cfg.Audience[0]
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.AccessTokenTTL)
	jti, err := randomToken(18)
	if err != nil {
		return "", 0, err
	}
	payload := map[string]any{
		"iss": s.cfg.Issuer, "sub": subject, "aud": []string{audience},
		"iat": now.Unix(), "exp": expiresAt.Unix(), "jti": jti,
		"subject_type": subjectType, "scope": scope,
	}
	if tenantID != "" {
		payload["organization_id"] = tenantID
	}
	if subjectType == authnext.SubjectTypePrincipal || subjectType == authnext.SubjectTypeServiceAccount {
		payload["credential_version"] = credentialVersion
		if principalID, parseErr := guid.Parse(subject); parseErr == nil {
			payload["principal_id"] = principalID
		}
	}
	if subjectType == authnext.SubjectTypePrincipal {
		principalID, parseErr := guid.Parse(subject)
		if parseErr != nil || principalID <= 0 {
			return "", 0, authnext.ErrInvalidToken
		}
		if err := s.tokenProfile(ctx, payload, principalID, tenantID, scope, username, email, emailVerified); err != nil {
			return "", 0, err
		}
	} else {
		payload["preferred_username"], payload["email"] = username, email
	}
	if !assurance.AuthTime.IsZero() {
		payload["auth_time"] = assurance.AuthTime.UTC().Unix()
	}
	if assurance.Level > 0 {
		payload["acr"] = assurance.Level
	}
	if len(assurance.Methods) > 0 {
		payload["amr"] = assurance.Methods
	}
	if assurance.ClientID != "" {
		payload["client_id"] = assurance.ClientID
	}
	if assurance.OAuthGrantID != "" {
		payload["oauth_grant_id"] = assurance.OAuthGrantID
	}
	if assurance.NetworkZone != "" {
		payload["network_zone"] = assurance.NetworkZone
	}
	if err := s.enrich(ctx, payload, ClaimContext{
		TokenType: "access_token", Subject: subject, TenantID: tenantID,
		Audience: audience, Scope: scope,
	}); err != nil {
		return "", 0, err
	}
	token, err := s.sign(payload)
	return token, int64(s.cfg.AccessTokenTTL.Seconds()), err
}

func (s *Service) IssueIDToken(
	ctx context.Context,
	principalID guid.ID,
	clientID, nonce string,
) (string, error) {
	return s.IssueTenantIDToken(ctx, principalID, "", clientID, nonce)
}

func (s *Service) IssueTenantIDToken(
	ctx context.Context,
	principalID guid.ID,
	tenantID, clientID, nonce string,
) (string, error) {
	return s.IssueTenantIDTokenWithAssurance(ctx, principalID, tenantID, clientID, nonce, "openid", authnext.Assurance{
		AuthTime: s.now().UTC(), Level: 1, Methods: []string{"pwd"}, ClientID: clientID,
	})
}

func (s *Service) IssueTenantIDTokenWithAssurance(
	ctx context.Context,
	principalID guid.ID,
	tenantID, clientID, nonce, scope string,
	assurance authnext.Assurance,
) (string, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return "", authnext.ErrInvalidCredentials
	}
	if s == nil || !s.cfg.Enabled || !s.signingKeys.usable() {
		return "", authnext.ErrDisabled
	}
	claims, err := s.claims(ctx, principalID)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	jti, err := randomToken(18)
	if err != nil {
		return "", err
	}
	authTime := int64(0)
	if !assurance.AuthTime.IsZero() {
		authTime = assurance.AuthTime.UTC().Unix()
	}
	payload := map[string]any{
		"iss": s.cfg.Issuer, "sub": claims.PrincipalID.String(), "aud": []string{clientID},
		"iat": now.Unix(), "exp": now.Add(s.cfg.AccessTokenTTL).Unix(), "jti": jti,
		"subject_type":       authnext.SubjectTypePrincipal,
		"credential_version": claims.CredentialVersion, "principal_id": claims.PrincipalID,
		"auth_time": authTime,
	}
	if err := s.tokenProfile(ctx, payload, principalID, tenantID, scope, claims.PreferredUsername, claims.Email, claims.EmailVerified); err != nil {
		return "", err
	}
	if nonce != "" {
		payload["nonce"] = nonce
	}
	if tenantID != "" {
		payload["organization_id"] = tenantID
	}
	if assurance.Level > 0 {
		payload["acr"] = assurance.Level
	}
	if len(assurance.Methods) > 0 {
		payload["amr"] = assurance.Methods
	}
	if err := s.enrich(ctx, payload, ClaimContext{
		TokenType: "id_token", Subject: claims.PrincipalID.String(), TenantID: tenantID, Audience: clientID, Scope: scope,
	}); err != nil {
		return "", err
	}
	return s.sign(payload)
}

func (s *Service) enrich(ctx context.Context, payload map[string]any, claimContext ClaimContext) error {
	if s.enricher == nil {
		return nil
	}
	claims, err := s.enricher.Enrich(ctx, claimContext)
	if err != nil {
		return fmt.Errorf("enrich token claims: %w", err)
	}
	if len(claims) > 0 {
		payload["ext"] = claims
	}
	return nil
}

// JWKS publishes every key a caller may still meet in a token, not just the one
// signing now. A relying party that fetched this document has to be able to
// verify tokens issued before the current key existed, and to already know the
// next key before anything is signed with it.
func (s *Service) JWKS() map[string]any {
	if s == nil {
		return map[string]any{"keys": []map[string]any{}}
	}
	return s.signingKeys.jwks()
}

func (s *Service) sign(payload map[string]any) (string, error) {
	if s == nil || !s.signingKeys.usable() {
		return "", authnext.ErrDisabled
	}
	// Only the active key ever signs. A retired key is kept to read what it
	// already sealed, never to seal anything more.
	header, err := json.Marshal(
		map[string]string{"alg": "EdDSA", "kid": s.signingKeys.activeID(), "typ": "JWT"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	signature := ed25519.Sign(s.signingKeys.private, []byte(signed))
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Service) verifyAccessToken(authorization string) (*authnext.Claims, error) {
	if s == nil || !s.signingKeys.usable() {
		return nil, authnext.ErrInvalidToken
	}
	parts := strings.Fields(authorization)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, authnext.ErrInvalidBearer
	}
	token, err := jwsx.Parse(parts[1])
	if err != nil || !accessTokenAlgorithms.Allows(token.Header.Alg) {
		return nil, authnext.ErrInvalidToken
	}
	key, known := s.signingKeys.publicKey(token.Header.Kid)
	if !known || accessTokenAlgorithms.Verify(token.Header.Alg, key, token.SigningInput, token.Signature) != nil {
		return nil, authnext.ErrInvalidToken
	}
	payload := token.Payload
	var raw struct {
		Issuer            string   `json:"iss"`
		Subject           string   `json:"sub"`
		SubjectType       string   `json:"subject_type"`
		Audience          []string `json:"aud"`
		ExpiresAt         int64    `json:"exp"`
		IssuedAt          int64    `json:"iat"`
		PreferredUsername string   `json:"preferred_username"`
		Email             string   `json:"email"`
		EmailVerified     bool     `json:"email_verified"`
		OrganizationID    string   `json:"organization_id"`
		PrincipalID       guid.ID  `json:"principal_id"`
		CredentialVersion int64    `json:"credential_version"`
		AuthTime          int64    `json:"auth_time"`
		ACR               int      `json:"acr"`
		AMR               []string `json:"amr"`
		ClientID          string   `json:"client_id"`
		OAuthGrantID      string   `json:"oauth_grant_id"`
		NetworkZone       string   `json:"network_zone"`
	}
	if json.Unmarshal(payload, &raw) != nil || raw.Issuer != s.cfg.Issuer || raw.Subject == "" ||
		!validSubjectType(raw.SubjectType) || s.now().UTC().After(time.Unix(raw.ExpiresAt, 0).Add(s.cfg.ClockSkew)) {
		return nil, authnext.ErrInvalidToken
	}
	if len(s.cfg.Audience) > 0 && !hasAudience(raw.Audience, s.cfg.Audience) {
		return nil, authnext.ErrInvalidAud
	}
	var rawClaims map[string]any
	if err := json.Unmarshal(payload, &rawClaims); err != nil {
		return nil, authnext.ErrInvalidToken
	}
	return &authnext.Claims{
		Raw:    rawClaims,
		Issuer: raw.Issuer, Subject: raw.Subject, SubjectType: raw.SubjectType, Audience: raw.Audience,
		ExpiresAt: time.Unix(raw.ExpiresAt, 0).UTC(), IssuedAt: time.Unix(raw.IssuedAt, 0).UTC(),
		AuthTime: time.Unix(raw.AuthTime, 0).UTC(), PreferredUsername: raw.PreferredUsername,
		Email: raw.Email, EmailVerified: raw.EmailVerified, OrganizationID: raw.OrganizationID,
		PrincipalID: raw.PrincipalID, CredentialVersion: raw.CredentialVersion,
		ACR: raw.ACR, AMR: raw.AMR, ClientID: raw.ClientID, NetworkZone: raw.NetworkZone,
		OAuthGrantID: raw.OAuthGrantID,
	}, nil
}

// accessTokenAlgorithms is what this service signs its own access tokens
// with; every other algorithm, "none" and HMAC included, is refused before a
// key is looked up.
var accessTokenAlgorithms = jwsx.Algorithms{jwsx.EdDSA}

func validSubjectType(subjectType string) bool {
	return subjectType == authnext.SubjectTypePrincipal || subjectType == authnext.SubjectTypeOAuthClient ||
		subjectType == authnext.SubjectTypeService || subjectType == authnext.SubjectTypeServiceAccount
}

func (s *Service) verifySubjectState(ctx context.Context, claims *authnext.Claims) error {
	if s == nil || s.repo == nil || claims == nil {
		return authnext.ErrInvalidToken
	}
	switch claims.SubjectType {
	case authnext.SubjectTypePrincipal:
		state, err := s.repo.principalState(ctx, claims.PrincipalID)
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil &&
			(state.Status != "active" || state.CredentialVersion != claims.CredentialVersion) {
			return authnext.ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("%w: verify principal status: %w", authnext.ErrUnavailable, err)
		}
		if claims.ClientID != "" {
			if claims.OAuthGrantID == "" || s.oauthGrants == nil {
				return authnext.ErrInvalidToken
			}
			return s.oauthGrants(ctx, claims)
		}
		return nil
	case authnext.SubjectTypeOAuthClient:
		clientID, ok := strings.CutPrefix(claims.Subject, "client:")
		if !ok || clientID == "" {
			return authnext.ErrInvalidToken
		}
		return s.verifyOAuthClientState(ctx, clientID, claims.OrganizationID)
	case authnext.SubjectTypeServiceAccount:
		state, err := s.repo.serviceAccountState(ctx, claims.PrincipalID)
		expired := state.ExpiresAt != nil && !state.ExpiresAt.After(s.now().UTC())
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil &&
			(claims.OrganizationID == "" || state.TenantID.String() != claims.OrganizationID ||
				state.AccountStatus != "active" || state.PrincipalStatus != "active" ||
				state.MemberStatus != "active" || state.TenantStatus != "active" || expired ||
				state.TokenVersion != claims.CredentialVersion) {
			return authnext.ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("%w: verify service account status: %w", authnext.ErrUnavailable, err)
		}
		return nil
	case authnext.SubjectTypeService:
		return nil
	default:
		return authnext.ErrInvalidToken
	}
}

func (s *Service) verifyOAuthClientState(ctx context.Context, clientID, tenantID string) error {
	parsedClientID, err := guid.Parse(clientID)
	if err != nil {
		return authnext.ErrInvalidToken
	}
	client, err := s.repo.oauthClientState(ctx, parsedClientID)
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil &&
		(client.Status != "active" || client.TenantID.String() != tenantID) {
		return authnext.ErrInvalidToken
	}
	if err != nil {
		return fmt.Errorf("%w: verify OAuth client status: %w", authnext.ErrUnavailable, err)
	}
	return nil
}

func (s *Service) claims(ctx context.Context, principalID guid.ID) (*authnext.Claims, error) {
	principal, credentialVersion, err := s.repo.activePrincipalClaims(ctx, principalID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, fmt.Errorf("%w: load principal: %w", authnext.ErrUnavailable, err)
	}
	return &authnext.Claims{
		Issuer: s.cfg.Issuer, Subject: principal.ID.String(), SubjectType: authnext.SubjectTypePrincipal,
		PrincipalID: principal.ID, PreferredUsername: principal.LoginName,
		Email: principal.Email, EmailVerified: principal.EmailVerified,
		CredentialVersion: credentialVersion,
	}, nil
}

const authnAuditTenant = guid.ID(0)

func (s *Service) audit(ctx context.Context, principalID guid.ID, eventType, outcome string) {
	if s == nil || s.repo == nil {
		return
	}
	_ = s.repo.appendAudit(ctx, securityAudit(principalID, eventType, outcome))
}

func securityAudit(principalID guid.ID, eventType, outcome string) audit.DomainEvent {
	return audit.DomainEvent{
		TenantID: authnAuditTenant, PrincipalID: principalID,
		EventType: eventType, TargetType: "principal", TargetID: principalID,
		Detail: map[string]any{"outcome": outcome},
	}
}

// mfaKeyDomain separates this module's derived subkeys from every other
// module's. It is the literal prefix the derivation already used before the key
// ring existed, so every stored TOTP secret, recovery code, and outstanding
// token keeps working across the change.
const mfaKeyDomain = "platform:mfa:key:"

// resolveMFAKeyring loads the MFA keys so startup fails fast instead of
// surfacing a runtime secret error. The first key seals and derives; every key
// opens and is recognized, which is what lets a deployment replace one without
// invalidating the recovery codes its users are holding.
func resolveMFAKeyring(cfg authnext.MFAConfig) (*keyringx.Keyring, error) {
	active, err := secretx.Resolve("authn.mfa.encryption_key", cfg.EncryptionKey, cfg.EncryptionKeyFile, 4096)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(active) == "" {
		return nil, errors.New("authn MFA encryption key is required when browser authentication is enabled")
	}
	previous, err := secretx.ResolveList(
		"authn.mfa.previous_encryption_keys",
		cfg.PreviousEncryptionKeys, cfg.PreviousEncryptionKeyFiles, 4096,
	)
	if err != nil {
		return nil, err
	}
	keys, err := keyringx.New(mfaKeyDomain, active, previous...)
	if err != nil {
		return nil, fmt.Errorf("authn MFA encryption key: %w", err)
	}
	return keys, nil
}

func parseSigningKey(encoded string) (ed25519.PrivateKey, error) {
	encoded = strings.TrimSpace(encoded)
	var raw []byte
	var err error
	for _, decoder := range []*base64.Encoding{
		base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding,
	} {
		raw, err = decoder.DecodeString(encoded)
		if err == nil {
			break
		}
	}
	if err != nil {
		raw = []byte(encoded)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, errors.New("authn signing key must be a 32-byte Ed25519 seed")
	}
}

func normalizeLogin(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func tokenHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cookieValue(header, name string) (string, error) {
	request := &http.Request{Header: http.Header{"Cookie": []string{header}}}
	cookie, err := request.Cookie(name)
	if err != nil || cookie.Value == "" {
		return "", http.ErrNoCookie
	}
	return cookie.Value, nil
}

func hasAudience(got, accepted []string) bool {
	for _, candidate := range got {
		if slices.Contains(accepted, candidate) {
			return true
		}
	}
	return false
}

var (
	dummyHashOnce sync.Once
	dummyHash     string
)

func verifyDummy(password string) {
	dummyHashOnce.Do(func() {
		dummyHash, _ = passwordx.Hash("not-a-real-password-for-timing-equalization")
	})
	_, _ = passwordx.Verify(dummyHash, password)
}
