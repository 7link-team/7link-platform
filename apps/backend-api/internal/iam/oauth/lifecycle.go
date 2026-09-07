package oauth

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// ErasePrincipalData destroys OAuth authorization and token material in the
// caller's transaction after the suspended principal has been locked.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, _ time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	for _, table := range []string{"iam_oauth_consent_requests", "iam_oauth_codes", "iam_refresh_tokens", "iam_oauth_consents"} {
		if err := tx.WithContext(ctx).Table(prefix+table).Where("principal_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return err
		}
	}
	return nil
}
