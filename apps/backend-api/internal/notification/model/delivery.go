package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Delivery struct {
	ID                     guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID               guid.ID    `gorm:"column:tenant_id;not null"`
	NotificationID         *guid.ID   `gorm:"column:notification_id"`
	Channel                string     `gorm:"column:channel;size:16;not null"`
	RecipientPrincipalID   *guid.ID   `gorm:"column:recipient_principal_id"`
	RecipientAddressHash   string     `gorm:"column:recipient_address_hash;size:64;not null"`
	RecipientAddressSealed string     `gorm:"column:recipient_address_sealed;not null"`
	PayloadSealed          string     `gorm:"column:payload_sealed;not null"`
	MessageID              string     `gorm:"column:message_id;size:998;not null"`
	SourceRef              string     `gorm:"column:source_ref;size:160;not null"`
	State                  string     `gorm:"column:state;size:16;not null"`
	SuppressReason         string     `gorm:"column:suppress_reason;size:32;not null"`
	JobID                  *guid.ID   `gorm:"column:job_id"`
	Attempts               int        `gorm:"column:attempts;not null"`
	LastError              string     `gorm:"column:last_error;size:1024;not null"`
	SentAt                 *time.Time `gorm:"column:sent_at"`
	CreatedAt              time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt              time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Delivery) TableName() string { return "notification_deliveries" }
