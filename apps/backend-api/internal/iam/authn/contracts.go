// Package authn owns IAM authentication and its cross-module service contracts.
package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
)

// InvitationCredentials issues tenant invitation tokens and derives their
// digests. The organization module compares digests when accepting a token.
type InvitationCredentials interface {
	IssueInvitationToken(guid.ID) (string, string, error)
	InvitationTokenDigest(string) (string, error)
}

// WebService is the authentication workflow consumed by OAuth and federation.
// The authentication module's concrete service also implements the narrower
// authz request-path contracts.
type WebService interface {
	Authenticate(context.Context, string, string) (*authnext.Claims, error)
	ClearCookie() string
	CookieSecure() bool
	CreateSession(context.Context, guid.ID, time.Time, authnext.Assurance) (string, error)
	Issuer() string
	IssueTenantAccessTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, int64, error)
	IssueTenantIDTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, error)
	IssueTenantOAuthClientToken(context.Context, string, string, string, string, string) (string, int64, error)
	IssueTenantServiceAccountToken(context.Context, string, string, string, string, string, int64) (string, int64, error)
	JWKS() map[string]any
	Logout(context.Context, string) string
	ResolveReturnURL(string) (string, error)
	SessionCookie(string) string
	SessionCookieName() string
	ValidateCSRF(method, origin, cookieHeader, authorization string) error
}
