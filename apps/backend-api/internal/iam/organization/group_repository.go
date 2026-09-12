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

type groupRow = model.Group

type groupMemberRow struct {
	TenantID     guid.ID    `gorm:"column:tenant_id"`
	GroupID      guid.ID    `gorm:"column:group_id"`
	PrincipalID  guid.ID    `gorm:"column:principal_id"`
	StartsAt     *time.Time `gorm:"column:starts_at"`
	EndsAt       *time.Time `gorm:"column:ends_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	DisplayName  string     `gorm:"column:display_name"`
	Email        string     `gorm:"column:email"`
	DepartmentID *guid.ID   `gorm:"column:department_id"`
	Status       string     `gorm:"column:status"`
}

func (r *Repository) listGroups(ctx context.Context, tenantID guid.ID) ([]groupRow, error) {
	var rows []groupRow
	err := r.writer().WithContext(ctx).Table(r.table(model.Group{}.TableName())).Where("tenant_id = ?", tenantID).
		Order("sort_order ASC, name_key ASC, id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return rows, nil
}

func (r *Repository) getGroup(ctx context.Context, tenantID, id guid.ID) (*groupRow, error) {
	var row groupRow
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}
	return &row, nil
}

func (r *Repository) insertGroup(ctx context.Context, row *groupRow) error {
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Create(row).Error
	if isGroupNameViolation(err) {
		return ErrGroupNameConflict
	}
	if err != nil {
		return fmt.Errorf("insert group: %w", err)
	}
	return nil
}

func (r *Repository) updateGroup(ctx context.Context, row *groupRow, expectedVersion int64) error {
	result := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Where("tenant_id = ? AND id = ? AND version = ?", row.TenantID, row.ID, expectedVersion).
		Select("name", "name_key", "rule_json", "description", "status", "sort_order", "version", "updated_at").Updates(row)
	if isGroupNameViolation(result.Error) {
		return ErrGroupNameConflict
	}
	if result.Error != nil {
		return fmt.Errorf("update group: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrGroupVersionConflict
	}
	return nil
}

func (r *Repository) deleteGroup(ctx context.Context, tenantID, id guid.ID, version int64) error {
	db := r.writer().WithContext(ctx)
	checks := []struct {
		table, column string
		failure       error
	}{
		{model.GroupMember{}.TableName(), "group_id", ErrGroupHasMembers},
		{model.GroupRoleBinding{}.TableName(), "group_id", ErrGroupRoleBound},
	}
	for _, check := range checks {
		var count int64
		if err := db.Table(r.table(check.table)).Where("tenant_id = ? AND "+check.column+" = ?", tenantID, id).Count(&count).Error; err != nil {
			return fmt.Errorf("check group references in %s: %w", check.table, err)
		}
		if count > 0 {
			return check.failure
		}
	}
	count, err := r.countRelationshipSubject(ctx, tenantID, "group", id)
	if err != nil {
		return fmt.Errorf("count group relationships: %w", err)
	}
	if count > 0 {
		return ErrGroupRelationshipBound
	}
	result := db.Table(r.table(model.Group{}.TableName())).Where("tenant_id = ? AND id = ? AND version = ?", tenantID, id, version).Delete(&groupRow{})
	if result.Error != nil {
		return fmt.Errorf("delete group: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrGroupVersionConflict
	}
	return nil
}

func (r *Repository) listDynamicGroupCandidates(ctx context.Context, tenantID, groupID guid.ID) ([]groupMemberRow, error) {
	tm, err := r.foreign.accessTable("iam_tenant_members")
	if err != nil {
		return nil, err
	}
	var rows []groupMemberRow
	md := r.table(model.MemberDepartment{}.TableName())
	err = r.writer().WithContext(ctx).Table(tm+" AS tm").Select("? AS group_id, tm.principal_id, tm.display_name, tm.email, md.department_id, tm.status, tm.created_at, tm.updated_at", groupID).
		Joins("LEFT JOIN "+md+" AS md ON md.tenant_id = tm.tenant_id AND md.principal_id = tm.principal_id").
		Where("tm.tenant_id = ? AND tm.status = ?", tenantID, StatusActive).Order("tm.display_name ASC, tm.principal_id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list dynamic group member candidates: %w", err)
	}
	return rows, nil
}

func (r *Repository) listGroupMembers(ctx context.Context, tenantID, groupID guid.ID) ([]groupMemberRow, error) {
	tm, err := r.foreign.accessTable("iam_tenant_members")
	if err != nil {
		return nil, err
	}
	var rows []groupMemberRow
	gm := r.table(model.GroupMember{}.TableName())
	err = r.writer().WithContext(ctx).Table(gm+" AS gm").Select("gm.*, tm.display_name, tm.email").
		Joins("JOIN "+tm+" AS tm ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id").
		Where("gm.tenant_id = ? AND gm.group_id = ?", tenantID, groupID).Order("tm.display_name ASC, gm.principal_id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list group members: %w", err)
	}
	return rows, nil
}

func (r *Repository) getGroupMember(ctx context.Context, tenantID, groupID, principalID guid.ID) (*groupMemberRow, error) {
	tm, err := r.foreign.accessTable("iam_tenant_members")
	if err != nil {
		return nil, err
	}
	var row groupMemberRow
	gm := r.table(model.GroupMember{}.TableName())
	err = r.writer().WithContext(ctx).Table(gm+" AS gm").Select("gm.*, tm.display_name, tm.email").
		Joins("JOIN "+tm+" AS tm ON tm.tenant_id = gm.tenant_id AND tm.principal_id = gm.principal_id").
		Where("gm.tenant_id = ? AND gm.group_id = ? AND gm.principal_id = ?", tenantID, groupID, principalID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrGroupMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get group member: %w", err)
	}
	return &row, nil
}

func (r *Repository) putGroupMember(ctx context.Context, row *model.GroupMember) error {
	err := r.writer().WithContext(ctx).Table(r.table(row.TableName())).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "group_id"}, {Name: "principal_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"starts_at", "ends_at", "updated_at"}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("upsert group member: %w", err)
	}
	return nil
}

func (r *Repository) deleteGroupMember(ctx context.Context, tenantID, groupID, principalID guid.ID) (bool, error) {
	result := r.writer().WithContext(ctx).Table(r.table(model.GroupMember{}.TableName())).Where("tenant_id = ? AND group_id = ? AND principal_id = ?", tenantID, groupID, principalID).Delete(&model.GroupMember{})
	if result.Error != nil {
		return false, fmt.Errorf("delete group member: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

func isGroupNameViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "groups_name") || strings.Contains(message, "tenant_id") && strings.Contains(message, "name_key")
}
