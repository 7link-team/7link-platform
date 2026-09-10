// Package authn owns IAM authentication and its cross-module service contracts.
package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
)

// InvitationCredentials issues tenant invitation tokens and recognizes them
// again. The organization module asks whether a presented token produces the
// digest it stored, rather than deriving one itself, so that a token issued
// before a key rotation is still recognized afterwards.
type InvitationCredentials interface {
	IssueInvitationToken(guid.ID) (string, string, error)
	InvitationTokenMatches(token, digest string) bool
}

// OAuthGrantValidator verifies the current application grant for a principal
// bearer token. The OAuth module supplies this capability without owning authn.
type OAuthGrantValidator func(context.Context, *authnext.Claims) error

// WithOAuthGrantValidator connects application-grant validation to bearer authentication.
func WithOAuthGrantValidator(check OAuthGrantValidator) WebOption {
	return func(service *Service) { service.oauthGrants = check }
}

// WebService is the authentication workflow consumed by OAuth and federation.
// The authentication module's concrete service also implements the narrower
// authz request-path contracts.
type WebService interface {
	Authenticate(context.Context, string, string) (*authnext.Claims, error)
	ProjectProfile(context.Context, *authnext.Claims, string) (ProfileClaims, error)
	ClearCookie() string
	CookieSecure() bool
	CreateSession(context.Context, guid.ID, time.Time, authnext.Assurance) (string, error)
	Issuer() string
	IssueTenantAccessTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, int64, error)
	IssueTenantIDTokenWithAssurance(context.Context, guid.ID, string, string, string, string, authnext.Assurance) (string, error)
	IssueTenantOAuthClientToken(context.Context, string, string, string, string, string) (string, int64, error)
	IssueTenantServiceAccountToken(context.Context, string, string, string, string, string, int64) (string, int64, error)
	JWKS() map[string]any
	Logout(context.Context, string) (string, error)
	ResolveReturnURL(string) (string, error)
	SessionCookie(string) string
	SessionCookieName() string
	ValidateCSRF(method, origin, cookieHeader, authorization string) error
}

// ProfileClaims contains only tenant-policy-visible and scope-authorized claims.
// Omission is distinct from an empty string or a false verification state.
type ProfileClaims struct {
	PreferredUsername *string `json:"preferred_username,omitempty"`
	Email             *string `json:"email,omitempty"`
	EmailVerified     *bool   `json:"email_verified,omitempty"`
}
