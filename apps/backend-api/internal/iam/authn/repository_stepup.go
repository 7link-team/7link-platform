package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen/field"
)

func (r *Repository) createStepUpChallenge(ctx context.Context, row *model.StepUpChallenge, now time.Time) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		if _, err := tables.StepUpChallenges.WithContext(ctx).Where(
			tables.StepUpChallenges.PrincipalID.Eq(row.PrincipalID),
			field.Or(tables.StepUpChallenges.ConsumedAt.IsNotNull(), tables.StepUpChallenges.ExpiresAt.Lte(now)),
		).Delete(); err != nil {
			return err
		}
		return tables.StepUpChallenges.WithContext(ctx).Create(row)
	})
}

func (r *Repository) getStepUpChallenge(ctx context.Context, idHash string) (*model.StepUpChallenge, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.StepUpChallenges.WithContext(ctx).
		Where(tables.StepUpChallenges.IDHash.Eq(idHash)).Take()
}

func (r *Repository) failStepUpChallenge(
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
	result := db.WithContext(ctx).Table(tables.StepUpChallenges.TableName()).
		Where("id_hash = ? AND consumed_at IS NULL AND expires_at > ? AND attempts = ?", idHash, now, expectedAttempts).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) consumeStepUpChallenge(
	ctx context.Context,
	idHash string,
	principalID guid.ID,
	sessionHash string,
	now time.Time,
) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.StepUpChallenges.TableName()).
		Where("id_hash = ? AND principal_id = ? AND session_hash = ?", idHash, principalID, sessionHash).
		Where("consumed_at IS NULL AND expires_at > ?", now).Update("consumed_at", now)
	return result.RowsAffected == 1, result.Error
}
