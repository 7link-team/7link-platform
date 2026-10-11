package oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"7link.com/platform/internal/iam/oauth/model"
	"7link.com/platform/internal/infra/guid"
)

// Token exchange, RFC 8693. A service holding its own token asks for one that
// acts as somebody else, so that a downstream call carries the person's
// authority rather than the service's.
//
// The permission is never implied. A client with no exchange grant cannot
// exchange at all, and a client with one cannot step outside the scopes that
// grant names: the pair has to be written down before it exists. That is the
// whole reason this is a table and not a flag on the client.
const (
	grantTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	// tokenTypeAccess is the only subject and issued token type supported. The
	// RFC allows several; admitting one this deployment cannot verify would be
	// accepting an assertion from somewhere it does not trust.
	tokenTypeAccess = "urn:ietf:params:oauth:token-type:access_token"

	ExchangeGrantActive   = "active"
	ExchangeGrantDisabled = "disabled"
)

var (
	// ErrExchangeNotPermitted reports a client asking to act as a subject it has
	// no written grant for.
	ErrExchangeNotPermitted = errors.New("token exchange is not permitted for this client and subject")
	// ErrUnsupportedTokenType reports a subject token this deployment cannot
	// verify.
	ErrUnsupportedTokenType = errors.New("unsupported token type")
	ErrExchangeGrantExists  = errors.New("token exchange grant exists")
	ErrExchangeGrantMissing = errors.New("token exchange grant not found")
	ErrInvalidExchangeGrant = errors.New("invalid token exchange grant")
)

