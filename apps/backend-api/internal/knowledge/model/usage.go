package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// UsageReceipt is an immutable, source-verified runtime fact. Diagnostics never
// create these rows. SourceID survives chunk retention cleanup.
type UsageReceipt struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	SourceID    guid.ID   `gorm:"column:source_id;not null"`
	EntryID     guid.ID   `gorm:"column:entry_id;not null"`
	ExecutionID guid.ID   `gorm:"column:execution_id;not null"`
	Kind        string    `gorm:"column:kind;not null;size:16"`
	EvidenceID  guid.ID   `gorm:"column:evidence_id;not null"`
	ContentHash string    `gorm:"column:content_hash;not null;size:64"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (UsageReceipt) TableName() string { return "knowledge_usage_receipts" }
