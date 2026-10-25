package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// CredentialRotation coordinates independent IAM commits without storing secrets.
type CredentialRotation struct {
	ID           guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID    guid.ID    `gorm:"column:machine_id;not null"`
	PrincipalID  guid.ID    `gorm:"column:principal_id;not null"`
	RequestedBy  guid.ID    `gorm:"column:requested_by;not null"`
	State        string     `gorm:"column:state;not null;size:32"`
	Phase        string     `gorm:"column:phase;not null;size:32"`
	ReasonCode   string     `gorm:"column:reason_code;not null;size:32"`
	ActiveKey    *int       `gorm:"column:active_key"`
	Attempts     int        `gorm:"column:attempts;not null"`
	CredentialID *guid.ID   `gorm:"column:credential_id"`
	IssuedAt     *time.Time `gorm:"column:issued_at"`
	RetireOldAt  time.Time  `gorm:"column:retire_old_at;not null"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (CredentialRotation) TableName() string { return "fleet_credential_rotations" }

// MachineCredential tracks delivery and retirement; IAM remains the credential authority.
type MachineCredential struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID   guid.ID    `gorm:"column:machine_id;not null"`
	RotationID  *guid.ID   `gorm:"column:rotation_id"`
	State       string     `gorm:"column:state;not null;size:32"`
	ReasonCode  string     `gorm:"column:reason_code;not null;size:32"`
	RetireAt    *time.Time `gorm:"column:retire_at"`
	DeliveredAt *time.Time `gorm:"column:delivered_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (MachineCredential) TableName() string { return "fleet_machine_credentials" }
