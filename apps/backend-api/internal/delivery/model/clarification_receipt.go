package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClarificationReceipt records handling of one exact source reply revision.
type ClarificationReceipt struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	ClarificationID guid.ID   `gorm:"column:clarification_id;not null"`
	ReplyVersion    int       `gorm:"column:reply_version;not null"`
	HandledBy       guid.ID   `gorm:"column:handled_by;not null"`
	HandledAt       time.Time `gorm:"column:handled_at;not null;autoCreateTime:false"`
}

func (ClarificationReceipt) TableName() string { return "delivery_clarification_receipts" }
