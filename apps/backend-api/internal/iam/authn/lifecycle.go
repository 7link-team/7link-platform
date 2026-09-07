package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

var (
	ErrLegacyNotifications  = errors.New("unowned legacy notifications require retention remediation")
	ErrNotificationInFlight = errors.New("principal notification delivery is in progress")
)

// ErasePrincipalData removes authentication secrets and operational PII using
// the caller's transaction, which must already lock the suspended principal.
// It is independent of module activation so disabled modules leave no data behind.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, _ time.Time) error {
	if tx == nil || id <= 0 {
		return errRepositoryUnavailable
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	var count int64
	if err := tx.WithContext(ctx).Table(prefix + "iam_notification_outbox").Where("principal_id IS NULL").Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrLegacyNotifications
	}
	if err := tx.WithContext(ctx).Table(prefix+"iam_notification_outbox").
		Where("principal_id = ? AND status = 'delivering'", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrNotificationInFlight
	}
	for _, table := range []string{
		"iam_notification_outbox", "iam_email_verification_tokens", "iam_password_recovery_tokens",
		"iam_stepup_challenges", "iam_passkey_challenges", "iam_passkeys", "iam_passkey_users",
		"iam_mfa_challenges", "iam_recovery_codes", "iam_mfa_enrollments", "iam_password_history",
		"iam_sessions", "iam_credentials",
	} {
		if err := tx.WithContext(ctx).Table(prefix+table).Where("principal_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return fmt.Errorf("erase authentication table %s: %w", table, err)
		}
	}
	return nil
}
