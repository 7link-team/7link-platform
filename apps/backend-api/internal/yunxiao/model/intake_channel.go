package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// IntakeChannel stores policy and hashed API credentials, with a UTC daily
// receiving counter. Email and portal policy never imply available transports.
type IntakeChannel struct {
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID      guid.ID   `gorm:"column:project_id;not null"`
	Kind           string    `gorm:"column:kind;size:16;not null"`
	Document       string    `gorm:"column:document;not null"`
	CredentialHash *string   `gorm:"column:credential_hash;size:64"`
	ReceivingDay   *string   `gorm:"column:receiving_day;size:10"`
	ReceivedCount  int       `gorm:"column:received_count;not null;default:0"`
	Version        int       `gorm:"column:version;not null"`
	UpdatedBy      guid.ID   `gorm:"column:updated_by;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (IntakeChannel) TableName() string { return "intake_channel_settings" }
