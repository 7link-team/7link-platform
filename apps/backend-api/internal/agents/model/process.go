package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProcessEvent records a command or a fenced Runner observation, never a guess.
type ProcessEvent struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null" json:"id"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null" json:"-"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null" json:"agent_principal_id"`
	Kind             string    `gorm:"column:kind;not null;size:32" json:"kind"`
	MachineID        *guid.ID  `gorm:"column:machine_id" json:"machine_id,omitempty"`
	PID              int       `gorm:"column:pid;not null" json:"pid"`
	RestartCount     int       `gorm:"column:restart_count;not null" json:"restart_count"`
	Reason           string    `gorm:"column:reason;not null;size:2000" json:"reason"`
	OccurredAt       time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false;autoUpdateTime:false" json:"occurred_at"`
}

func (ProcessEvent) TableName() string { return "agents_process_events" }
