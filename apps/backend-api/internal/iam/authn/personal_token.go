package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

var errPersonalTokenInput = errors.New("invalid personal token parameters")

// PersonalTokenRequest defines one tenant and a finite set of interface permissions.
// Existing membership, object, field and data authorization remains authoritative.
type PersonalTokenRequest struct {
	Name        string    `json:"name" minLength:"1" maxLength:"128"`
	TenantID    guid.ID   `json:"tenant_id"`
	Permissions []string  `json:"permissions" minItems:"1" maxItems:"128"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// PersonalTokenView deliberately excludes the secret and its digest.
type PersonalTokenView struct {
	ID          guid.ID    `json:"id"`
	Name        string     `json:"name"`
	TenantID    guid.ID    `json:"tenant_id"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

type PersonalTokenPreparation struct {
	Request PersonalTokenRequest `json:"request"`
	Target  string               `json:"target"`
}

type PersonalTokenCreated struct {
	PersonalTokenView
	Token string `json:"token" doc:"Shown only once; store securely. A lost response requires revoking this token and creating another."`
}

// PersonalTokenPermissions returns registered permissions currently granted to
// this principal in one selected tenant, without requiring role administration.
func (s *Service) PersonalTokenPermissions(ctx context.Context, cookie string, tenantID guid.ID) ([]authz.Action, error) {
	claims, err := s.Authenticate(ctx, "", cookie)
	if err != nil {
		return nil, err
	}
	if tenantID <= 0 {
		return nil, errPersonalTokenInput
	}
	if s.personalRegistry == nil || s.personalPermissions == nil || s.memberTenant == nil {
		return nil, authnext.ErrUnavailable
	}
	resolved, err := s.memberTenant(ctx, claims.PrincipalID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: membership: %w", authnext.ErrUnavailable, err)
	}
	if resolved != tenantID {
		return nil, authnext.ErrInvalidCredentials
	}
	actions := s.personalRegistry.All()
	codes := make([]string, 0, len(actions))
	for _, action := range actions {
		if action.Scope != "platform" {
			codes = append(codes, action.Code)
		}
	}
	allowed, err := s.personalPermissions(ctx, tenantID, codes, claims.PrincipalID)
	if err != nil {
		return nil, fmt.Errorf("%w: permissions: %w", authnext.ErrUnavailable, err)
	}
	result := []authz.Action{}
	for _, action := range actions {
		if action.Scope != "platform" && allowed[action.Code] {
			result = append(result, action)
		}
	}
	return result, nil
}

func (s *Service) normalizePersonalToken(in PersonalTokenRequest) (PersonalTokenPreparation, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.ExpiresAt = in.ExpiresAt.UTC().Truncate(time.Millisecond)
	if in.Name == "" || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 128 || strings.ContainsRune(in.Name, 0) ||
		in.TenantID <= 0 || len(in.Permissions) == 0 || len(in.Permissions) > 128 ||
		!in.ExpiresAt.After(s.now().UTC()) || in.ExpiresAt.Year() > 9999 || in.ExpiresAt.Year() < 1000 {
		return PersonalTokenPreparation{}, errPersonalTokenInput
	}
	in.Permissions = slices.Clone(in.Permissions)
	slices.Sort(in.Permissions)
	in.Permissions = slices.Compact(in.Permissions)
	if s.personalRegistry == nil {
		return PersonalTokenPreparation{}, authnext.ErrUnavailable
	}
	for _, permission := range in.Permissions {
		action, ok := s.personalRegistry.Find(permission)
		if !ok || action.Scope == "platform" {
			return PersonalTokenPreparation{}, errPersonalTokenInput
		}
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return PersonalTokenPreparation{}, err
	}
	return PersonalTokenPreparation{Request: in, Target: tokenHash(string(encoded))}, nil
}

func (s *Service) checkPersonalTokenPermissions(ctx context.Context, principalID guid.ID, in PersonalTokenRequest) error {
	if s.personalPermissions == nil || s.memberTenant == nil {
		return authnext.ErrUnavailable
	}
	resolved, err := s.memberTenant(ctx, principalID, in.TenantID)
	if err != nil {
		return fmt.Errorf("%w: membership: %w", authnext.ErrUnavailable, err)
	}
	if resolved != in.TenantID {
		return authnext.ErrInvalidCredentials
	}
	allowed, err := s.personalPermissions(ctx, in.TenantID, in.Permissions, principalID)
	if err != nil {
		return fmt.Errorf("%w: permissions: %w", authnext.ErrUnavailable, err)
	}
	for _, permission := range in.Permissions {
		if !allowed[permission] {
			return authnext.ErrInvalidCredentials
		}
	}
	return nil
}

// PreparePersonalToken returns the canonical target to bind fresh verification.
func (s *Service) PreparePersonalToken(ctx context.Context, cookie string, in PersonalTokenRequest) (PersonalTokenPreparation, error) {
	claims, err := s.Authenticate(ctx, "", cookie)
	if err != nil {
		return PersonalTokenPreparation{}, err
	}
	prepared, err := s.normalizePersonalToken(in)
	if err != nil {
		return PersonalTokenPreparation{}, err
	}
	if err := s.checkPersonalTokenPermissions(ctx, claims.PrincipalID, prepared.Request); err != nil {
		return PersonalTokenPreparation{}, err
	}
	return prepared, nil
}

// CreatePersonalToken consumes proof and writes the digest and audit atomically.
func (s *Service) CreatePersonalToken(ctx context.Context, cookie, proof string, in PersonalTokenRequest) (PersonalTokenCreated, error) {
	claims, err := s.Authenticate(ctx, "", cookie)
	if err != nil {
		return PersonalTokenCreated{}, err
	}
	prepared, err := s.normalizePersonalToken(in)
	if err != nil {
		return PersonalTokenCreated{}, err
	}
	if err := s.checkPersonalTokenPermissions(ctx, claims.PrincipalID, prepared.Request); err != nil {
		return PersonalTokenCreated{}, err
	}
	secret, err := randomToken(32)
	if err != nil {
		return PersonalTokenCreated{}, err
	}
	secret = "pat_" + secret
	permissions, err := json.Marshal(prepared.Request.Permissions)
	if err != nil {
		return PersonalTokenCreated{}, err
	}
	row := model.PersonalToken{TokenHash: tokenHash(secret), PrincipalID: claims.PrincipalID,
		TenantID: prepared.Request.TenantID, Name: prepared.Request.Name, Permissions: string(permissions),
		CreatedAt: s.now().UTC().Truncate(time.Millisecond), ExpiresAt: prepared.Request.ExpiresAt}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := s.ConsumeReauthenticationOn(ctx, repo.writer(), claims.PrincipalID, cookie, proof, ReauthenticationPersonalTokenCreate, prepared.Target); err != nil {
			return err
		}
		_, row.CredentialVersion, err = repo.activePrincipalClaims(ctx, claims.PrincipalID)
		if err != nil {
			return err
		}
		row.ID, err = repo.nextID()
		if err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		if err := tables.PersonalTokens.WithContext(ctx).Create(&row); err != nil {
			return err
		}
		event := securityAudit(claims.PrincipalID, "personal_token_created", "success")
		event.TenantID, event.TargetType, event.TargetID = row.TenantID, "personal_token", row.ID
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return PersonalTokenCreated{}, err
	}
	return PersonalTokenCreated{PersonalTokenView: personalTokenView(&row), Token: secret}, nil
}

