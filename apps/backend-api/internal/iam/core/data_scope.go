package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type roleDataScopeRecord struct {
	TenantID  guid.ID   `gorm:"column:tenant_id"`
	RoleID    guid.ID   `gorm:"column:role_id"`
	ScopeType string    `gorm:"column:scope_type"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

type roleScopeDepartmentRecord struct {
	TenantID     guid.ID   `gorm:"column:tenant_id"`
	RoleID       guid.ID   `gorm:"column:role_id"`
	DepartmentID guid.ID   `gorm:"column:department_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

type memberDepartmentRecord struct {
	TenantID     guid.ID   `gorm:"column:tenant_id"`
	PrincipalID  guid.ID   `gorm:"column:principal_id"`
	DepartmentID guid.ID   `gorm:"column:department_id"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (r *Repository) GetRoleDataScope(ctx context.Context, tenantID, roleID guid.ID) (RoleDataScope, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return RoleDataScope{}, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return RoleDataScope{}, err
	}
	db := repo.writer().WithContext(ctx)
	var row roleDataScopeRecord
	err = db.Table(repo.table("iam_role_data_scopes")).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoleDataScope{RoleID: roleID, Scope: DataScopeAll, DepartmentIDs: []guid.ID{}}, nil
	}
	if err != nil {
		return RoleDataScope{}, fmt.Errorf("get role data scope: %w", err)
	}
	departmentIDs := make([]guid.ID, 0)
	if err := db.Table(repo.table("iam_role_scope_departments")).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Order("department_id ASC").Pluck("department_id", &departmentIDs).Error; err != nil {
		return RoleDataScope{}, fmt.Errorf("list role scope departments: %w", err)
	}
	return RoleDataScope{
		RoleID: roleID, Scope: DataScope(row.ScopeType), DepartmentIDs: departmentIDs,
		UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (r *Repository) replaceRoleDataScope(
	ctx context.Context,
	tenantID, roleID guid.ID,
	scope DataScope,
	departmentIDs []guid.ID,
) error {
	repo, err := r.pinWriter()
	if err != nil {
		return err
	}
	db := repo.writer().WithContext(ctx)
	if result := db.Table(repo.table("iam_role_data_scopes")).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Delete(&roleDataScopeRecord{}); result.Error != nil {
		return fmt.Errorf("delete role data scope: %w", result.Error)
	}
	if scope == DataScopeAll {
		return nil
	}
	now := repo.now().UTC().Truncate(time.Millisecond)
	row := roleDataScopeRecord{TenantID: tenantID, RoleID: roleID, ScopeType: string(scope), UpdatedAt: now}
	if result := db.Table(repo.table("iam_role_data_scopes")).Create(&row); result.Error != nil {
		return fmt.Errorf("insert role data scope: %w", result.Error)
	}
	if len(departmentIDs) == 0 {
		return nil
	}
	rows := make([]roleScopeDepartmentRecord, len(departmentIDs))
	for index, departmentID := range departmentIDs {
		rows[index] = roleScopeDepartmentRecord{
			TenantID: tenantID, RoleID: roleID, DepartmentID: departmentID, CreatedAt: now,
		}
	}
	if result := db.Table(repo.table("iam_role_scope_departments")).Create(&rows); result.Error != nil {
		return fmt.Errorf("insert role scope departments: %w", result.Error)
	}
	return nil
}

func (r *Repository) setMemberDepartment(
	ctx context.Context,
	tenantID, principalID, departmentID guid.ID,
) error {
	repo, err := r.pinWriter()
	if err != nil {
		return err
	}
	db := repo.writer().WithContext(ctx)
	if result := db.Table(repo.table("iam_member_departments")).
		Where("tenant_id = ? AND principal_id = ?", tenantID, principalID).
		Delete(&memberDepartmentRecord{}); result.Error != nil {
		return fmt.Errorf("clear member department: %w", result.Error)
	}
	if departmentID.Zero() {
		return nil
	}
	row := memberDepartmentRecord{
		TenantID: tenantID, PrincipalID: principalID, DepartmentID: departmentID,
		UpdatedAt: repo.now().UTC().Truncate(time.Millisecond),
	}
	if result := db.Table(repo.table("iam_member_departments")).Create(&row); result.Error != nil {
		return fmt.Errorf("set member department: %w", result.Error)
	}
	return nil
}
