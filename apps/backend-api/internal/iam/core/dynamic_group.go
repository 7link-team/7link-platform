package core

import (
	"context"
	"encoding/json"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

type dynamicGroupRuleRecord struct {
	ID           guid.ID `gorm:"column:id"`
	RuleJSON     string  `gorm:"column:rule_json"`
	Subject      string  `gorm:"column:subject"`
	Email        string  `gorm:"column:email"`
	DepartmentID string  `gorm:"column:department_id"`
	Status       string  `gorm:"column:status"`
}

func matchingDynamicGroupIDs(
	ctx context.Context,
	db *gorm.DB,
	prefix string,
	tenantID, subject guid.ID,
) ([]guid.ID, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	rows := make([]dynamicGroupRuleRecord, 0)
	err := db.WithContext(ctx).Table(prefix+"iam_groups AS g").
		Select("g.id, g.rule_json, tm.principal_id AS subject, tm.email, COALESCE(md.department_id, 0) AS department_id, tm.status").
		Joins("JOIN "+prefix+"iam_tenant_members AS tm ON tm.tenant_id = g.tenant_id AND tm.principal_id = ? AND tm.status = ?", subject, MemberActive).
		Joins("LEFT JOIN "+prefix+"iam_member_departments AS md ON md.tenant_id = tm.tenant_id AND md.principal_id = tm.principal_id").
		Where("g.tenant_id = ? AND g.group_type = 'dynamic' AND g.status = 'active'", tenantID).
		Order("g.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list dynamic group candidates: %w", err)
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		matched, err := policyx.EvaluateMemberRule(json.RawMessage(row.RuleJSON), policyx.MemberFacts{
			Subject: row.Subject, Email: row.Email,
			DepartmentID: row.DepartmentID, Status: row.Status,
		})
		if err != nil {
			return nil, fmt.Errorf("evaluate dynamic group %q rule: %w", row.ID, err)
		}
		if matched {
			ids = append(ids, row.ID)
		}
	}
	return ids, nil
}
