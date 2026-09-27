package authn

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const accountViewMaxTTL = 30 * time.Minute

// Personal reads may reuse only the server-verified target, never the operator cookie.
func (s *Service) personalReadClaims(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		view := claims.AccountView
		if !view.Valid() || claims.PrincipalID <= 0 || view.ScopeType != "personal" || view.ScopeID != claims.PrincipalID {
			return nil, authnext.ErrInvalidToken
		}
		return claims, nil
	}
	return s.Authenticate(ctx, authorization, cookie)
}

type AccountViewAuthorizer = authnext.AccountViewAuthorizer

func (s *Service) authenticateAccountHolder(ctx context.Context, authorization, cookie string) (*authnext.Claims, error) {
	if scheme, raw, ok := strings.Cut(strings.TrimSpace(authorization), " "); ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(raw, "avt_") {
		return nil, authnext.ErrInvalidCredentials
	}
	claims, err := s.Authenticate(ctx, authorization, cookie)
	if err == nil && claims != nil && claims.AccountView != nil {
		return nil, authnext.ErrInvalidCredentials
	}
	return claims, err
}

// AccountViewAuthorizerConnector lets the authority owner bind live admission.
type AccountViewAuthorizerConnector = authnext.AccountViewAuthorizerConnector

func WithAccountViewAuthorizer(authorizer AccountViewAuthorizer) WebOption {
	return func(service *Service) { service.accountViewAuthorizer = authorizer }
}

type AccountViewRequest struct {
	PrincipalID guid.ID   `json:"principal_id"`
	ScopeType   string    `json:"scope_type"`
	ScopeID     guid.ID   `json:"scope_id"`
	Mode        string    `json:"mode,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type AccountViewCreated struct {
	ID        guid.ID   `json:"id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Mode      string    `json:"mode"`
}

func (s *Service) accountViewReady(ctx context.Context) error {
	if s == nil || !s.Enabled() || s.repo == nil || s.accountViewAuthorizer == nil {
		return authnext.ErrUnavailable
	}
	if claims, ok := authnext.FromContext(ctx); ok && claims != nil && claims.AccountView != nil {
		return authnext.ErrInvalidCredentials
	}
	return nil
}

func normalizeAccountView(in AccountViewRequest, now time.Time) (AccountViewRequest, error) {
	if in.Mode == "" {
		in.Mode = "read_only"
	}
	if in.PrincipalID <= 0 || in.ScopeID <= 0 ||
		!slices.Contains([]string{"system", "platform", "tenant", "entity", "personal"}, in.ScopeType) ||
		!slices.Contains([]string{"read_only", "read_write"}, in.Mode) ||
		(in.ScopeType == "personal" && in.ScopeID != in.PrincipalID) {
		return in, authnext.ErrInvalidToken
	}
	if in.ExpiresAt.IsZero() {
		in.ExpiresAt = now.Add(accountViewMaxTTL)
	}
	in.ExpiresAt = in.ExpiresAt.UTC().Truncate(time.Millisecond)
	if !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(accountViewMaxTTL)) {
		return in, authnext.ErrInvalidToken
	}
	return in, nil
}

// Current reads prevent an earlier MySQL snapshot from bypassing revocation.
func accountViewCurrentRepository(repo *Repository) *Repository {
	db := repo.writer()
	if db.Dialector.Name() != "sqlite" {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"}).Session(&gorm.Session{})
	}
	return repo.withDB(db, true)
}

func lockAccountViewPrincipals(ctx context.Context, repo *Repository, actor, target guid.ID) (map[guid.ID]*identity.Principal, error) {
	ids := []guid.ID{actor, target}
	slices.Sort(ids)
	principals := make(map[guid.ID]*identity.Principal, 2)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return nil, authnext.ErrInvalidToken
		}
		principal, err := repo.lockPrincipal(ctx, id)
		if err != nil {
			return nil, err
		}
		if principal.ActivationRequired {
			return nil, authnext.ErrInvalidToken
		}
		principals[id] = principal
	}
	return principals, nil
}