func personalTokenView(row *model.PersonalToken) PersonalTokenView {
	permissions := []string{}
	_ = json.Unmarshal([]byte(row.Permissions), &permissions)
	return PersonalTokenView{ID: row.ID, Name: row.Name, TenantID: row.TenantID, Permissions: permissions,
		CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt}
}

// ListPersonalTokens lists only the current browser principal's token metadata.
func (s *Service) ListPersonalTokens(ctx context.Context, cookie string, offset, limit int) ([]PersonalTokenView, int64, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, 0, errPersonalTokenInput
	}
	claims, err := s.personalReadClaims(ctx, "", cookie)
	if err != nil {
		return nil, 0, err
	}
	tables, _, err := s.repo.tables()
	if err != nil {
		return nil, 0, err
	}
	t := tables.PersonalTokens
	q := t.WithContext(ctx).Where(t.PrincipalID.Eq(claims.PrincipalID))
	rows, total, err := q.Order(t.ID.Desc()).FindByPage(offset, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]PersonalTokenView, 0, len(rows))
	for _, row := range rows {
		result = append(result, personalTokenView(row))
	}
	return result, total, nil
}

// RevokePersonalToken is idempotent and cannot reveal another principal's token.
func (s *Service) RevokePersonalToken(ctx context.Context, cookie string, id guid.ID) error {
	claims, err := s.Authenticate(ctx, "", cookie)
	if err != nil {
		return err
	}
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		if _, err := s.lockReauthenticationSession(ctx, repo, claims.PrincipalID, currentSessionHash(cookie, s.web.CookieName)); err != nil {
			return err
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		t := tables.PersonalTokens
		row, err := t.WithContext(ctx).Where(t.ID.Eq(id), t.PrincipalID.Eq(claims.PrincipalID)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.RevokedAt != nil {
			return nil
		}
		if _, err := t.WithContext(ctx).Where(t.ID.Eq(id), t.PrincipalID.Eq(claims.PrincipalID), t.RevokedAt.IsNull()).Update(t.RevokedAt, s.now().UTC().Truncate(time.Millisecond)); err != nil {
			return err
		}
		event := securityAudit(claims.PrincipalID, "personal_token_revoked", "success")
		event.TenantID, event.TargetType, event.TargetID = row.TenantID, "personal_token", row.ID
		return repo.appendAudit(ctx, event)
	})
}

