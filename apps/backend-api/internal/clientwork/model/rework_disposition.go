package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReworkDisposition freezes one Human decision against one rejected receipt item.
type ReworkDisposition struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	IntakeID      guid.ID   `gorm:"column:intake_id;not null"`
	ReceiptID     guid.ID   `gorm:"column:receipt_id;not null"`
	ItemID        string    `gorm:"column:item_id;not null"`
	Mode          string    `gorm:"column:mode;not null"`
	TargetID      *guid.ID  `gorm:"column:target_id"`
	Reason        string    `gorm:"column:reason;not null"`
	ClientMessage string    `gorm:"column:client_message;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ReworkDisposition) TableName() string { return "clientwork_rework_dispositions" }
