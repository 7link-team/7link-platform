package organization

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// ErasePrincipalData removes the email from invitations whose accepted subject
// is known. Unaccepted invitations belong to recipients, not inferred principals.
// The caller owns the transaction and the suspended principal's row lock.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, now time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	return tx.WithContext(ctx).Table(prefix+"iam_invitations").Where("accepted_by = ?", id).
		Updates(map[string]any{"email": "", "email_key": "", "updated_at": now}).Error
}
