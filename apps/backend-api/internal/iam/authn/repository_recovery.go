package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (r *Repository) createPasswordRecovery(ctx context.Context, row *model.PasswordRecoveryToken, now time.Time) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, row.PrincipalID); err != nil {
			return err
		}
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.PasswordRecoveryTokens.WithContext(ctx).
			Where(
				tables.PasswordRecoveryTokens.PrincipalID.Eq(row.PrincipalID),
				tables.PasswordRecoveryTokens.ConsumedAt.IsNull(),
			).UpdateSimple(tables.PasswordRecoveryTokens.ConsumedAt.Value(now)); err != nil {
			return err
		}
		return tables.PasswordRecoveryTokens.WithContext(ctx).Create(row)
	})
}

// activePasswordRecovery finds the outstanding token matching any of the
// supplied authenticators. The caller supplies one per key the ring holds, so a
// recovery mail sent before a key rotation still resolves.
func (r *Repository) activePasswordRecovery(
	ctx context.Context,
	tokenHMACs []string,
	now time.Time,
) (*model.PasswordRecoveryToken, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	if len(tokenHMACs) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return tables.PasswordRecoveryTokens.WithContext(ctx).Where(
		tables.PasswordRecoveryTokens.TokenHMAC.In(tokenHMACs...),
		tables.PasswordRecoveryTokens.ConsumedAt.IsNull(),
		tables.PasswordRecoveryTokens.ExpiresAt.Gt(now),
	).Take()
}

func (r *Repository) consumePasswordRecovery(ctx context.Context, tokenHMAC string, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.PasswordRecoveryTokens.WithContext(ctx).Where(
		tables.PasswordRecoveryTokens.TokenHMAC.Eq(tokenHMAC),
		tables.PasswordRecoveryTokens.ConsumedAt.IsNull(),
		tables.PasswordRecoveryTokens.ExpiresAt.Gt(now),
	).UpdateSimple(tables.PasswordRecoveryTokens.ConsumedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) consumePasswordRecoveries(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.PasswordRecoveryTokens.WithContext(ctx).Where(
		tables.PasswordRecoveryTokens.PrincipalID.Eq(principalID),
		tables.PasswordRecoveryTokens.ConsumedAt.IsNull(),
	).UpdateSimple(tables.PasswordRecoveryTokens.ConsumedAt.Value(now))
	return err
}

func (r *Repository) enqueueNotification(ctx context.Context, row *model.NotificationOutbox) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	return tables.NotificationOutbox.WithContext(ctx).Create(row)
}

func (r *Repository) resetStaleNotifications(ctx context.Context, staleBefore time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(tables.NotificationOutbox.TableName()).
		Where("status = ? AND (locked_at IS NULL OR locked_at < ?)", "delivering", staleBefore).
		Updates(map[string]any{"status": "pending", "locked_at": nil}).Error
}

func (r *Repository) pendingNotificationIDs(ctx context.Context, now time.Time, limit int) ([]guid.ID, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.NotificationOutbox.WithContext(ctx).
		Select(tables.NotificationOutbox.ID).
		Where(
			tables.NotificationOutbox.Status.Eq("pending"),
			tables.NotificationOutbox.AvailableAt.Lte(now),
		).Order(tables.NotificationOutbox.CreatedAt).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]guid.ID, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	return ids, nil
}

func (r *Repository) claimNotification(
	ctx context.Context,
	id guid.ID,
	now time.Time,
) (*model.NotificationOutbox, bool, error) {
	var claimed *model.NotificationOutbox
	err := r.runInTx(ctx, func(repository *Repository) error {
		tables, db, err := repository.tables()
		if err != nil {
			return err
		}
		result := db.WithContext(ctx).Table(tables.NotificationOutbox.TableName()).
			Where("id = ? AND status = ? AND available_at <= ?", id, "pending", now).
			Updates(map[string]any{
				"status":     "delivering",
				"locked_at":  now,
				"attempts":   gorm.Expr("attempts + 1"),
				"updated_at": now,
			})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		claimed, err = tables.NotificationOutbox.WithContext(ctx).
			Where(tables.NotificationOutbox.ID.Eq(id)).Take()
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return claimed, claimed != nil, nil
}

func (r *Repository) markNotificationSent(ctx context.Context, id guid.ID, attempt int, now time.Time) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.NotificationOutbox.TableName()).
		Where("id = ? AND status = ? AND attempts = ?", id, "delivering", attempt).
		Updates(map[string]any{
			"status":     "sent",
			"sent_at":    now,
			"locked_at":  nil,
			"last_error": "",
			"updated_at": now,
		})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) markNotificationFailed(
	ctx context.Context,
	id guid.ID,
	attempt int,
	status, lastError string,
	availableAt, now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.NotificationOutbox.TableName()).
		Where("id = ? AND status = ? AND attempts = ?", id, "delivering", attempt).
		Updates(map[string]any{
			"status":       status,
			"available_at": availableAt,
			"locked_at":    nil,
			"last_error":   lastError,
			"updated_at":   now,
		})
	return result.RowsAffected == 1, result.Error
}
