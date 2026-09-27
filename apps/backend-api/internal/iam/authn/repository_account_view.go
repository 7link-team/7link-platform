package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (r *Repository) createAccountViewToken(ctx context.Context, row *model.AccountViewToken) error {
	if row == nil {
		return authnext.ErrInvalidToken
	}
	if row.Mode == "" {
		row.Mode = "read_only"
	}
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	return tables.AccountViewTokens.WithContext(ctx).Create(row)
}

// activeAccountViewToken checks the token and its operator session without
// touching any browser session. Permission and principal-state checks are separate.
func (r *Repository) activeAccountViewToken(ctx context.Context, hash string, now time.Time) (*model.AccountViewToken, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	v := tables.AccountViewTokens
	row, err := v.WithContext(ctx).Where(v.TokenHash.Eq(hash), v.RevokedAt.IsNull(), v.CreatedAt.Lte(now), v.ExpiresAt.Gt(now)).Take()
	if err != nil {
		return nil, err
	}
	session, err := r.activeSession(ctx, row.ActorSessionHash, now)
	if err != nil {
		return nil, err
	}
	if session.PrincipalID != row.ActorID {
		return nil, gorm.ErrRecordNotFound
	}
	return row, nil
}

func (r *Repository) touchAccountViewToken(ctx context.Context, id guid.ID, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	v := tables.AccountViewTokens
	result, err := v.WithContext(ctx).Where(v.ID.Eq(id), v.RevokedAt.IsNull(), v.CreatedAt.Lte(now), v.ExpiresAt.Gt(now)).
		Where(v.WithContext(ctx).Where(v.LastUsedAt.IsNull()).Or(v.LastUsedAt.Lte(now))).Update(v.LastUsedAt, now)
	return result.RowsAffected == 1, err
}

func (r *Repository) revokeAccountViewToken(ctx context.Context, id, actorID guid.ID, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	v := tables.AccountViewTokens
	result, err := v.WithContext(ctx).Where(v.ID.Eq(id), v.ActorID.Eq(actorID), v.RevokedAt.IsNull()).Update(v.RevokedAt, now)
	return result.RowsAffected == 1, err
}

func (r *Repository) revokePrincipalAccountViews(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	v := tables.AccountViewTokens
	_, err = v.WithContext(ctx).Where(v.RevokedAt.IsNull()).Where(v.ActorID.Eq(principalID)).
		Or(v.WithContext(ctx).Where(v.RevokedAt.IsNull(), v.PrincipalID.Eq(principalID))).Update(v.RevokedAt, now)
	return err
}
