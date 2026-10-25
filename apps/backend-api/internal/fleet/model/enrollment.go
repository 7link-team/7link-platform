package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Enrollment stores coordination state only. Raw device codes and IAM secrets
// are returned to the device once and never persisted by fleet.
type Enrollment struct {
	ID                  guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID            guid.ID    `gorm:"column:tenant_id;not null"`
	ClientID            guid.ID    `gorm:"column:client_id;not null"`
	DeviceHash          string     `gorm:"column:device_hash;size:64;not null"`
	UserHash            string     `gorm:"column:user_hash;size:64;not null"`
	Name                string     `gorm:"column:name;size:100;not null"`
	OS                  string     `gorm:"column:os;size:32;not null"`
	FactsJSON           *string    `gorm:"column:facts_json;type:text"`
	State               string     `gorm:"column:state;size:32;not null"`
	PrincipalID         *guid.ID   `gorm:"column:principal_id"`
	CredentialID        *guid.ID   `gorm:"column:credential_id"`
	ApprovedBy          *guid.ID   `gorm:"column:approved_by"`
	MachineID           *guid.ID   `gorm:"column:machine_id"`
	ExpiresAt           time.Time  `gorm:"column:expires_at;not null"`
	PollIntervalSeconds int64      `gorm:"column:poll_interval_seconds;not null;default:5"`
	LastPolledAt        *time.Time `gorm:"column:last_polled_at"`
	AcknowledgedAt      *time.Time `gorm:"column:acknowledged_at"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Enrollment) TableName() string { return "fleet_machine_enrollments" }
