package governance

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/governance/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// ErasePrincipalData redacts cached principal names without deleting approval
// decisions or their identity references. The caller owns the transaction and
// the suspended principal's row lock.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, _ time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	items := query.BindNames(prefix).AccessReviewItems.String()
	return tx.WithContext(ctx).Table(items).Where("principal_id = ?", id).
		Update("principal_name", "Anonymized account").Error
}
