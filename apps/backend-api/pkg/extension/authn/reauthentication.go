package authn

import (
	"7link.com/platform/pkg/guid"
	"context"
	"gorm.io/gorm"
)

// ReauthenticationConsumer spends a purpose- and target-bound proof in the
// caller's transaction, taking the principal lock before any domain child locks.
type ReauthenticationConsumer func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error

// BrowserSessionSecurity provides cookie-only authentication and origin checks
// to personal security operations without depending on the authn module.
type BrowserSessionSecurity struct {
	Authenticate func(context.Context, string) (*Claims, error)
	ValidateCSRF func(method, origin, cookieHeader string) error
}
