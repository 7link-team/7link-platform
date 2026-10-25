package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ArtifactConsumption preserves the exact accepted evidence consumed by a work item.
// Reconciliation changes only its current applicability, never the original evidence.
type ArtifactConsumption struct {
	TenantID           guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"-"`
	ID                 guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID          guid.ID    `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID         guid.ID    `gorm:"column:work_item_id;not null" json:"work_item_id"`
	UpstreamWorkItemID guid.ID    `gorm:"column:upstream_work_item_id;not null" json:"upstream_work_item_id"`
	AcceptedArtifactID guid.ID    `gorm:"column:accepted_artifact_id;not null" json:"accepted_artifact_id"`
	ResultID           guid.ID    `gorm:"column:result_id;not null" json:"result_id"`
	FileID             guid.ID    `gorm:"column:file_id;not null" json:"file_id"`
	VersionID          guid.ID    `gorm:"column:version_id;not null" json:"version_id"`
	SHA256             string     `gorm:"column:content_sha256;not null" json:"content_sha256"`
	State              string     `gorm:"column:state;not null" json:"state"`
	ObservedHash       string     `gorm:"column:observed_hash;not null" json:"observed_hash"`
	CreatedBy          guid.ID    `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	CheckedAt          *time.Time `gorm:"column:checked_at" json:"checked_at"`
}

func (ArtifactConsumption) TableName() string { return "delivery_artifact_consumptions" }
