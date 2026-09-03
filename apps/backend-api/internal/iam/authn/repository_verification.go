package authn

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var errAmbiguousEmailVerificationCode = errors.New("ambiguous email verification code")

func (r *Repository) replaceEmailVerification(ctx context.Context, row *model.EmailVerificationToken, now time.Time) error {
	return r.runInTx(ctx, func(repository *Repository) error {
		if _, err := repository.lockPrincipal(ctx, row.PrincipalID); err != nil {
			return err
		}
		if err := repository.consumeEmailVerifications(ctx, row.PrincipalID, now); err != nil {
			return err
		}
		tables, _, err := repository.tables()
		if err != nil {
			return err
		}
		return tables.EmailVerificationTokens.WithContext(ctx).Create(row)
	})
}

func (r *Repository) createEmailVerification(ctx context.Context, row *model.EmailVerificationToken) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	return tables.EmailVerificationTokens.WithContext(ctx).Create(row)
}

func (r *Repository) consumeEmailVerifications(ctx context.Context, principalID guid.ID, now time.Time) error {
	tables, _, err := r.tables()
	if err != nil {
		return err
	}
	_, err = tables.EmailVerificationTokens.WithContext(ctx).Where(
		tables.EmailVerificationTokens.PrincipalID.Eq(principalID),
		tables.EmailVerificationTokens.ConsumedAt.IsNull(),
	).UpdateSimple(tables.EmailVerificationTokens.ConsumedAt.Value(now))
	return err
}

func (r *Repository) activeEmailVerificationByHMAC(
	ctx context.Context,
	tokenHMAC string,
	now time.Time,
) (*model.EmailVerificationToken, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	return tables.EmailVerificationTokens.WithContext(ctx).Where(
		tables.EmailVerificationTokens.TokenHMAC.Eq(tokenHMAC),
		tables.EmailVerificationTokens.ConsumedAt.IsNull(),
		tables.EmailVerificationTokens.ExpiresAt.Gt(now),
	).Take()
}

func (r *Repository) activeEmailVerificationByEmailAndCode(
	ctx context.Context,
	email string,
	code string,
	maxAttempts int,
	now time.Time,
) (*model.EmailVerificationToken, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	rows, err := tables.EmailVerificationTokens.WithContext(ctx).Where(
		tables.EmailVerificationTokens.Email.Eq(email),
		tables.EmailVerificationTokens.Code.Eq(code),
		tables.EmailVerificationTokens.Attempts.Lt(maxAttempts),
		tables.EmailVerificationTokens.ConsumedAt.IsNull(),
		tables.EmailVerificationTokens.ExpiresAt.Gt(now),
	).Limit(2).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(rows) != 1 {
		return nil, errAmbiguousEmailVerificationCode
	}
	return rows[0], nil
}

func (r *Repository) recordEmailVerificationAttempt(
	ctx context.Context,
	email string,
	maxAttempts int,
	now time.Time,
) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Table(tables.EmailVerificationTokens.TableName()).Where(
		"email = ? AND consumed_at IS NULL AND expires_at > ? AND attempts < ?", email, now, maxAttempts,
	).UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error
}

func (r *Repository) consumeEmailVerification(
	ctx context.Context,
	tokenHMAC string,
	maxAttempts int,
	now time.Time,
) (bool, error) {
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	statement := tables.EmailVerificationTokens.WithContext(ctx).Where(
		tables.EmailVerificationTokens.TokenHMAC.Eq(tokenHMAC),
		tables.EmailVerificationTokens.ConsumedAt.IsNull(),
		tables.EmailVerificationTokens.ExpiresAt.Gt(now),
	)
	if maxAttempts > 0 {
		statement = statement.Where(tables.EmailVerificationTokens.Attempts.Lt(maxAttempts))
	}
	result, err := statement.UpdateSimple(tables.EmailVerificationTokens.ConsumedAt.Value(now))
	return result.RowsAffected == 1, err
}

func (r *Repository) latestEmailVerificationTime(ctx context.Context, principalID guid.ID) (*time.Time, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	row, err := tables.EmailVerificationTokens.WithContext(ctx).
		Select(tables.EmailVerificationTokens.CreatedAt).
		Where(tables.EmailVerificationTokens.PrincipalID.Eq(principalID)).
		Order(tables.EmailVerificationTokens.CreatedAt.Desc()).Take()
	if err != nil {
		return nil, err
	}
	return &row.CreatedAt, nil
}
