package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type positionRow = model.Position

type positionMemberRow struct {
	TenantID    guid.ID    `gorm:"column:tenant_id"`
	PositionID  guid.ID    `gorm:"column:position_id"`
	PrincipalID guid.ID    `gorm:"column:principal_id"`
	StartsAt    *time.Time `gorm:"column:starts_at"`
	EndsAt      *time.Time `gorm:"column:ends_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	DisplayName string     `gorm:"column:display_name"`
	Email       string     `gorm:"column:email"`
}

func (r *Repository) listPositions(ctx context.Context, tenantID guid.ID) ([]positionRow, error) {
	var rows []positionRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Position{}.TableName())).Where("tenant_id = ?", tenantID).
		Order("sort_order ASC, code ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	return rows, nil
}

func (r *Repository) getPosition(ctx context.Context, tenantID, id guid.ID) (*positionRow, error) {
	var row positionRow
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPositionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get position: %w", err)
	}
	return &row, nil
}

func (r *Repository) insertPosition(ctx context.Context, row *positionRow) error {
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Create(row).Error
	if isPositionCodeViolation(err) {
		return ErrPositionCodeConflict
	}
	if err != nil {
		return fmt.Errorf("insert position: %w", err)
	}
	return nil
}

func (r *Repository) updatePosition(ctx context.Context, row *positionRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, expectedVersion).
		Select("code", "name", "status", "sort_order", "version", "updated_at").Updates(row)
	if isPositionCodeViolation(result.Error) {
		return ErrPositionCodeConflict
	}
	if result.Error != nil {
		return fmt.Errorf("update position: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrPositionVersionConflict
	}
	return nil
}

func (r *Repository) deletePosition(ctx context.Context, tenantID, id guid.ID, version int64) error {
	db := r.writer().WithContext(ctx)
	checks := []struct {
		table, column string
		failure       error
	}{
		{model.PositionMember{}.TableName(), "position_id", ErrPositionHasMembers},
		{model.PositionRoleBinding{}.TableName(), "position_id", ErrPositionRoleBound},
	}
	for _, check := range checks {
		var count int64
		if err := db.Table(r.table(check.table)).Where("tenant_id = ? AND "+check.column+" = ?", tenantID, id).Count(&count).Error; err != nil {
			return fmt.Errorf("check position references in %s: %w", check.table, err)
		}
		if count > 0 {
			return check.failure
		}
	}
	count, err := r.countRelationshipSubject(ctx, tenantID, "position", id)
	if err != nil {
		return fmt.Errorf("count position relationships: %w", err)
	}
	if count > 0 {
		return ErrPositionRelationshipBound
	}
	result := db.Table(r.table(model.Position{}.TableName())).Where("tenant_id = ? AND id = ? AND version = ?", tenantID, id, version).Delete(&positionRow{})
	if result.Error != nil {
		return fmt.Errorf("delete position: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrPositionVersionConflict
	}
	return nil
}

func (r *Repository) listPositionMembers(ctx context.Context, tenantID, positionID guid.ID) ([]positionMemberRow, error) {
	tm := r.foreign.access.TenantMembers.String()
	var rows []positionMemberRow
	pm := r.table(model.PositionMember{}.TableName())
	err := r.writer().WithContext(ctx).Table(pm+" AS pm").Select("pm.*, tm.display_name, tm.email").
		Joins("JOIN "+tm+" AS tm ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id").
		Where("pm.tenant_id = ? AND pm.position_id = ?", tenantID, positionID).Order("tm.display_name ASC, pm.principal_id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list position members: %w", err)
	}
	return rows, nil
}

func (r *Repository) getPositionMember(ctx context.Context, tenantID, positionID, principalID guid.ID) (*positionMemberRow, error) {
	tm := r.foreign.access.TenantMembers.String()
	var row positionMemberRow
	pm := r.table(model.PositionMember{}.TableName())
	err := r.writer().WithContext(ctx).Table(pm+" AS pm").Select("pm.*, tm.display_name, tm.email").
		Joins("JOIN "+tm+" AS tm ON tm.tenant_id = pm.tenant_id AND tm.principal_id = pm.principal_id").
		Where("pm.tenant_id = ? AND pm.position_id = ? AND pm.principal_id = ?", tenantID, positionID, principalID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPositionMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get position member: %w", err)
	}
	return &row, nil
}

func (r *Repository) putPositionMember(ctx context.Context, row *model.PositionMember) error {
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "position_id"}, {Name: "principal_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"starts_at", "ends_at", "updated_at"}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("upsert position member: %w", err)
	}
	return nil
}

func (r *Repository) deletePositionMember(ctx context.Context, tenantID, positionID, principalID guid.ID) (bool, error) {
	result := r.writer().WithContext(ctx).Table(r.table(model.PositionMember{}.TableName())).Where("tenant_id = ? AND position_id = ? AND principal_id = ?", tenantID, positionID, principalID).Delete(&model.PositionMember{})
	if result.Error != nil {
		return false, fmt.Errorf("delete position member: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func isPositionCodeViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "positions_code") || strings.Contains(message, "tenant_id") && strings.Contains(message, "code")
}
