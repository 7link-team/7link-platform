package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProjectExecutionPolicy controls unattended execution independently of any grant.
type ProjectExecutionPolicy struct {
	TenantID      guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;primaryKey;autoIncrement:false;not null"`
	AllowFullAuto bool      `gorm:"column:allow_full_auto;not null"`
	Version       int       `gorm:"column:version;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	Reason        string    `gorm:"column:reason;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ProjectExecutionPolicy) TableName() string { return "execution_project_policies" }
