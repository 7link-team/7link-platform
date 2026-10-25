package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Goal struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	PrincipalID guid.ID   `gorm:"column:principal_id;not null"`
	PeriodKind  string    `gorm:"column:period_kind;not null"`
	PeriodStart string    `gorm:"column:period_start;not null"`
	State       string    `gorm:"column:state;not null"`
	Content     string    `gorm:"column:content;not null"`
	ContentHash string    `gorm:"column:content_hash;not null"`
	Version     int       `gorm:"column:version;not null"`
	CreatedBy   guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Goal) TableName() string { return "planning_goals" }

// GoalSnapshot preserves the exact persisted goal version, never backfilled from
// today's mutable value. It is appended in the command's audit transaction.
type GoalSnapshot struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	GoalID      guid.ID   `gorm:"column:goal_id;primaryKey;autoIncrement:false;not null"`
	Version     int       `gorm:"column:version;primaryKey;not null"`
	Content     string    `gorm:"column:content;not null"`
	ContentHash string    `gorm:"column:content_hash;not null"`
	State       string    `gorm:"column:state;not null"`
	ObservedAt  time.Time `gorm:"column:observed_at;not null;autoCreateTime:false"`
}

func (GoalSnapshot) TableName() string { return "planning_goal_snapshots" }
