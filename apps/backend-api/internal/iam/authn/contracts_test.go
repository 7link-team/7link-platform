package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
)

var (
	_ InvitationCredentials = (*webServiceFixture)(nil)
	_ WebService            = (*webServiceFixture)(nil)
)

type webServiceFixture struct{}

func (*webServiceFixture) IssueInvitationToken(guid.ID) (string, string, error) {
	return "", "", nil
}

func (*webServiceFixture) InvitationTokenDigest(string) (string, error) { return "", nil }

func (*webServiceFixture) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return nil, nil
}

func (*webServiceFixture) ClearCookie() string  { return "" }
func (*webServiceFixture) CookieSecure() bool   { return false }
func (*webServiceFixture) Issuer() string       { return "" }
func (*webServiceFixture) JWKS() map[string]any { return nil }
func (*webServiceFixture) Logout(context.Context, string) string {
	return ""
}
func (*webServiceFixture) ResolveReturnURL(string) (string, error) { return "", nil }
func (*webServiceFixture) SessionCookie(string) string             { return "" }

func (*webServiceFixture) CreateSession(context.Context, guid.ID, time.Time, authnext.Assurance) (string, error) {
	return "", nil
}

func (*webServiceFixture) IssueTenantAccessTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, int64, error) {
	return "", 0, nil
}

func (*webServiceFixture) IssueTenantIDTokenWithAssurance(context.Context, guid.ID, string, string, string, authnext.Assurance) (string, error) {
	return "", nil
}

func (*webServiceFixture) IssueTenantOAuthClientToken(context.Context, string, string, string, string, string) (string, int64, error) {
	return "", 0, nil
}

func (*webServiceFixture) IssueTenantServiceAccountToken(context.Context, string, string, string, string, string, int64) (string, int64, error) {
	return "", 0, nil
}
