package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// WorkItemCriterion is a stable task-owned acceptance clause. A task may have
// criteria without a Requirement; requirement baselines remain separate records.
type WorkItemCriterion struct {
	TenantID           guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                 guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID          guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID         guid.ID   `gorm:"column:work_item_id;not null"`
	Position           int       `gorm:"column:position;not null"`
	Text               string    `gorm:"column:text;not null"`
	VerificationMethod string    `gorm:"column:verification_method;not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	CreatedBy          guid.ID   `gorm:"column:created_by;not null"`
}

func (WorkItemCriterion) TableName() string { return "delivery_work_item_criteria" }

// WorkItemContract freezes the Human-confirmed brief. VersionNo is independent
// of WorkItem.Version (concurrency) and BaselineVersionNo (Requirement baseline).
// Commands insert new versions; no command updates an existing snapshot.
type WorkItemContract struct {
	TenantID    guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID  guid.ID   `gorm:"column:work_item_id;not null"`
	VersionNo   int       `gorm:"column:version_no;not null"`
	Snapshot    string    `gorm:"column:snapshot;not null"`
	ContentHash string    `gorm:"column:content_hash;not null"`
	ConfirmedAt time.Time `gorm:"column:confirmed_at;not null"`
	ConfirmedBy guid.ID   `gorm:"column:confirmed_by;not null"`
}

func (WorkItemContract) TableName() string { return "delivery_work_item_contracts" }
