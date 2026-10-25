package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Plan struct {
	UnconfirmedAt *time.Time `gorm:"column:unconfirmed_at"`
	SubmittedAt   *time.Time `gorm:"column:submitted_at"`
	TenantID      guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID    `gorm:"column:project_id;not null"`
	PrincipalID   guid.ID    `gorm:"column:principal_id;not null"`
	PeriodKind    string     `gorm:"column:period_kind;not null"`
	PeriodStart   string     `gorm:"column:period_start;not null"`
	State         string     `gorm:"column:state;not null"`
	Content       string     `gorm:"column:content;not null"`
	ContentHash   string     `gorm:"column:content_hash;not null"`
	Version       int        `gorm:"column:version;not null"`
	CreatedBy     guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Plan) TableName() string { return "planning_plans" }
