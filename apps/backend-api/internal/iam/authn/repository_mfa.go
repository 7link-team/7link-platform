package authn

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen/field"
)

var errRecoveryCodePrincipalMismatch = errors.New("recovery code principal does not match replacement owner")

func (r *Repository) replaceMFAEnrollment(ctx context.Context, row *model.MFAEnrollment) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, row.PrincipalID); err != nil {
			return err
		}
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.MFAEnrollments.WithContext(ctx).
			Where(tables.MFAEnrollments.PrincipalID.Eq(row.PrincipalID)).Delete(); err != nil {
			return err
		}
		return tables.MFAEnrollments.WithContext(ctx).Create(row)
	})
}

func (r *Repository) activeMFAEnrollment(ctx context.Context, principalID guid.ID, now time.Time) (*model.MFAEnrollment, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.MFAEnrollments.WithContext(ctx).Where(
		tables.MFAEnrollments.PrincipalID.Eq(principalID),
		tables.MFAEnrollments.ExpiresAt.Gt(now),
	).Take()
}

func (r *Repository) activeMFAEnrollmentForUpdate(
	ctx context.Context,
	principalID guid.ID,
	now time.Time,
) (*model.MFAEnrollment, error) {
	if _, err := r.lockPrincipal(ctx, principalID); err != nil {
		return nil, err
	}
	return r.activeMFAEnrollment(ctx, principalID, now)
}

func (r *Repository) deleteMFAEnrollment(ctx context.Context, principalID guid.ID) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.MFAEnrollments.WithContext(ctx).
		Where(tables.MFAEnrollments.PrincipalID.Eq(principalID)).Delete()
	return err
}

func (r *Repository) countAvailableRecoveryCodes(ctx context.Context, principalID guid.ID) (int64, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	return tables.RecoveryCodes.WithContext(ctx).Where(
		tables.RecoveryCodes.PrincipalID.Eq(principalID),
		tables.RecoveryCodes.UsedAt.IsNull(),
	).Count()
}

func (r *Repository) replaceRecoveryCodes(ctx context.Context, principalID guid.ID, rows []*model.RecoveryCode) error {
	for _, row := range rows {
		if row == nil || row.PrincipalID != principalID {
			return errRecoveryCodePrincipalMismatch
		}
	}
	return r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, principalID); err != nil {
			return err
		}
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.RecoveryCodes.WithContext(ctx).
			Where(tables.RecoveryCodes.PrincipalID.Eq(principalID)).Delete(); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return tables.RecoveryCodes.WithContext(ctx).Create(rows...)
	})
}

func (r *Repository) consumeRecoveryCode(ctx context.Context, principalID guid.ID, codeHash string, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.RecoveryCodes.WithContext(ctx).Where(
		tables.RecoveryCodes.PrincipalID.Eq(principalID),
		tables.RecoveryCodes.CodeHash.Eq(codeHash),
		tables.RecoveryCodes.UsedAt.IsNull(),
	).UpdateSimple(tables.RecoveryCodes.UsedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) createMFAChallenge(ctx context.Context, row *model.MFAChallenge, now time.Time) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.MFAChallenges.WithContext(ctx).Where(
			tables.MFAChallenges.PrincipalID.Eq(row.PrincipalID),
			field.Or(tables.MFAChallenges.ConsumedAt.IsNotNull(), tables.MFAChallenges.ExpiresAt.Lte(now)),
		).Delete(); err != nil {
			return err
		}
		return tables.MFAChallenges.WithContext(ctx).Create(row)
	})
}

func (r *Repository) getMFAChallenge(ctx context.Context, idHash string) (*model.MFAChallenge, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.MFAChallenges.WithContext(ctx).
		Where(tables.MFAChallenges.IDHash.Eq(idHash)).Take()
}

func (r *Repository) failMFAChallenge(
	ctx context.Context,
	idHash string,
	expectedAttempts, maxAttempts int,
	now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	updates := map[string]any{"attempts": expectedAttempts + 1}
	if expectedAttempts+1 >= maxAttempts {
		updates["consumed_at"] = now
	}
	result := db.WithContext(ctx).Table(tables.MFAChallenges.TableName()).
		Where("id_hash = ? AND consumed_at IS NULL AND expires_at > ? AND attempts = ?", idHash, now, expectedAttempts).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) consumeMFAChallenge(ctx context.Context, idHash string, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.MFAChallenges.WithContext(ctx).Where(
		tables.MFAChallenges.IDHash.Eq(idHash),
		tables.MFAChallenges.ConsumedAt.IsNull(),
		tables.MFAChallenges.ExpiresAt.Gt(now),
	).UpdateSimple(tables.MFAChallenges.ConsumedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) purgeMFAState(ctx context.Context, principalID guid.ID) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		for _, remove := range []func() error{
			func() error {
				_, err := tables.MFAEnrollments.WithContext(ctx).Where(tables.MFAEnrollments.PrincipalID.Eq(principalID)).Delete()
				return err
			},
			func() error {
				_, err := tables.RecoveryCodes.WithContext(ctx).Where(tables.RecoveryCodes.PrincipalID.Eq(principalID)).Delete()
				return err
			},
			func() error {
				_, err := tables.MFAChallenges.WithContext(ctx).Where(tables.MFAChallenges.PrincipalID.Eq(principalID)).Delete()
				return err
			},
		} {
			if err := remove(); err != nil {
				return err
			}
		}
		return nil
	})
}
