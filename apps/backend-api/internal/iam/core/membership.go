package core

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

// MembershipChecker is the fail-closed tenant admission capability used by
// authorization consumers. It owns no mutation methods.
type MembershipChecker struct {
	runtime *appenv.Runtime
}

func NewMembershipChecker(runtime *appenv.Runtime) *MembershipChecker {
	if runtime == nil {
		panic("iam membership checker requires runtime")
	}
	return &MembershipChecker{runtime: runtime}
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

func (m *MembershipChecker) IsMemberActiveOn(
	ctx context.Context,
	db *gorm.DB,
	tenantID, principalID guid.ID,
) (bool, error) {
	_, active, err := tenantMembershipState(ctx, db, m.prefix(), tenantID, principalID)
	return active, err
}

func (m *MembershipChecker) prefix() string {
	if m == nil || m.runtime == nil {
		return ""
	}
	return m.runtime.TablePrefix()
}

type tenantAdmission struct {
	Status      string `gorm:"column:status"`
	MemberCount int    `gorm:"column:member_count"`
}

func tenantMembershipState(
	ctx context.Context,
	db *gorm.DB,
	prefix string,
	tenantID, principalID guid.ID,
) (tenantActive, memberActive bool, err error) {
	if db == nil {
		return false, false, ErrUnavailable
	}
	var state tenantAdmission
	err = db.WithContext(ctx).Table(prefix+"iam_tenants AS t").
		Select("t.status, COUNT(tm.principal_id) AS member_count").
		Joins("LEFT JOIN "+prefix+"iam_tenant_members AS tm ON tm.tenant_id = t.id AND tm.principal_id = ? AND tm.status = ?",
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
