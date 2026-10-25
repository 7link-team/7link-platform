package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Receipt struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	Consumer    string    `gorm:"column:consumer;size:128;not null"`
	EventID     guid.ID   `gorm:"column:event_id;not null"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ProcessedAt time.Time `gorm:"column:processed_at;not null"`
}

func (Receipt) TableName() string { return "domain_event_receipts" }