func accountViewCredentialVersion(ctx context.Context, repo *Repository, id guid.ID) (int64, error) {
	credential, err := repo.getCredential(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return credential.CredentialVersion, nil
}

func accountViewActorContext(ctx context.Context, principal *identity.Principal, session *model.Session, version int64) context.Context {
	ctx = policyx.WithTrustedContext(ctx, policyx.TrustedContext{ACR: session.ACR, AMR: strings.Fields(session.AMR)})
	return authnext.WithClaims(ctx, &authnext.Claims{
		Subject: principal.ID.String(), SubjectType: authnext.SubjectTypePrincipal, PrincipalID: principal.ID,
		CredentialVersion: version, ACR: session.ACR, AMR: strings.Fields(session.AMR), AuthTime: session.AuthTime,
	})
}

func accountViewSession(ctx context.Context, repo *Repository, hash string, actor guid.ID, now time.Time) (*model.Session, error) {
	session, err := repo.activeSession(ctx, hash, now)
	if err != nil {
		return nil, err
	}
	if session.PrincipalID != actor || session.ACR < 1 || session.CreatedAt.After(now) {
		return nil, authnext.ErrInvalidToken
	}
	return session, nil
}

func accountViewServiceError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.ErrInvalidToken
	}
	if errors.Is(err, authnext.ErrInvalidToken) || errors.Is(err, authnext.ErrInvalidCredentials) || errors.Is(err, authnext.ErrUnavailable) {
		return err
	}
	return fmt.Errorf("%w: account view: %w", authnext.ErrUnavailable, err)
}

func (s *Service) CreateAccountView(ctx context.Context, operatorCookie string, input AccountViewRequest) (AccountViewCreated, error) {
	if err := s.accountViewReady(ctx); err != nil {
		return AccountViewCreated{}, err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	input, err := normalizeAccountView(input, now)
	if err != nil {
		return AccountViewCreated{}, err
	}
	secret, err := cookieValue(operatorCookie, s.web.CookieName)
	if err != nil {
		return AccountViewCreated{}, authnext.ErrInvalidToken
	}
	sessionHash := tokenHash(secret)
	candidate, err := s.repo.activeSession(ctx, sessionHash, now)
	if err != nil {
		return AccountViewCreated{}, accountViewServiceError(err)
	}
	var result AccountViewCreated
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		principals, err := lockAccountViewPrincipals(ctx, repo, candidate.PrincipalID, input.PrincipalID)
		if err != nil {
			return err
		}
		repo = accountViewCurrentRepository(repo)
		actorVersion, err := accountViewCredentialVersion(ctx, repo, candidate.PrincipalID)
		if err != nil {
			return err
		}
		targetVersion, err := accountViewCredentialVersion(ctx, repo, input.PrincipalID)
		if err != nil {
			return err
		}
		session, err := accountViewSession(ctx, repo, sessionHash, candidate.PrincipalID, now)
		if err != nil {
			return err
		}
		actorContext := accountViewActorContext(ctx, principals[candidate.PrincipalID], session, actorVersion)
		if err := s.accountViewAuthorizer(actorContext, repo.writer(), candidate.PrincipalID, input.PrincipalID, input.ScopeType, input.ScopeID); err != nil {
			return err
		}
		expiresAt := input.ExpiresAt
		for _, boundary := range []time.Time{session.ExpiresAt, session.AbsoluteExpiresAt} {
			if boundary.Before(expiresAt) {
				expiresAt = boundary
			}
		}
		random, err := randomToken(32)
		if err != nil {
			return err
		}
		id, err := repo.nextID()
		if err != nil {
			return err
		}
		raw := "avt_" + random
		row := model.AccountViewToken{ID: id, TokenHash: tokenHash(raw), ActorID: candidate.PrincipalID, PrincipalID: input.PrincipalID,
			ActorSessionHash: sessionHash, ActorCredentialVersion: actorVersion, TargetCredentialVersion: targetVersion,
			ScopeType: input.ScopeType, ScopeID: input.ScopeID, Mode: input.Mode, CreatedAt: now, ExpiresAt: expiresAt}
		if err := repo.createAccountViewToken(ctx, &row); err != nil {
			return err
		}
		if err := repo.appendAudit(ctx, accountViewAudit(ctx, &row, "account_view_created")); err != nil {
			return err
		}
		result = AccountViewCreated{ID: id, Token: raw, ExpiresAt: expiresAt, Mode: input.Mode}
		return nil
	})
	if err != nil {
		return AccountViewCreated{}, accountViewServiceError(err)
	}
	return result, nil
}

func accountViewAudit(ctx context.Context, row *model.AccountViewToken, eventType string) audit.DomainEvent {
	claims := &authnext.Claims{PrincipalID: row.PrincipalID, AccountView: &authnext.AccountViewIdentity{
		ID: row.ID, PrincipalID: row.ActorID, ReadOnly: row.Mode == "read_only", ScopeType: row.ScopeType, ScopeID: row.ScopeID}}
	event := audit.NewDomainEvent(authnext.WithClaims(ctx, claims), 0, eventType, "account_view", row.ID)
	event.Detail["scope_type"], event.Detail["scope_id"] = row.ScopeType, row.ScopeID.String()
	return event
}

