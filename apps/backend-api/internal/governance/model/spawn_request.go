package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type SpawnRequest struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null" json:"tenant_id"`
	RequesterID  guid.ID   `gorm:"column:requester_id;not null" json:"requester_id"`
	SupervisorID guid.ID   `gorm:"column:supervisor_id;not null" json:"supervisor_id"`
	AgentID      *guid.ID  `gorm:"column:agent_id" json:"agent_id,omitempty"`
	State        string    `gorm:"column:state;not null" json:"state"`
	Payload      string    `gorm:"column:payload;not null" json:"-"`
	Reason       string    `gorm:"column:reason;not null" json:"reason"`
	Version      int       `gorm:"column:version;not null" json:"version"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	ExpiresAt    time.Time `gorm:"column:expires_at;not null" json:"expires_at"`
}

func (SpawnRequest) TableName() string { return "governance_spawn_requests" }
