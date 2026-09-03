package authn

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) listPasskeys(ctx context.Context, principalID guid.ID) ([]*model.Passkey, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.Passkeys.WithContext(ctx).
		Where(tables.Passkeys.PrincipalID.Eq(principalID)).
		Order(tables.Passkeys.CreatedAt.Desc()).Find()
}

func (r *Repository) countPasskeys(ctx context.Context, principalID guid.ID) (int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	return tables.Passkeys.WithContext(ctx).
		Where(tables.Passkeys.PrincipalID.Eq(principalID)).Count()
}

func (r *Repository) createPasskey(ctx context.Context, row *model.Passkey, maxCredentials int) (bool, error) {
	created := false
	err := r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, row.PrincipalID); err != nil {
			return err
		}
		count, err := repository.countPasskeys(ctx, row.PrincipalID)
		if err != nil {
			return err
		}
		if count >= int64(maxCredentials) {
			return nil
		}
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if err := tables.Passkeys.WithContext(ctx).Create(row); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (r *Repository) getPasskey(ctx context.Context, idHash string) (*model.Passkey, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.Passkeys.WithContext(ctx).Where(tables.Passkeys.IDHash.Eq(idHash)).Take()
}

func (r *Repository) updatePasskeyCounter(
	ctx context.Context,
	idHash string,
	expectedSignCount, signCount uint32,
	credentialCiphertext string,
	now time.Time,
) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.Passkeys.WithContext(ctx).Where(
		tables.Passkeys.IDHash.Eq(idHash),
		tables.Passkeys.SignCount.Eq(expectedSignCount),
	).UpdateSimple(
		tables.Passkeys.CredentialCiphertext.Value(credentialCiphertext),
		tables.Passkeys.SignCount.Value(signCount),
		tables.Passkeys.UpdatedAt.Value(now),
		tables.Passkeys.LastUsedAt.Value(now),
	)
	return result.RowsAffected == 1, err
}

func (r *Repository) storePasskeyCloneWarning(
	ctx context.Context,
	idHash, credentialCiphertext string,
	now time.Time,
) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.Passkeys.WithContext(ctx).Where(tables.Passkeys.IDHash.Eq(idHash)).UpdateSimple(
		tables.Passkeys.CredentialCiphertext.Value(credentialCiphertext),
		tables.Passkeys.UpdatedAt.Value(now),
	)
	return err
}

func (r *Repository) renamePasskey(
	ctx context.Context,
	principalID guid.ID,
	idHash, name string,
	now time.Time,
) (*model.Passkey, bool, error) {
	var row model.Passkey
	changed := false
	err := r.runInTx(ctx, func(repository *Repository) error {
		tables, db, err := repository.tables()
		if err != nil {
			return err
		}
		lookup := db.WithContext(ctx).Table(tables.Passkeys.TableName()).
			Where("id_hash = ? AND principal_id = ?", idHash, principalID)
		if db.Dialector.Name() != "sqlite" {
			lookup = lookup.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := lookup.Take(&row).Error; err != nil {
			return err
		}
		result, err := tables.Passkeys.WithContext(ctx).Where(
			tables.Passkeys.IDHash.Eq(idHash), tables.Passkeys.PrincipalID.Eq(principalID),
		).UpdateSimple(tables.Passkeys.Name.Value(name), tables.Passkeys.UpdatedAt.Value(now))
		if err != nil {
			return err
		}
		changed = result.RowsAffected == 1
		if changed {
			row.Name = name
			row.UpdatedAt = now
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &row, changed, nil
}

func (r *Repository) deletePasskey(ctx context.Context, principalID guid.ID, idHash string) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.Passkeys.WithContext(ctx).Where(
		tables.Passkeys.IDHash.Eq(idHash), tables.Passkeys.PrincipalID.Eq(principalID),
	).Delete()
	return result.RowsAffected == 1, err
}

func (r *Repository) ensurePasskeyUser(ctx context.Context, row *model.PasskeyUser) (*model.PasskeyUser, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	result := db.WithContext(ctx).Table(tables.PasskeyUsers.TableName()).
		Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	if result.Error != nil {
		return nil, result.Error
	}
	return tables.PasskeyUsers.WithContext(ctx).
		Where(tables.PasskeyUsers.PrincipalID.Eq(row.PrincipalID)).Take()
}

func (r *Repository) getPasskeyUser(ctx context.Context, principalID guid.ID) (*model.PasskeyUser, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.PasskeyUsers.WithContext(ctx).
		Where(tables.PasskeyUsers.PrincipalID.Eq(principalID)).Take()
}

func (r *Repository) findPasskeyUserByHandle(
	ctx context.Context,
	principalID guid.ID,
	userHandle string,
) (*model.PasskeyUser, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.PasskeyUsers.WithContext(ctx).Where(
		tables.PasskeyUsers.PrincipalID.Eq(principalID), tables.PasskeyUsers.UserHandle.Eq(userHandle),
	).Take()
}

func (r *Repository) createPasskeyChallenge(ctx context.Context, row *model.PasskeyChallenge, now time.Time) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.PasskeyChallenges.WithContext(ctx).Where(
			field.Or(tables.PasskeyChallenges.ConsumedAt.IsNotNull(), tables.PasskeyChallenges.ExpiresAt.Lte(now)),
		).Delete(); err != nil {
			return err
		}
		return tables.PasskeyChallenges.WithContext(ctx).Create(row)
	})
}

func (r *Repository) getPasskeyChallenge(
	ctx context.Context,
	idHash, kind string,
	principalID guid.ID,
) (*model.PasskeyChallenge, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	query := db.WithContext(ctx).Table(tables.PasskeyChallenges.TableName()).
		Where("id_hash = ? AND kind = ?", idHash, kind)
	if !principalID.Zero() {
		query = query.Where("principal_id = ?", principalID)
	}
	var row model.PasskeyChallenge
	if err := query.Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) consumePasskeyChallenge(
	ctx context.Context,
	idHash, kind string,
	principalID guid.ID,
	now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	query := db.WithContext(ctx).Table(tables.PasskeyChallenges.TableName()).
		Where("id_hash = ? AND kind = ?", idHash, kind).
		Where("consumed_at IS NULL AND expires_at > ?", now)
	if !principalID.Zero() {
		query = query.Where("principal_id = ?", principalID)
	}
	result := query.Update("consumed_at", now)
	return result.RowsAffected == 1, result.Error
}
