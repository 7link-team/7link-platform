package organization

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/organization/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// PrincipalErasureVersion is the schema version ErasePrincipalData needs: the
// later of this module's principal-erasure guard migration and the migration
// that created the newest table erasure touches. Raise it when erasure reaches
// a later table: the principallifecycle erasure-version test fails until then.
const PrincipalErasureVersion = 9

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
	invitations := query.BindNames(prefix).Invitations.String()
	return tx.WithContext(ctx).Table(invitations).Where("accepted_by = ?", id).
		Updates(map[string]any{"email": "", "email_key": "", "updated_at": now}).Error
}
