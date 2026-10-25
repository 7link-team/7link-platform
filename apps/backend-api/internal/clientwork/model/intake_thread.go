package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// IntakeThread preserves the client-shared source root without moving messages
// or attachments when the source is merged or declined.
type IntakeThread struct {
	TenantID      guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	IntakeID      guid.ID   `gorm:"column:intake_id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	ChannelID     guid.ID   `gorm:"column:channel_id;not null"`
	RootMessageID guid.ID   `gorm:"column:root_message_id;not null"`
	RecipientID   guid.ID   `gorm:"column:recipient_id;not null"`
	CreatedBy     guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (IntakeThread) TableName() string { return "clientwork_intake_threads" }
