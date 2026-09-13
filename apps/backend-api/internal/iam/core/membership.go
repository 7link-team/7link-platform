package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

// MembershipChecker is the fail-closed tenant admission capability used by
// authorization consumers. It owns no mutation methods.
type MembershipChecker struct {
	runtime *appenv.Runtime
	foreign *foreignTables
	repositoryTables
}

func NewMembershipChecker(runtime *appenv.Runtime) *MembershipChecker {
	if runtime == nil {
		panic("iam membership checker requires runtime")
	}
	return &MembershipChecker{runtime: runtime, foreign: &foreignTables{}, repositoryTables: repositoryTables{names: query.BindNames(runtime.TablePrefix())}}
}

// authorizationTables binds the names the membership statements read: core's
// own tables from its deployment prefix, and the tenant and directory tables
// from the modules that own them. An owner that has not published is
// ErrUnavailable, not an empty table name in a statement.
func (m *MembershipChecker) authorizationTables() (query.AuthorizationTables, error) {
	return m.foreign.authorizationTables(m.names)
}

// BindForeignTables records the table-name sets of the tables membership reads
// but does not own. The modules that own them call it through the connectors
// core publishes; a caller that builds the checker outside the module has to
// call it itself, because an unbound checker reports ErrUnavailable rather than
// running a statement against an empty table name.
func (m *MembershipChecker) BindForeignTables(directory DirectoryTableNames, principal PrincipalTableNames) error {
	if m == nil {
		return ErrUnavailable
	}
	if m.foreign == nil {
		m.foreign = &foreignTables{}
	}
	return m.foreign.bind(directory, principal)
}

func (m *MembershipChecker) IsMemberActive(ctx context.Context, tenantID, principalID guid.ID) (bool, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil {
		return false, ErrUnavailable
	}
	db := m.runtime.Database().Read()
	if db == nil {
		return false, ErrUnavailable
	}
	return m.IsMemberActiveOn(ctx, db, tenantID, principalID)
}

// ResolveTenant reads current memberships from the writer, including tenant
// status. An explicit choice that is no longer accessible returns zero.
func (m *MembershipChecker) ResolveTenant(ctx context.Context, principalID, requested guid.ID) (guid.ID, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return 0, ErrUnavailable
	}
	if principalID <= 0 || requested < 0 {
		return 0, ErrInvalidArgument
	}
	names, err := m.authorizationTables()
	if err != nil {
		return 0, err
	}
	db := m.runtime.Database().Write().WithContext(ctx).Table(names.Tables.TenantMembers.String()+" AS tm").
		Joins("JOIN "+names.Directory.Tenants.String()+" AS t ON t.id = tm.tenant_id AND t.status = 'active'").
		Where("tm.principal_id = ? AND tm.status = ?", principalID, MemberActive)
	if requested > 0 {
		db = db.Where("tm.tenant_id = ?", requested)
	}
	ids := []guid.ID{}
	if err := db.Order("tm.tenant_id ASC").Limit(2).Pluck("tm.tenant_id", &ids).Error; err != nil {
		return 0, fmt.Errorf("resolve active member tenant: %w", err)
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return 0, nil
}

// ActiveDepartment supplies server-owned attribution for new business rows.
func (m *MembershipChecker) ActiveDepartment(ctx context.Context, tenantID, principalID guid.ID) (guid.ID, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return 0, ErrUnavailable
	}
	if err := validateMemberRef(tenantID, principalID); err != nil {
		return 0, err
	}
	names, err := m.authorizationTables()
	if err != nil {
		return 0, err
	}
	var row struct{ DepartmentID guid.ID }
	err = m.runtime.Database().Write().WithContext(ctx).Table(names.Directory.MemberDepartments.String()+" AS md").
		Select("md.department_id").
		Joins("JOIN "+names.Directory.Departments.String()+" AS d ON d.tenant_id = md.tenant_id AND d.id = md.department_id AND d.status = 'active'").
		Joins("JOIN "+names.Tables.TenantMembers.String()+" AS tm ON tm.tenant_id = md.tenant_id AND tm.principal_id = md.principal_id AND tm.status = 'active'").
		Joins("JOIN "+names.Directory.Tenants.String()+" AS t ON t.id = md.tenant_id AND t.status = 'active'").
		Where("md.tenant_id = ? AND md.principal_id = ?", tenantID, principalID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get active member department: %w", err)
	}
	return row.DepartmentID, nil
}

func (m *MembershipChecker) IsMemberActiveOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, principalID guid.ID,
) (bool, error) {
	names, err := m.authorizationTables()
	if err != nil {
		return false, err
	}
	_, active, err := tenantMembershipState(ctx, db, names, tenantID, principalID)
	return active, err
}

type tenantAdmission struct {
	Status      string `gorm:"column:status"`
	MemberCount int    `gorm:"column:member_count"`
}

func tenantMembershipState(
	ctx context.Context,
	db *gorm.DB,
	names query.AuthorizationTables,
	tenantID, principalID guid.ID,
) (tenantActive, memberActive bool, err error) {
	if db == nil {
		return false, false, ErrUnavailable
	}
	var state tenantAdmission
	err = db.WithContext(ctx).Table(names.Directory.Tenants.String()+" AS t").
		Select("t.status, COUNT(tm.principal_id) AS member_count").
		Joins("LEFT JOIN "+names.Tables.TenantMembers.String()+" AS tm ON tm.tenant_id = t.id AND tm.principal_id = ? AND tm.status = ?",
			principalID, MemberActive).
		Where("t.id = ?", tenantID).
		Group("t.status").Take(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("check active tenant membership: %w", err)
	}
	tenantActive = state.Status == "active"
	return tenantActive, tenantActive && state.MemberCount == 1, nil
}

var _ ActiveMemberChecker = (*MembershipChecker)(nil)
