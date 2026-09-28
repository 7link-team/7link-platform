package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

const roleScopeDepartmentQueryBatch = 200

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

// GetRoleDataScope returns a tenant role's data scope, defaulting to all when
// no explicit row exists.
func (r *Repository) GetRoleDataScope(ctx context.Context, tenantID, roleID guid.ID) (RoleDataScope, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return RoleDataScope{}, err
	}
	directory, err := repo.directoryTables()
	if err != nil {
		return RoleDataScope{}, err
	}
	if err := repo.ensureRole(ctx, tenantID, roleID); err != nil {
		return RoleDataScope{}, err
	}
	db := repo.writer().WithContext(ctx)
	var row roleDataScopeRecord
	err = db.Table(directory.RoleDataScopes.String()).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoleDataScope{RoleID: roleID, Scope: DataScopeAll, DepartmentIDs: []guid.ID{}}, nil
	}
	if err != nil {
		return RoleDataScope{}, fmt.Errorf("get role data scope: %w", err)
	}
	departmentIDs := make([]guid.ID, 0)
	if err := db.Table(directory.RoleScopeDepartments.String()).
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
	directory, err := repo.directoryTables()
	if err != nil {
		return err
	}
	db := repo.writer().WithContext(ctx)
	if result := db.Table(directory.RoleDataScopes.String()).
		Where("tenant_id = ? AND role_id = ?", tenantID, roleID).
		Delete(&roleDataScopeRecord{}); result.Error != nil {
		return fmt.Errorf("delete role data scope: %w", result.Error)
	}
	if scope == DataScopeAll {
		return nil
	}
	now := repo.now().UTC().Truncate(time.Millisecond)
	row := roleDataScopeRecord{TenantID: tenantID, RoleID: roleID, ScopeType: string(scope), UpdatedAt: now}
	if result := db.Table(directory.RoleDataScopes.String()).Create(&row); result.Error != nil {
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
	if result := db.Table(directory.RoleScopeDepartments.String()).Create(&rows); result.Error != nil {
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
	directory, err := repo.directoryTables()
	if err != nil {
		return err
	}
	db := repo.writer().WithContext(ctx)
	if result := db.Table(directory.MemberDepartments.String()).
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
	if result := db.Table(directory.MemberDepartments.String()).Create(&row); result.Error != nil {
		return fmt.Errorf("set member department: %w", result.Error)
	}
	return nil
}

func (r *Repository) departmentStatuses(
	ctx context.Context,
	tenantID guid.ID,
	departmentIDs []guid.ID,
) (map[guid.ID]string, error) {
	repo, err := r.pinWriter()
	if err != nil {
		return nil, err
	}
	directory, err := repo.directoryTables()
	if err != nil {
		return nil, err
	}
	statuses := make(map[guid.ID]string, len(departmentIDs))
	for start := 0; start < len(departmentIDs); start += roleScopeDepartmentQueryBatch {
		end := min(start+roleScopeDepartmentQueryBatch, len(departmentIDs))
		rows := make([]struct {
			ID     guid.ID `gorm:"column:id"`
			Status string  `gorm:"column:status"`
		}, 0, end-start)
		if err := repo.writer().WithContext(ctx).Table(directory.Departments.String()).
			Select("id, status").Where("tenant_id = ? AND id IN ?", tenantID, departmentIDs[start:end]).
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("list department statuses: %w", err)
		}
		for _, row := range rows {
			statuses[row.ID] = row.Status
		}
	}
	return statuses, nil
}

func (r *Repository) departmentStatus(
	ctx context.Context,
	tenantID, departmentID guid.ID,
) (string, error) {
	statuses, err := r.departmentStatuses(ctx, tenantID, []guid.ID{departmentID})
	if err != nil {
		return "", err
	}
	return statuses[departmentID], nil
}

// GetRoleDataScope returns the configured data scope for a tenant role.
func (s *Service) GetRoleDataScope(ctx context.Context, tenantID, roleID guid.ID) (RoleDataScope, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return RoleDataScope{}, err
	}
	return s.repo.GetRoleDataScope(ctx, tenantID, roleID)
}

// SetRoleDataScope atomically replaces a role's scope and selected department
// set after tenant-qualified validation.
func (s *Service) SetRoleDataScope(
	ctx context.Context,
	tenantID, roleID guid.ID,
	scope DataScope,
	departmentIDs []guid.ID,
) (RoleDataScope, bool, error) {
	if err := validateRoleRef(tenantID, roleID); err != nil {
		return RoleDataScope{}, false, err
	}
	departmentIDs, err := normalizeRoleDataScope(scope, departmentIDs)
	if err != nil {
		return RoleDataScope{}, false, err
	}
	var result RoleDataScope
	var changed bool
	record := newAuditRecord(ctx, tenantID, "role_data_scope_updated", "role", roleID)
	record.Detail["scope"] = scope
	record.Detail["department_count"] = len(departmentIDs)
	err = s.writes.Run(ctx, record, func(repo *Repository) error {
		codes, err := s.requireGrantableRolePermissions(ctx, repo, tenantID, roleID)
		if err != nil {
			return err
		}
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		current, err := repo.GetRoleDataScope(ctx, tenantID, roleID)
		if err != nil {
			return err
		}
		if current.Scope == scope && slices.Equal(current.DepartmentIDs, departmentIDs) {
			result = current
			record.SkipAudit = true
			return nil
		}
		statuses, err := repo.departmentStatuses(ctx, tenantID, departmentIDs)
		if err != nil {
			return err
		}
		for _, departmentID := range departmentIDs {
			status := statuses[departmentID]
			if status == "" {
				return ErrRoleScopeDepartmentMissing
			}
			if status != "active" {
				return ErrRoleScopeDepartmentInactive
			}
		}
		proposed := RoleDataScope{RoleID: roleID, Scope: scope, DepartmentIDs: departmentIDs}
		if err := s.requireGrantableDataScope(ctx, repo, tenantID, roleID, &proposed, codes); err != nil {
			return err
		}
		if err := repo.replaceRoleDataScope(ctx, tenantID, roleID, scope, departmentIDs); err != nil {
			return err
		}
		changed, record.PolicyChanged = true, true
		result, err = repo.GetRoleDataScope(ctx, tenantID, roleID)
		return err
	})
	return result, changed, err
}

func normalizeRoleDataScope(scope DataScope, departmentIDs []guid.ID) ([]guid.ID, error) {
	switch scope {
	case DataScopeAll, DataScopeSelf, DataScopeDepartment,
		DataScopeDepartmentAndDescendants, DataScopeSelectedDepartments:
	default:
		return nil, ErrInvalidRoleDataScope
	}
	set := make(map[guid.ID]struct{}, len(departmentIDs))
	for _, id := range departmentIDs {
		if id.Zero() {
			return nil, ErrInvalidRoleDataScope
		}
		set[id] = struct{}{}
	}
	normalized := make([]guid.ID, 0, len(set))
	for id := range set {
		normalized = append(normalized, id)
	}
	slices.Sort(normalized)
	if scope == DataScopeSelectedDepartments && len(normalized) == 0 {
		return nil, ErrRoleScopeDepartmentNeeded
	}
	if scope != DataScopeSelectedDepartments && len(normalized) > 0 {
		return nil, ErrRoleScopeDepartmentsExtra
	}
	return normalized, nil
}
