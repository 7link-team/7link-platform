package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type groupRoleBindingRecord struct {
	TenantID  guid.ID   `gorm:"column:tenant_id"`
	RoleID    guid.ID   `gorm:"column:role_id"`
	GroupID   guid.ID   `gorm:"column:group_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

type positionRoleBindingRecord struct {
	TenantID   guid.ID   `gorm:"column:tenant_id"`
	RoleID     guid.ID   `gorm:"column:role_id"`
	PositionID guid.ID   `gorm:"column:position_id"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (r *Repository) ListDirectoryBindings(
	ctx context.Context,
	tenantID, roleID guid.ID,
) ([]RoleDirectoryBinding, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return nil, err
	}
	db := repo.writer().WithContext(ctx)
	var groups []groupRoleBindingRecord
	if err := db.Table(repo.table("iam_group_role_bindings")).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list group role bindings: %w", err)
	}
	bindings := make([]RoleDirectoryBinding, 0, len(groups))
	for _, row := range groups {
		bindings = append(bindings, RoleDirectoryBinding{
			RoleID: row.RoleID, AssigneeType: DirectoryAssigneeGroup,
			AssigneeID: row.GroupID, CreatedAt: row.CreatedAt.UTC(),
		})
	}
	var positions []positionRoleBindingRecord
	if err := db.Table(repo.table("iam_position_role_bindings")).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Find(&positions).Error; err != nil {
		return nil, fmt.Errorf("list position role bindings: %w", err)
	}
	for _, row := range positions {
		bindings = append(bindings, RoleDirectoryBinding{
			RoleID: row.RoleID, AssigneeType: DirectoryAssigneePosition,
			AssigneeID: row.PositionID, CreatedAt: row.CreatedAt.UTC(),
		})
	}
	sort.Slice(bindings, func(left, right int) bool {
		if bindings[left].AssigneeType == bindings[right].AssigneeType {
			return bindings[left].AssigneeID < bindings[right].AssigneeID
		}
		return bindings[left].AssigneeType < bindings[right].AssigneeType
	})
	return bindings, nil
}

func (r *Repository) ChangeDirectoryBinding(
	ctx context.Context,
	tenantID, roleID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
	add bool,
) (bool, error) {
	var changed bool
	err := r.runInTx(ctx, func(repo *Repository) error {
		if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
			return err
		}
		status, err := repo.directoryAssigneeStatus(ctx, tenantID, assigneeType, assigneeID)
		if err != nil {
			return err
		}
		if add && status != "active" {
			return ErrDirectoryAssigneeInactive
		}
		db := repo.writer().WithContext(ctx)
		now := repo.now().UTC().Truncate(time.Millisecond)
		switch assigneeType {
		case DirectoryAssigneeGroup:
			if add {
				row := groupRoleBindingRecord{TenantID: tenantID, RoleID: roleID, GroupID: assigneeID, CreatedAt: now}
				result := db.Table(repo.table("iam_group_role_bindings")).
					Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
				if result.Error != nil {
					return fmt.Errorf("insert group role binding: %w", result.Error)
				}
				changed = result.RowsAffected > 0
				return nil
			}
			result := db.Table(repo.table("iam_group_role_bindings")).
				Where("tenant_id = ? AND role_id = ? AND group_id = ?", tenantID, roleID, assigneeID).
				Delete(&groupRoleBindingRecord{})
			if result.Error != nil {
				return fmt.Errorf("delete group role binding: %w", result.Error)
			}
			changed = result.RowsAffected > 0
		case DirectoryAssigneePosition:
			if add {
				row := positionRoleBindingRecord{TenantID: tenantID, RoleID: roleID, PositionID: assigneeID, CreatedAt: now}
				result := db.Table(repo.table("iam_position_role_bindings")).
					Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
				if result.Error != nil {
					return fmt.Errorf("insert position role binding: %w", result.Error)
				}
				changed = result.RowsAffected > 0
				return nil
			}
			result := db.Table(repo.table("iam_position_role_bindings")).
				Where("tenant_id = ? AND role_id = ? AND position_id = ?", tenantID, roleID, assigneeID).
				Delete(&positionRoleBindingRecord{})
			if result.Error != nil {
				return fmt.Errorf("delete position role binding: %w", result.Error)
			}
			changed = result.RowsAffected > 0
		default:
			return fmt.Errorf("%w: invalid directory assignee type", ErrInvalidArgument)
		}
		return nil
	})
	return changed, err
}

func (r *Repository) directoryAssigneeStatus(
	ctx context.Context,
	tenantID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
) (string, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return "", err
	}
	table := ""
	switch assigneeType {
	case DirectoryAssigneeGroup:
		table = "iam_groups"
	case DirectoryAssigneePosition:
		table = "iam_positions"
	default:
		return "", fmt.Errorf("%w: invalid directory assignee type", ErrInvalidArgument)
	}
	var row struct {
		Status string `gorm:"column:status"`
	}
	err = repo.writer().WithContext(ctx).Table(repo.table(table)).
		Select("status").Where("tenant_id = ? AND id = ?", tenantID, assigneeID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrDirectoryAssigneeNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get directory assignee: %w", err)
	}
	return row.Status, nil
}
