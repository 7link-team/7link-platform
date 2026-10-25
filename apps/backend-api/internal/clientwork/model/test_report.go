package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// TestReport freezes its source at request time; only the pending file receipt
// may be completed. Completed content and source snapshots are immutable.
type TestReport struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID    `gorm:"column:project_id;not null"`
	ActorID     guid.ID    `gorm:"column:actor_id;not null"`
	Title       string     `gorm:"column:title;not null;size:200"`
	Snapshot    string     `gorm:"column:snapshot;not null"`
	State       string     `gorm:"column:state;not null;size:16"`
	JobID       guid.ID    `gorm:"column:job_id;not null"`
	BindingID   *guid.ID   `gorm:"column:binding_id"`
	FileID      *guid.ID   `gorm:"column:file_id"`
	VersionID   *guid.ID   `gorm:"column:version_id"`
	ContentHash *string    `gorm:"column:content_hash;size:64"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	BuiltAt     *time.Time `gorm:"column:built_at;autoCreateTime:false;autoUpdateTime:false"`
}

func (TestReport) TableName() string { return "clientwork_test_reports" }
