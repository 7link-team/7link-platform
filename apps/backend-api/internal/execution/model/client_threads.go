package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClientCommentLink binds a CLIENT thread to exactly one frozen delivery item.
// AnchorSnapshot is captured from pinned bytes and never updated on conversion.
type ClientCommentLink struct {
	FirstBody      string     `gorm:"column:first_body;not null"`
	CreatedBy      guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	ThreadID       guid.ID    `gorm:"column:thread_id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	DeliveryID     guid.ID    `gorm:"column:delivery_id;not null"`
	ItemID         string     `gorm:"column:item_id;size:64;not null"`
	ContactID      guid.ID    `gorm:"column:contact_id;not null"`
	AnchorSnapshot string     `gorm:"column:anchor_snapshot;not null"`
	TargetKind     string     `gorm:"column:target_kind;size:32;not null"`
	TargetID       *guid.ID   `gorm:"column:target_id"`
	ConvertedAt    *time.Time `gorm:"column:converted_at"`
	ConvertedBy    *guid.ID   `gorm:"column:converted_by"`
}

func (ClientCommentLink) TableName() string { return "execution_client_comment_links" }