func (s *Service) authenticatePersonalToken(ctx context.Context, secret string) (*authnext.Claims, error) {
	scope, ok := authnext.PersonalTokenScopeFromContext(ctx)
	if !ok || !strings.HasPrefix(secret, "pat_") || len(secret) > 128 {
		return nil, authnext.ErrInvalidToken
	}
	tables, _, err := s.repo.tables()
	if err != nil {
		return nil, fmt.Errorf("%w: personal token store: %w", authnext.ErrUnavailable, err)
	}
	t := tables.PersonalTokens
	row, err := t.WithContext(ctx).Where(t.TokenHash.Eq(tokenHash(secret)), t.TenantID.Eq(scope.TenantID), t.RevokedAt.IsNull(), t.ExpiresAt.Gt(s.now().UTC())).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, authnext.ErrInvalidToken
	}
	if err != nil {
		return nil, fmt.Errorf("%w: personal token lookup: %w", authnext.ErrUnavailable, err)
	}
	var permissions []string
	if json.Unmarshal([]byte(row.Permissions), &permissions) != nil || !slices.Contains(permissions, scope.Permission) {
		return nil, authnext.ErrInvalidToken
	}
	claims, err := s.claims(ctx, row.PrincipalID)
	if err != nil {
		return nil, err
	}
	if claims.CredentialVersion != row.CredentialVersion {
		return nil, authnext.ErrInvalidToken
	}
	claims.TenantID = row.TenantID
	return claims, nil
}

// revokeTenantPersonalTokensOn participates in a principal-locked tenant leave
// transaction, so rejoining cannot reactivate an old personal token.
func (s *Service) revokeTenantPersonalTokensOn(ctx context.Context, tx *gorm.DB, tenantID, principalID guid.ID) error {
	if tx == nil || tenantID <= 0 || principalID <= 0 {
		return errPersonalTokenInput
	}
	tables, _, err := s.repo.withDB(tx, true).tables()
	if err != nil {
		return err
	}
	t := tables.PersonalTokens
	_, err = t.WithContext(ctx).Where(t.TenantID.Eq(tenantID), t.PrincipalID.Eq(principalID), t.RevokedAt.IsNull()).Update(t.RevokedAt, s.now().UTC().Truncate(time.Millisecond))
	return err
}
