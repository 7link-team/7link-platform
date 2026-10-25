package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReportQualityReceipt freezes source-owned gate facts; retries never mutate it.
type ReportQualityReceipt struct {
	ExecutorPrincipalID *guid.ID  `gorm:"column:executor_principal_id"`
	TenantID            guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                  guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID           guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID          guid.ID   `gorm:"column:work_item_id;not null"`
	Kind                string    `gorm:"column:kind;not null"`
	SourceID            guid.ID   `gorm:"column:source_id;not null"`
	Rounds              int       `gorm:"column:rounds;not null"`
	OccurredAt          time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false"`
}

func (ReportQualityReceipt) TableName() string { return "delivery_report_quality_receipts" }

// ReportQualityObservation is migration metadata, not a domain receipt. The
// singleton marks the earliest complete observation boundary, without backfill.
type ReportQualityObservation struct {
	Singleton     int       `gorm:"column:singleton;primaryKey;autoIncrement:false;not null"`
	ObservedSince time.Time `gorm:"column:observed_since;not null;autoCreateTime:false"`
}

func (ReportQualityObservation) TableName() string { return "delivery_report_quality_observation" }
