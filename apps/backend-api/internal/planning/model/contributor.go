package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Contributor stores explicit assignments. OWNER and CHILD_GOAL contributions
// are derived from current goal records so lifecycle changes cannot leave stale grants.
type Contributor struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	GoalID      guid.ID   `gorm:"column:goal_id;not null"`
	KR          string    `gorm:"column:kr;not null"`
	PrincipalID guid.ID   `gorm:"column:principal_id;not null"`
	CreatedBy   guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Contributor) TableName() string { return "planning_goal_contributors" }
