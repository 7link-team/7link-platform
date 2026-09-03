package authn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (r *Repository) getCredential(ctx context.Context, principalID guid.ID) (*model.Credential, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.Credentials.WithContext(ctx).
		Where(tables.Credentials.PrincipalID.Eq(principalID)).Take()
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *Repository) createCredential(ctx context.Context, row *model.Credential) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	return tables.Credentials.WithContext(ctx).Create(row)
}

func (r *Repository) resetBootstrapCredential(
	ctx context.Context,
	principalID guid.ID,
	passwordHash *string,
	incrementVersion bool,
	now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	updates := map[string]any{
		"failed_attempts": 0,
		"locked_until":    nil,
		"updated_at":      now,
	}
	if passwordHash != nil {
		updates["password_hash"] = *passwordHash
		updates["password_changed_at"] = now
	}
	if incrementVersion {
		updates["credential_version"] = gorm.Expr("credential_version + 1")
	}
	result := db.WithContext(ctx).Table(tables.Credentials.TableName()).
		Where("principal_id = ?", principalID).Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) getMFAEnabledCredential(ctx context.Context, principalID guid.ID) (*model.Credential, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.Credentials.WithContext(ctx).
		Where(tables.Credentials.PrincipalID.Eq(principalID), tables.Credentials.MFARequired.Is(true)).Take()
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *Repository) recordLoginFailure(
	ctx context.Context,
	principalID guid.ID,
	maxAttempts int,
	lockDuration time.Duration,
	now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	if maxAttempts <= 0 || lockDuration <= 0 {
		return errors.New("login failure policy must be positive")
	}
	threshold := maxAttempts - 1
	// locked_until is assigned first because MySQL evaluates UPDATE assignments
	// from left to right; both CASE expressions must observe the old counter.
	statement := fmt.Sprintf(`UPDATE %s
		SET locked_until = CASE WHEN failed_attempts >= ? THEN ? ELSE NULL END,
			failed_attempts = CASE WHEN failed_attempts >= ? THEN 0 ELSE failed_attempts + 1 END,
			updated_at = ?
		WHERE principal_id = ? AND (locked_until IS NULL OR locked_until <= ?)`, tables.Credentials.TableName())
	result := db.WithContext(ctx).Exec(
		statement, threshold, now.Add(lockDuration), threshold, now, principalID, now,
	)
	return result.Error
}

func (r *Repository) resetLoginFailures(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(tables.Credentials.TableName()).
		Where("principal_id = ?", principalID).
		Updates(map[string]any{"failed_attempts": 0, "locked_until": nil, "updated_at": now}).Error
}

func (r *Repository) requireMFA(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.Credentials.WithContext(ctx).
		Where(tables.Credentials.PrincipalID.Eq(principalID)).
		UpdateSimple(tables.Credentials.MFARequired.Value(true), tables.Credentials.UpdatedAt.Value(now))
	return err
}

func (r *Repository) enableMFA(
	ctx context.Context,
	principalID guid.ID,
	secretCiphertext string,
	lastUsedStep int64,
	now time.Time,
) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.Credentials.WithContext(ctx).
		Where(tables.Credentials.PrincipalID.Eq(principalID), tables.Credentials.MFARequired.Is(false)).
		UpdateSimple(
			tables.Credentials.TOTPSecret.Value(secretCiphertext),
			tables.Credentials.MFARequired.Value(true),
			tables.Credentials.TOTPLastUsedStep.Value(lastUsedStep),
			tables.Credentials.UpdatedAt.Value(now),
		)
	return result.RowsAffected == 1, err
}

func (r *Repository) disableMFA(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(tables.Credentials.TableName()).Where("principal_id = ?", principalID).
		Updates(map[string]any{
			"totp_secret":         "",
			"mfa_required":        false,
			"totp_last_used_step": int64(0),
			"updated_at":          now,
		}).Error
}

func (r *Repository) advanceTOTPStep(ctx context.Context, principalID guid.ID, step int64) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.Credentials.TableName()).
		Where("principal_id = ? AND totp_last_used_step < ?", principalID, step).
		Update("totp_last_used_step", step)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) updatePassword(
	ctx context.Context,
	principalID guid.ID,
	oldHash, newHash string,
	now time.Time,
	incrementVersion bool,
	recoveryCooldownUntil *time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	updates := map[string]any{
		"password_hash":       newHash,
		"password_changed_at": now,
		"updated_at":          now,
		"failed_attempts":     0,
		"locked_until":        nil,
	}
	if recoveryCooldownUntil != nil {
		updates["recovery_cooldown_until"] = *recoveryCooldownUntil
	}
	if incrementVersion {
		updates["credential_version"] = gorm.Expr("credential_version + 1")
	}
	result := db.WithContext(ctx).Table(tables.Credentials.TableName()).
		Where("principal_id = ? AND password_hash = ?", principalID, oldHash).Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) recoveryCooldown(ctx context.Context, principalID guid.ID) (*time.Time, error) {
	credential, err := r.getCredential(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("load recovery cooldown: %w", err)
	}
	return credential.RecoveryCooldownUntil, nil
}

func (r *Repository) listPasswordHistory(ctx context.Context, principalID guid.ID, limit int) ([]*model.PasswordHistory, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	query := tables.PasswordHistory.WithContext(ctx).
		Where(tables.PasswordHistory.PrincipalID.Eq(principalID)).
		Order(tables.PasswordHistory.CreatedAt.Desc(), tables.PasswordHistory.ID.Desc())
	if limit > 0 {
		query = query.Limit(limit)
	}
	return query.Find()
}

func (r *Repository) appendPasswordHistory(
	ctx context.Context,
	principalID guid.ID,
	passwordHash string,
	now time.Time,
) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	id, err := r.nextID()
	if err != nil {
		return fmt.Errorf("generate password history id: %w", err)
	}
	return tables.PasswordHistory.WithContext(ctx).Create(&model.PasswordHistory{
		ID: id, PrincipalID: principalID, PasswordHash: passwordHash, CreatedAt: now,
	})
}

func (r *Repository) trimPasswordHistory(ctx context.Context, principalID guid.ID, keep int) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	rows, err := r.listPasswordHistory(ctx, principalID, 0)
	if err != nil {
		return err
	}
	if len(rows) <= keep {
		return nil
	}
	ids := make([]guid.ID, len(rows)-keep)
	for index, row := range rows[keep:] {
		ids[index] = row.ID
	}
	return db.WithContext(ctx).Table(tables.PasswordHistory.TableName()).
		Where("id IN ?", ids).Delete(&model.PasswordHistory{}).Error
}
