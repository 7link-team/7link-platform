package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type PreviewShare struct {
	TenantID    guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID    `gorm:"column:project_id;not null"`
	PreviewID   guid.ID    `gorm:"column:preview_id;not null"`
	ActiveSlot  *guid.ID   `gorm:"column:active_slot"`
	RecipientID guid.ID    `gorm:"column:recipient_id;not null"`
	RequestedBy guid.ID    `gorm:"column:requested_by;not null"`
	DecidedBy   *guid.ID   `gorm:"column:decided_by"`
	BindingID   *guid.ID   `gorm:"column:binding_id"`
	State       string     `gorm:"column:state;size:32;not null"`
	Note        string     `gorm:"column:note;not null"`
	TTLHours    int        `gorm:"column:ttl_hours;not null"`
	ExpiresAt   *time.Time `gorm:"column:expires_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	DecidedAt   *time.Time `gorm:"column:decided_at"`
	Reason      *string    `gorm:"column:reason"`
	Version     int        `gorm:"column:version;not null"`
}

func (PreviewShare) TableName() string { return "clientwork_preview_shares" }
