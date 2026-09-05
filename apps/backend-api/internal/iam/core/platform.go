package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const platformAuditTenant guid.ID = 0

type platformAdministratorRow struct {
	PrincipalID    guid.ID   `gorm:"column:principal_id"`
	RecordKind     string    `gorm:"column:record_kind"`
	PermissionCode string    `gorm:"column:permission_code"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

// PlatformPermissionCodes returns declared platform permission codes in stable
// order.
func PlatformPermissionCodes(registry *authz.Registry) []string {
	codes := make([]string, 0, 8)
	if registry == nil {
		return codes
	}
	for _, action := range registry.All() {
		if action.Scope == "platform" {
			codes = append(codes, action.Code)
		}
	}
	sort.Strings(codes)
	return codes
}

func validatePlatformPrincipal(principalID guid.ID) (guid.ID, error) {
	if principalID.Zero() {
		return 0, fmt.Errorf("%w: invalid platform administrator principal", ErrInvalidArgument)
	}
	return principalID, nil
}

// ListPlatformAdministrators returns full and restricted platform
// administrators from one database statement snapshot.
func (r *Repository) ListPlatformAdministrators(ctx context.Context) ([]PlatformAdministrator, error) {
	return r.listPlatformAdministrators(ctx, nil)
}

func (r *Repository) listPlatformAdministrators(
	ctx context.Context,
	principalID *guid.ID,
) ([]PlatformAdministrator, error) {
	tables, db, err := r.tables()
	if err != nil {
		return nil, err
	}
	where, arguments := "", []any{}
	if principalID != nil {
		where = " WHERE principal_id = ?"
		arguments = append(arguments, *principalID, *principalID)
	}
	rows := make([]platformAdministratorRow, 0)
	err = db.WithContext(ctx).Raw(
		"SELECT principal_id, 'full' AS record_kind, '' AS permission_code, created_at FROM "+
			tables.PlatformAdministrators.TableName()+where+
			" UNION ALL SELECT principal_id, 'grant' AS record_kind, permission_code, created_at FROM "+
			tables.PlatformGrants.TableName()+where+
			" ORDER BY principal_id ASC, record_kind DESC, permission_code ASC",
		arguments...,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list platform administrators: %w", err)
	}

	result := make([]PlatformAdministrator, 0, len(rows))
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].PrincipalID != row.PrincipalID {
			result = append(result, PlatformAdministrator{
				PrincipalID: row.PrincipalID, Permissions: []string{}, CreatedAt: row.CreatedAt.UTC(),
			})
		}
		administrator := &result[len(result)-1]
		if row.RecordKind == "full" {
			administrator.FullAdministrator = true
			administrator.CreatedAt = row.CreatedAt.UTC()
		} else {
			administrator.Permissions = append(administrator.Permissions, row.PermissionCode)
		}
	}
	return result, nil
}

// GetPlatformAdministrator returns one full or restricted platform
// administrator.
func (r *Repository) GetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	administrators, err := r.listPlatformAdministrators(ctx, &principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	if len(administrators) == 1 {
		return administrators[0], nil
	}
	return PlatformAdministrator{}, ErrPlatformAdministratorNotFound
}

// SetPlatformAdministrator atomically replaces one principal's full or
// restricted platform grants.
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

// DeletePlatformAdministrator revokes all platform access while preserving at
// least one full administrator.
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

// CountFullPlatformAdministrators returns the durable full-administrator
// count.
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

// PlatformPermissionCatalog returns all declared platform permissions in
// stable code order.
func (s *Service) PlatformPermissionCatalog(context.Context) []authz.Action {
	actions := make([]authz.Action, 0, 8)
	for _, action := range s.registry.All() {
		if action.Scope == "platform" {
			actions = append(actions, action)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Code < actions[j].Code })
	return actions
}

// ListPlatformAdministrators returns every full and restricted platform
// administrator.
func (s *Service) ListPlatformAdministrators(ctx context.Context) ([]PlatformAdministrator, error) {
	return s.repo.ListPlatformAdministrators(ctx)
}

// SetPlatformAdministrator validates and atomically replaces a principal's
// platform access.
func (s *Service) SetPlatformAdministrator(
	ctx context.Context,
	principalID guid.ID,
	full bool,
	permissions []string,
) (PlatformAdministrator, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	normalized, err := s.normalizePlatformPermissions(full, permissions)
	if err != nil {
		return PlatformAdministrator{}, err
	}
	record := newAuditRecord(ctx, platformAuditTenant, "platform_administrator_updated", "principal", principalID)
	record.PolicyChanged = true
	record.Detail["full_administrator"] = full
	record.Detail["permissions"] = normalized
	var administrator PlatformAdministrator
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		administrator, err = repo.SetPlatformAdministrator(ctx, principalID, full, normalized)
		return err
	})
	return administrator, err
}

// DeletePlatformAdministrator revokes a principal's platform access while
// preserving the last full administrator.
func (s *Service) DeletePlatformAdministrator(ctx context.Context, principalID guid.ID) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	record := newAuditRecord(ctx, platformAuditTenant, "platform_administrator_revoked", "principal", principalID)
	record.PolicyChanged = true
	var changed bool
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		changed, err = repo.DeletePlatformAdministrator(ctx, principalID)
		if err != nil {
			return err
		}
		record.Detail["changed"] = changed
		if !changed {
			return ErrPlatformAdministratorNotFound
		}
		return nil
	})
	return changed, err
}

func (s *Service) normalizePlatformPermissions(full bool, permissions []string) ([]string, error) {
	if full {
		if len(permissions) > 0 {
			return nil, fmt.Errorf("%w: a full platform administrator does not accept explicit permissions", ErrInvalidArgument)
		}
		return []string{}, nil
	}
	if len(permissions) == 0 {
		return nil, fmt.Errorf("%w: a restricted platform administrator requires at least one permission", ErrInvalidArgument)
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(permissions))
	for _, code := range permissions {
		code = strings.TrimSpace(code)
		action, ok := s.registry.Find(code)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrPermissionNotFound, code)
		}
		if action.Scope != "platform" {
			return nil, fmt.Errorf("%w: %s", ErrPlatformPermissionScope, code)
		}
		if !seen[code] {
			seen[code] = true
			normalized = append(normalized, code)
		}
	}
	sort.Strings(normalized)
	return normalized, nil
}

// ListPlatformAdministratorsOn reads full and restricted platform
// administrators through the caller's transaction. The bootstrap command needs
// the check and the following grant to observe one snapshot, so it cannot use
// the connection-picking read above.
func (r *Repository) ListPlatformAdministratorsOn(
	ctx context.Context,
	db *gorm.DB,
) ([]PlatformAdministrator, error) {
	if r == nil || db == nil {
		return nil, ErrUnavailable
	}
	return r.withDB(db).listPlatformAdministrators(ctx, nil)
}

// GrantFullPlatformAdministratorOn inserts one full administrator row through
// the caller's transaction and reports whether it created it. Reporting rather
// than failing on a duplicate is what keeps bootstrap create-only: a rerun
// says "already bootstrapped" instead of rewriting the row.
func (r *Repository) GrantFullPlatformAdministratorOn(
	ctx context.Context,
	db *gorm.DB,
	principalID guid.ID,
) (bool, error) {
	principalID, err := validatePlatformPrincipal(principalID)
	if err != nil {
		return false, err
	}
	if r == nil || db == nil {
		return false, ErrUnavailable
	}
	repo := r.withDB(db)
	tables, database, err := repo.tables()
	if err != nil {
		return false, err
	}
	row := model.PlatformAdministrator{
		PrincipalID: principalID,
		CreatedAt:   repo.now().UTC().Truncate(time.Millisecond),
	}
	result := database.WithContext(ctx).Table(tables.PlatformAdministrators.TableName()).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, fmt.Errorf("grant platform administrator: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}
