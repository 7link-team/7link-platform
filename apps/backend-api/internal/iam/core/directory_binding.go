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

// ListDirectoryBindings returns group and position role assignments for one
// tenant role.
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

// ChangeDirectoryBinding idempotently adds or removes one tenant-qualified
// group or position role assignment.
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

// ListDirectoryBindings returns directory assignees bound to one tenant role.
func (s *Service) ListDirectoryBindings(
	ctx context.Context,
	tenantID, roleID guid.ID,
) ([]RoleDirectoryBinding, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return nil, err
	}
	return s.repo.ListDirectoryBindings(ctx, tenantID, roleID)
}

// AddDirectoryBinding grants a role to an active group or position after the
// authenticated actor's anti-escalation check.
func (s *Service) AddDirectoryBinding(
	ctx context.Context,
	tenantID, roleID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
) (bool, error) {
	return s.changeDirectoryBinding(ctx, tenantID, roleID, assigneeType, assigneeID, true)
}

// RemoveDirectoryBinding revokes a group or position role assignment while
// preserving the last durable tenant administrator.
func (s *Service) RemoveDirectoryBinding(
	ctx context.Context,
	tenantID, roleID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
) (bool, error) {
	return s.changeDirectoryBinding(ctx, tenantID, roleID, assigneeType, assigneeID, false)
}

func (s *Service) changeDirectoryBinding(
	ctx context.Context,
	tenantID, roleID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
	add bool,
) (bool, error) {
	if err := validateDirectoryBindingRef(tenantID, roleID, assigneeType, assigneeID); err != nil {
		return false, err
	}
	if add {
		if err := s.requireGrantableRole(ctx, tenantID, roleID); err != nil {
			return false, err
		}
	}
	eventType := "role_directory_binding_removed"
	if add {
		eventType = "role_directory_binding_added"
	}
	record := newAuditRecord(ctx, tenantID, eventType, "role", roleID)
	record.Detail["assignee_type"] = assigneeType
	record.Detail["assignee_id"] = assigneeID
	var changed bool
	err := s.writes.Run(ctx, record, func(repo *Repository) error {
		var verify func() error
		var err error
		if !add {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		changed, err = repo.ChangeDirectoryBinding(
			ctx, tenantID, roleID, assigneeType, assigneeID, add,
		)
		if err == nil && verify != nil {
			err = verify()
		}
		record.PolicyChanged = changed
		record.SkipAudit = !changed
		return err
	})
	return changed, err
}

func validateDirectoryBindingRef(
	tenantID, roleID guid.ID,
	assigneeType DirectoryAssigneeType,
	assigneeID guid.ID,
) error {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return err
	}
	if assigneeType != DirectoryAssigneeGroup && assigneeType != DirectoryAssigneePosition {
		return fmt.Errorf("%w: invalid directory assignee type", ErrInvalidArgument)
	}
	if assigneeID.Zero() {
		return fmt.Errorf("%w: invalid directory assignee id", ErrInvalidArgument)
	}
	return nil
}
