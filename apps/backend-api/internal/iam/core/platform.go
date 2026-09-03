package core

import (
	"context"
	"fmt"
	"sort"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func validatePlatformPrincipal(principalID guid.ID) (guid.ID, error) {
	if principalID.Zero() {
		return 0, fmt.Errorf("%w: invalid platform administrator principal", ErrInvalidArgument)
	}
	return principalID, nil
}

func (r *Repository) ListPlatformAdministrators(ctx context.Context) ([]PlatformAdministrator, error) {
	tables, _, err := r.tables()
	if err != nil {
		return nil, err
	}
	administratorRows, err := tables.PlatformAdministrators.WithContext(ctx).
		Order(tables.PlatformAdministrators.PrincipalID).Find()
	if err != nil {
		return nil, fmt.Errorf("list platform administrators: %w", err)
	}
	grantRows, err := tables.PlatformGrants.WithContext(ctx).
		Order(tables.PlatformGrants.PrincipalID, tables.PlatformGrants.PermissionCode).Find()
	if err != nil {
		return nil, fmt.Errorf("list platform grants: %w", err)
	}

	byPrincipal := make(map[guid.ID]*PlatformAdministrator, len(administratorRows)+len(grantRows))
	for _, row := range administratorRows {
		byPrincipal[row.PrincipalID] = &PlatformAdministrator{
			PrincipalID: row.PrincipalID, FullAdministrator: true,
			Permissions: []string{}, CreatedAt: row.CreatedAt.UTC(),
		}
	}
	for _, row := range grantRows {
		administrator := byPrincipal[row.PrincipalID]
		if administrator == nil {
			administrator = &PlatformAdministrator{
				PrincipalID: row.PrincipalID, Permissions: []string{}, CreatedAt: row.CreatedAt.UTC(),
			}
			byPrincipal[row.PrincipalID] = administrator
		}
		administrator.Permissions = append(administrator.Permissions, row.PermissionCode)
	}

	principalIDs := make([]guid.ID, 0, len(byPrincipal))
	for principalID := range byPrincipal {
		principalIDs = append(principalIDs, principalID)
	}
	sort.Slice(principalIDs, func(left, right int) bool { return principalIDs[left] < principalIDs[right] })
	result := make([]PlatformAdministrator, len(principalIDs))
	for index, principalID := range principalIDs {
		result[index] = *byPrincipal[principalID]
	}
	return result, nil
}

func (r *Repository) GetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	administrators, err := r.ListPlatformAdministrators(ctx)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	for _, administrator := range administrators {
		if administrator.PrincipalID == principalID {
			return administrator, nil
		}
	}
	return PlatformAdministrator{}, ErrPlatformAdministratorNotFound
}

func (r *Repository) SetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
	full bool,
	permissions []string,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	var administrator PlatformAdministrator
	err = r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		if !full {
			if err := repo.lockPlatformAdministratorMutations(ctx); err != nil {
				return err
			}
		}
		if result := db.WithContext(ctx).Table(tables.PlatformGrants.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformGrant{}); result.Error != nil {
			return fmt.Errorf("clear platform grants: %w", result.Error)
		}
		now := repo.now().UTC().Truncate(time.Millisecond)
		if full {
			row := model.PlatformAdministrator{PrincipalID: principalID, CreatedAt: now}
			result := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
				Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return fmt.Errorf("grant platform administrator: %w", result.Error)
			}
			administrator, err = repo.GetPlatformAdministrator(ctx, principalID)
			return err
		}
		result := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformAdministrator{})
		if result.Error != nil {
			return fmt.Errorf("revoke platform administrator: %w", result.Error)
		}
		if len(permissions) == 0 {
			return ErrPlatformAdministratorNotFound
		}
		rows := make([]model.PlatformGrant, len(permissions))
		for index, code := range permissions {
			rows[index] = model.PlatformGrant{PrincipalID: principalID, PermissionCode: code, CreatedAt: now}
		}
		if result := db.WithContext(ctx).Table(tables.PlatformGrants.TableName()).Create(&rows); result.Error != nil {
			return fmt.Errorf("insert platform grants: %w", result.Error)
		}
		if result.RowsAffected > 0 {
			if err := repo.assertPlatformAdministratorRemains(ctx); err != nil {
				return err
			}
		}
		administrator, err = repo.GetPlatformAdministrator(ctx, principalID)
		return err
	})
	return administrator, err
}

func (r *Repository) DeletePlatformAdministrator(ctx context.Context, principalID guid.ID) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	var changed bool
	err = r.runInTx(ctx, func(repo *Repository) error {
		tables, db, err := repo.tables()
		if err != nil {
			return err
		}
		if err := repo.lockPlatformAdministratorMutations(ctx); err != nil {
			return err
		}
		administrator := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformAdministrator{})
		if administrator.Error != nil {
			return fmt.Errorf("revoke platform administrator: %w", administrator.Error)
		}
		grants := db.WithContext(ctx).Table(tables.PlatformGrants.TableName()).
			Where("principal_id = ?", principalID).Delete(&model.PlatformGrant{})
		if grants.Error != nil {
			return fmt.Errorf("revoke platform grants: %w", grants.Error)
		}
		changed = administrator.RowsAffected+grants.RowsAffected > 0
		if administrator.RowsAffected > 0 {
			return repo.assertPlatformAdministratorRemains(ctx)
		}
		return nil
	})
	return changed, err
}

// lockPlatformAdministratorMutations serializes operations that may remove a
// full administrator. Locking every current row makes a later count observe
// the preceding transaction before it can remove another administrator.
func (r *Repository) lockPlatformAdministratorMutations(ctx context.Context) error {
	tables, db, err := r.tables()
	if err != nil {
		return err
	}
	database := db.WithContext(ctx).Table(tables.PlatformAdministrators.TableName())
	if db.Dialector.Name() == "sqlite" {
		result := database.Where("principal_id > ?", 0).
			UpdateColumn("created_at", gorm.Expr("created_at"))
		if result.Error != nil {
			return fmt.Errorf("lock platform administrators: %w", result.Error)
		}
		return nil
	}
	var principalIDs []guid.ID
	if err := database.Select("principal_id").Order("principal_id").
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Scan(&principalIDs).Error; err != nil {
		return fmt.Errorf("lock platform administrators: %w", err)
	}
	return nil
}

func (r *Repository) CountFullPlatformAdministrators(ctx context.Context) (int, error) {
	tables, _, err := r.tables()
	if err != nil {
		return 0, err
	}
	count, err := tables.PlatformAdministrators.WithContext(ctx).Count()
	if err != nil {
		return 0, fmt.Errorf("count platform administrators: %w", err)
	}
	return int(count), nil
}

func (r *Repository) assertPlatformAdministratorRemains(ctx context.Context) error {
	count, err := r.CountFullPlatformAdministrators(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrLastPlatformAdministrator
	}
	return nil
}
