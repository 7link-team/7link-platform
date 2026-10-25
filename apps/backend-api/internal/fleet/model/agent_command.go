package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// AgentMachineCommand stores encrypted delivery and real machine receipts.
type AgentMachineCommand struct {
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	AgentPrincipalID guid.ID    `gorm:"column:agent_principal_id;not null"`
	MachineID        guid.ID    `gorm:"column:machine_id;not null"`
	CredentialID     *guid.ID   `gorm:"column:credential_id"`
	ConfigRevision   int        `gorm:"column:config_revision;not null"`
	Kind             string     `gorm:"column:kind;size:32;not null"`
	State            string     `gorm:"column:state;size:16;not null"`
	SealedCredential string     `gorm:"column:sealed_credential;not null"`
	SessionID        *guid.ID   `gorm:"column:session_id"`
	ConnEpoch        int64      `gorm:"column:conn_epoch;not null"`
	ReasonCode       string     `gorm:"column:reason_code;size:64;not null"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	ExpiresAt        time.Time  `gorm:"column:expires_at;not null"`
	CompletedAt      *time.Time `gorm:"column:completed_at"`
}

func (AgentMachineCommand) TableName() string { return "fleet_agent_machine_commands" }
