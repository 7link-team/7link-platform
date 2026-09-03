package authn

import (
	"context"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (r *Repository) activeSession(ctx context.Context, idHash string, now time.Time) (*model.Session, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.Sessions.WithContext(ctx).Where(
		tables.Sessions.IDHash.Eq(idHash),
		tables.Sessions.RevokedAt.IsNull(),
		tables.Sessions.ExpiresAt.Gt(now),
		tables.Sessions.AbsoluteExpiresAt.Gt(now),
	).Take()
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *Repository) touchSession(ctx context.Context, idHash string, lastSeenAt, expiresAt time.Time) (bool, error) {
	tables, db, err := r.tables()
	if err != nil {
		return false, err
	}
	result := db.WithContext(ctx).Table(tables.Sessions.TableName()).
		Where("id_hash = ? AND revoked_at IS NULL", idHash).
		Where("expires_at > ? AND absolute_expires_at > ? AND last_seen_at <= ?", lastSeenAt, lastSeenAt, lastSeenAt).
		Updates(map[string]any{
			"last_seen_at": lastSeenAt,
			"expires_at": gorm.Expr(
				"CASE WHEN absolute_expires_at < ? THEN absolute_expires_at ELSE ? END",
				expiresAt,
				expiresAt,
			),
		})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) createSession(ctx context.Context, row *model.Session) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	return tables.Sessions.WithContext(ctx).Create(row)
}

func (r *Repository) listActiveSessions(ctx context.Context, principalID guid.ID, now time.Time) ([]*model.Session, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.Sessions.WithContext(ctx).Where(
		tables.Sessions.PrincipalID.Eq(principalID),
		tables.Sessions.RevokedAt.IsNull(),
		tables.Sessions.ExpiresAt.Gt(now),
		tables.Sessions.AbsoluteExpiresAt.Gt(now),
	).Order(tables.Sessions.LastSeenAt.Desc(), tables.Sessions.CreatedAt.Desc()).Find()
}

func (r *Repository) revokeSession(ctx context.Context, principalID guid.ID, idHash string, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.Sessions.WithContext(ctx).Where(
		tables.Sessions.IDHash.Eq(idHash),
		tables.Sessions.PrincipalID.Eq(principalID),
		tables.Sessions.RevokedAt.IsNull(),
	).UpdateSimple(tables.Sessions.RevokedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) revokeSessionByHash(ctx context.Context, idHash string, now time.Time) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	result, err := tables.Sessions.WithContext(ctx).Where(
		tables.Sessions.IDHash.Eq(idHash),
		tables.Sessions.RevokedAt.IsNull(),
	).UpdateSimple(tables.Sessions.RevokedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) revokeSessions(
	ctx context.Context,
	principalID guid.ID,
	exceptIDHash string,
	now time.Time,
) (int64, error) {
	tables, db, err := r.tables()
	if err != nil {
		return 0, err
	}
	query := db.WithContext(ctx).Table(tables.Sessions.TableName()).
		Where("principal_id = ? AND revoked_at IS NULL", principalID)
	if exceptIDHash != "" {
		query = query.Where("id_hash <> ?", exceptIDHash)
	}
	result := query.Update("revoked_at", now)
	return result.RowsAffected, result.Error
}

// revokeRefreshTokens is an explicit cross-module write required by authn
// security workflows. It always uses the caller's current transaction.
func (r *Repository) revokeRefreshTokens(ctx context.Context, principalID guid.ID, now time.Time) (int64, error) {
	_, db, err := r.tables()
	if err != nil {
		return 0, err
	}
	result := db.WithContext(ctx).Table(r.table("iam_refresh_tokens")).
		Where("principal_id = ? AND revoked_at IS NULL", principalID).Update("revoked_at", now)
	return result.RowsAffected, result.Error
}

func (r *Repository) revokeOtherAuthentication(
	ctx context.Context,
	principalID guid.ID,
	currentSessionHash string,
	now time.Time,
) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.revokeSessions(ctx, principalID, currentSessionHash, now); err != nil {
			return err
		}
		_, err := repository.revokeRefreshTokens(ctx, principalID, now)
		return err
	})
}

func (r *Repository) rotateSession(
	ctx context.Context,
	principalID guid.ID,
	currentIDHash string,
	replacement *model.Session,
	now time.Time,
) (*model.Session, bool, error) {
	var current *model.Session
	changed := false
	err := r.runInTx(ctx, func(repository *Repository) error {
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		current, err = tables.Sessions.WithContext(ctx).Where(
			tables.Sessions.IDHash.Eq(currentIDHash),
			tables.Sessions.PrincipalID.Eq(principalID),
			tables.Sessions.RevokedAt.IsNull(),
			tables.Sessions.ExpiresAt.Gt(now),
			tables.Sessions.AbsoluteExpiresAt.Gt(now),
		).Take()
		if err != nil {
			return err
		}
		result, err := tables.Sessions.WithContext(ctx).Where(
			tables.Sessions.IDHash.Eq(currentIDHash),
			tables.Sessions.PrincipalID.Eq(principalID),
			tables.Sessions.RevokedAt.IsNull(),
			tables.Sessions.ExpiresAt.Gt(now),
			tables.Sessions.AbsoluteExpiresAt.Gt(now),
		).UpdateSimple(tables.Sessions.RevokedAt.Value(now))
		if err != nil {
			return err
		}
		changed = result.RowsAffected == 1
		if !changed {
			return nil
		}
		return tables.Sessions.WithContext(ctx).Create(replacement)
	})
	return current, changed, err
}
