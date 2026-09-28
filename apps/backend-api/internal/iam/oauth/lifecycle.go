package oauth

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/oauth/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
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
	names := query.BindNames(prefix)
	for _, table := range []gormx.TableName{
		names.ConsentRequests, names.AuthorizationCodes, names.RefreshTokens, names.Consents,
	} {
		if err := tx.WithContext(ctx).Table(table.String()).Where("principal_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return err
		}
	}
	return nil
}