// ExchangeGrant is one written-down delegation a tenant can read back.
type ExchangeGrant struct {
	ID            guid.ID   `json:"id"`
	TenantID      guid.ID   `json:"tenant_id"`
	ClientID      guid.ID   `json:"client_id"`
	SubjectPrefix string    `json:"subject_prefix" doc:"Principal identifier, or a prefix ending in * to permit a range"`
	Scope         string    `json:"scope" doc:"Scopes this delegation may ask for; empty permits none"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExchangeGrantInput carries the validated fields the management API accepts.
type ExchangeGrantInput struct {
	ClientID      guid.ID
	SubjectPrefix string
	Scope         string
	Status        string
}

// subjectPermitted reports whether a grant covers a subject. A prefix ending in
// a star covers what starts with it, which is how a tenant permits a range
// without listing every principal; anything else has to match exactly.
//
// An empty prefix covers nothing. That is deliberate: the obvious reading of
// "empty means everyone" would turn a half-written row into blanket
// impersonation.
func subjectPermitted(prefix, subject string) bool {
	if prefix == "" || subject == "" {
		return false
	}
	if trimmed, wildcard := strings.CutSuffix(prefix, "*"); wildcard {
		return trimmed != "" && strings.HasPrefix(subject, trimmed)
	}
	return prefix == subject
}

// tokenExchange issues a token acting as the subject of the presented one. The
// subject token is verified rather than read: a client that could hand in any
// string would be choosing who it acts as.
func (s *Service) tokenExchange(
	ctx context.Context, repo *Repository, client *model.Client, form url.Values,
) (TokenResponse, error) {
	// A public client authenticates with nothing, so holding its identifier would
	// be enough to act as every principal a grant covers. The delegation is only
	// meaningful for a client that proves who it is.
	if client.PublicClient || !containsWord(client.GrantTypes, grantTokenExchange) {
		return TokenResponse{}, ErrInvalidRequest
	}
	subjectToken := form.Get("subject_token")
	if subjectToken == "" {
		return TokenResponse{}, ErrInvalidRequest
	}
	// Both types default to an access token, which is the only one supported;
	// naming anything else is refused rather than quietly treated as that.
	if subjectType := form.Get("subject_token_type"); subjectType != "" && subjectType != tokenTypeAccess {
		return TokenResponse{}, ErrUnsupportedTokenType
	}
	if issuedType := form.Get("requested_token_type"); issuedType != "" && issuedType != tokenTypeAccess {
		return TokenResponse{}, ErrUnsupportedTokenType
	}

	claims, err := s.authn.Authenticate(ctx, "Bearer "+subjectToken, "")
	if err != nil {
		return TokenResponse{}, errInvalidClient
	}
	if claims.PrincipalID.Zero() {
		return TokenResponse{}, errInvalidClient
	}
	// The subject has to belong to the client's tenant. Without this a client
	// could exchange a token minted for another tenant it happens to hold.
	if err := repo.activeMember(ctx, client.TenantID, claims.PrincipalID); err != nil {
		return TokenResponse{}, err
	}

	grant, err := repo.exchangeGrantFor(ctx, client.TenantID, client.ID, claims.PrincipalID.String())
	// No grant covering the subject is a refusal; a failed read is a fault, and
	// reporting one as the other would read as a withdrawn delegation.
	if err != nil {
		return TokenResponse{}, err
	}
	// The scopes a delegation may ask for are the grant's, not the client's: a
	// client permitted to act as somebody is not thereby permitted everything it
	// can do as itself.
	scope := normalizeWords(form.Get("scope"))
	if scope == "" {
		scope = normalizeWords(grant.Scope)
	}
	if scope == "" || !allowedScopes(grant.Scope, scope) || !allowedScopes(client.Scopes, scope) {
		return TokenResponse{}, ErrExchangeNotPermitted
	}

	// The assurance travels from the subject token. An exchanged token must not
	// claim a stronger authentication than the one it was derived from, and it
	// records the client that asked so an audit can tell a delegated call from a
	// direct one.
	assurance := oauthAssurance(claims.AuthTime, claims.ACR, strings.Join(claims.AMR, " "), client.ID.String())
	access, expires, err := s.authn.IssueTenantAccessTokenWithAssurance(
		ctx, claims.PrincipalID, client.TenantID.String(), "", scope, assurance,
	)
	if err != nil {
		return TokenResponse{}, err
	}
	// No refresh token is issued. A delegation that could be refreshed would
	// outlive the authority it was derived from, and the holder can exchange
	// again while its own token is valid.
	return TokenResponse{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: expires, Scope: scope,
		IssuedTokenType: tokenTypeAccess,
	}, nil
}

// CreateExchangeGrant writes down one delegation.
func (s *Service) CreateExchangeGrant(ctx context.Context, tenantID guid.ID, input ExchangeGrantInput) (ExchangeGrant, error) {
	row, err := s.normalizeExchangeGrant(tenantID, input)
	if err != nil {
		return ExchangeGrant{}, err
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return ExchangeGrant{}, err
	}
	// The client has to exist in the tenant, or a grant would name a client that
	// could never present it and read as permission that is not.
	client, err := repo.deviceClient(ctx, row.ClientID)
	if err != nil && !errors.Is(err, ErrInvalidRequest) {
		return ExchangeGrant{}, err
	}
	if err != nil || client.TenantID != tenantID {
		return ExchangeGrant{}, ErrInvalidExchangeGrant
	}
	id, err := s.repo.ids.Next()
	if err != nil {
		return ExchangeGrant{}, err
	}
	row.ID = guid.ID(id)
	now := oauthTime(s.now())
	row.CreatedAt, row.UpdatedAt = now, now
	if err := repo.insertExchangeGrant(ctx, &row); err != nil {
		return ExchangeGrant{}, err
	}
	return projectExchangeGrant(row), nil
}

func (s *Service) ListExchangeGrants(ctx context.Context, tenantID guid.ID) ([]ExchangeGrant, error) {
	rows, err := s.repo.listExchangeGrants(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	items := make([]ExchangeGrant, 0, len(rows))
	for _, row := range rows {
		items = append(items, projectExchangeGrant(row))
	}
	return items, nil
}

func (s *Service) DeleteExchangeGrant(ctx context.Context, tenantID, id guid.ID) error {
	return s.repo.deleteExchangeGrant(ctx, tenantID, id)
}

func (s *Service) normalizeExchangeGrant(tenantID guid.ID, input ExchangeGrantInput) (model.TokenExchangeGrant, error) {
	prefix := strings.TrimSpace(input.SubjectPrefix)
	scope := normalizeWords(input.Scope)
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = ExchangeGrantActive
	}
	if tenantID.Zero() || input.ClientID.Zero() || prefix == "" || len(prefix) > 128 ||
		scope == "" || (status != ExchangeGrantActive && status != ExchangeGrantDisabled) {
		return model.TokenExchangeGrant{}, ErrInvalidExchangeGrant
	}
	// A bare star would permit acting as anybody, which is what this table exists
	// to prevent being expressible by accident.
	if prefix == "*" {
		return model.TokenExchangeGrant{}, ErrInvalidExchangeGrant
	}
	return model.TokenExchangeGrant{
		TenantID: tenantID, ClientID: input.ClientID, SubjectPrefix: prefix,
		Scope: scope, Status: status,
	}, nil
}

func projectExchangeGrant(row model.TokenExchangeGrant) ExchangeGrant {
	return ExchangeGrant{
		ID: row.ID, TenantID: row.TenantID, ClientID: row.ClientID,
		SubjectPrefix: row.SubjectPrefix, Scope: row.Scope, Status: row.Status,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
