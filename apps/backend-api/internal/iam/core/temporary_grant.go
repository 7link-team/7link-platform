package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

func (r *Repository) GrantTemporaryRole(
	ctx context.Context,
	tenantID, grantID, roleID, principalID, actorID guid.ID,
	startsAt, endsAt time.Time,
) error {
	if r == nil || tenantID.Zero() || grantID.Zero() || roleID.Zero() ||
		principalID.Zero() || actorID.Zero() || !endsAt.After(startsAt) {
		return errors.New("invalid temporary role grant")
	}
	return r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		_, active, err := tenantMembershipState(ctx, db, repo.prefix(), tenantID, principalID)
		if err != nil {
			return err
		}
		if !active {
			return ErrMemberInactive
		}
		row := model.TemporaryRoleGrant{
			TenantID: tenantID, ID: grantID, RoleID: roleID, PrincipalID: principalID,
			SourceType: "access_request", SourceID: grantID,
			StartsAt: startsAt.UTC().Truncate(time.Millisecond), EndsAt: endsAt.UTC().Truncate(time.Millisecond),
			CreatedBy: actorID, CreatedAt: repo.now().UTC().Truncate(time.Millisecond),
		}
		if err := tables.TemporaryRoleGrants.WithContext(ctx).Create(&row); err != nil {
			return fmt.Errorf("insert temporary role grant: %w", err)
		}
		return nil
	})
}

func (r *Repository) RevokeTemporaryRole(ctx context.Context, tenantID, grantID guid.ID) (bool, error) {
	if r == nil || tenantID.Zero() || grantID.Zero() {
		return false, errors.New("invalid temporary role grant")
	}
	tables, _, err := r.tables()
	if err != nil {
		return false, err
	}
	info, err := tables.TemporaryRoleGrants.WithContext(ctx).
		Where(
			tables.TemporaryRoleGrants.TenantID.Eq(tenantID),
			tables.TemporaryRoleGrants.ID.Eq(grantID),
		).Delete()
	if err != nil {
		return false, fmt.Errorf("delete temporary role grant: %w", err)
	}
	return info.RowsAffected > 0, nil
}

func (r *Repository) grantTemporaryRoleOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, grantID, roleID, principalID, actorID guid.ID,
	startsAt, endsAt time.Time,
) error {
	if db == nil {
		return ErrUnavailable
	}
	return r.withDB(db).GrantTemporaryRole(ctx, tenantID, grantID, roleID, principalID, actorID, startsAt, endsAt)
}

func (r *Repository) revokeTemporaryRoleOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, grantID guid.ID,
) (bool, error) {
	if db == nil {
		return false, ErrUnavailable
	}
	return r.withDB(db).RevokeTemporaryRole(ctx, tenantID, grantID)
}