func (s *Service) AuthenticateAccountView(ctx context.Context, raw string) (*authnext.Claims, error) {
	if err := s.accountViewReady(ctx); err != nil {
		return nil, err
	}
	encoded, found := strings.CutPrefix(raw, "avt_")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if !found || err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, authnext.ErrInvalidToken
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	candidate, err := s.repo.activeAccountViewToken(ctx, tokenHash(raw), now)
	if err != nil {
		return nil, accountViewServiceError(err)
	}
	var claims *authnext.Claims
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		principals, err := lockAccountViewPrincipals(ctx, repo, candidate.ActorID, candidate.PrincipalID)
		if err != nil {
			return err
		}
		repo = accountViewCurrentRepository(repo)
		row, err := repo.activeAccountViewToken(ctx, tokenHash(raw), now)
		if err != nil {
			return err
		}
		if row.ID <= 0 || (row.Mode != "read_only" && row.Mode != "read_write") {
			return authnext.ErrInvalidToken
		}
		_, err = normalizeAccountView(AccountViewRequest{PrincipalID: row.PrincipalID, ScopeType: row.ScopeType, ScopeID: row.ScopeID, Mode: row.Mode, ExpiresAt: row.ExpiresAt}, row.CreatedAt)
		if err != nil {
			return err
		}
		actorVersion, err := accountViewCredentialVersion(ctx, repo, row.ActorID)
		if err != nil {
			return err
		}
		targetVersion, err := accountViewCredentialVersion(ctx, repo, row.PrincipalID)
		if err != nil {
			return err
		}
		if actorVersion != row.ActorCredentialVersion || targetVersion != row.TargetCredentialVersion {
			return authnext.ErrInvalidToken
		}
		session, err := accountViewSession(ctx, repo, row.ActorSessionHash, row.ActorID, now)
		if err != nil {
			return err
		}
		actorContext := accountViewActorContext(ctx, principals[row.ActorID], session, actorVersion)
		if err := s.accountViewAuthorizer(actorContext, repo.writer(), row.ActorID, row.PrincipalID, row.ScopeType, row.ScopeID); err != nil {
			return err
		}
		if _, err := repo.touchAccountViewToken(ctx, row.ID, now); err != nil {
			return err
		}
		if err := repo.appendAudit(ctx, accountViewAudit(ctx, row, "account_view_used")); err != nil {
			return err
		}
		target := principals[row.PrincipalID]
		claims = &authnext.Claims{Issuer: s.cfg.Issuer, Subject: target.ID.String(), SubjectType: authnext.SubjectTypePrincipal,
			PrincipalID: target.ID, PreferredUsername: target.LoginName, Email: target.Email, EmailVerified: target.EmailVerified,
			CredentialVersion: targetVersion, IssuedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
			AccountView: &authnext.AccountViewIdentity{ID: row.ID, PrincipalID: row.ActorID, ReadOnly: row.Mode == "read_only", ScopeType: row.ScopeType, ScopeID: row.ScopeID}}
		return nil
	})
	if err != nil {
		return nil, accountViewServiceError(err)
	}
	return claims, nil
}

func (s *Service) RevokeAccountView(ctx context.Context, operatorCookie string, id guid.ID) error {
	if err := s.accountViewReady(ctx); err != nil {
		return err
	}
	secret, err := cookieValue(operatorCookie, s.web.CookieName)
	if err != nil || id <= 0 {
		return authnext.ErrInvalidToken
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	actorSession, err := s.repo.activeSession(ctx, tokenHash(secret), now)
	if err != nil {
		return accountViewServiceError(err)
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := repo.lockPrincipal(ctx, actorSession.PrincipalID); err != nil {
			return err
		}
		repo = accountViewCurrentRepository(repo)
		if _, err := accountViewSession(ctx, repo, tokenHash(secret), actorSession.PrincipalID, now); err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		row, err := tables.AccountViewTokens.WithContext(ctx).Where(tables.AccountViewTokens.ID.Eq(id), tables.AccountViewTokens.ActorID.Eq(actorSession.PrincipalID)).Take()
		if err != nil {
			return err
		}
		changed, err := repo.revokeAccountViewToken(ctx, id, actorSession.PrincipalID, now)
		if err != nil || !changed {
			return err
		}
		return repo.appendAudit(ctx, accountViewAudit(ctx, row, "account_view_revoked"))
	})
	return accountViewServiceError(err)
}
