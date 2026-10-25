package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Deployment records a service-authenticated, immutable exact-artifact event.
type Deployment struct {
	TenantID         guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID        guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID       guid.ID   `gorm:"column:work_item_id;not null"`
	ResultID         guid.ID   `gorm:"column:result_id;not null"`
	ProducerID       guid.ID   `gorm:"column:producer_id;not null"`
	EventKey         string    `gorm:"column:event_key;not null"`
	Environment      string    `gorm:"column:environment;not null"`
	Commit           string    `gorm:"column:commit_hash;not null"`
	ArtifactHash     string    `gorm:"column:artifact_hash;not null"`
	MergeReceiptHash string    `gorm:"column:merge_receipt_hash;not null"`
	FixedVersion     string    `gorm:"column:fixed_version;not null"`
	PayloadHash      string    `gorm:"column:payload_hash;not null"`
	OccurredAt       time.Time `gorm:"column:occurred_at;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Deployment) TableName() string { return "delivery_deployments" }
