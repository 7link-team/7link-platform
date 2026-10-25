package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// ResetCommand is a durable command fenced to the connection that accepted it.
type ResetCommand struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	MachineID        guid.ID   `gorm:"column:machine_id;not null"`
	SessionID        guid.ID   `gorm:"column:session_id;not null"`
	ConnEpoch        int64     `gorm:"column:conn_epoch;not null"`
	SessionEpoch     int       `gorm:"column:session_epoch;not null"`
	WorkspaceEpoch   int       `gorm:"column:workspace_epoch;not null"`
	Kind             string    `gorm:"column:kind;size:16;not null"`
	State            string    `gorm:"column:state;size:16;not null"`
	ReasonCode       string    `gorm:"column:reason_code;size:64;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	ExpiresAt        time.Time `gorm:"column:expires_at;not null"`
}

func (ResetCommand) TableName() string { return "fleet_reset_commands" }
