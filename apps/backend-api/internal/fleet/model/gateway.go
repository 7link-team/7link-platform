package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// GatewayConfig is a versioned tenant administration aggregate. Provider/model
// and budget records are stored as validated JSON; keys are separate sealed rows.
type GatewayConfig struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null;uniqueIndex"`
	Enabled      bool      `gorm:"column:enabled;not null"`
	MachineID    *guid.ID  `gorm:"column:machine_id"`
	DefaultModel string    `gorm:"column:default_model;not null;size:160"`
	RecordsJSON  string    `gorm:"column:records_json;not null;type:text"`
	Version      int       `gorm:"column:version;not null"`
	Revision     int64     `gorm:"column:revision;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (GatewayConfig) TableName() string { return "fleet_gateway_configs" }

type GatewayKey struct {
	RevokedAt     *time.Time `gorm:"column:revoked_at"`
	ReplacementID *guid.ID   `gorm:"column:replacement_id"`
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	Sealed        string     `gorm:"column:sealed;not null;type:text"`
	KeyHash       string     `gorm:"column:key_hash;not null;size:64;uniqueIndex"`
	Last4         string     `gorm:"column:last4;not null;size:4"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (GatewayKey) TableName() string { return "fleet_gateway_keys" }

// Requests never persist prompts, tools, credentials or response content.
type GatewayRequest struct {
	ID         guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID  *guid.ID   `gorm:"column:project_id"`
	TenantID   guid.ID    `gorm:"column:tenant_id;not null"`
	Revision   int64      `gorm:"column:revision;not null"`
	Model      string     `gorm:"column:model;not null;size:160"`
	State      string     `gorm:"column:state;not null;size:32"`
	Reason     string     `gorm:"column:reason;not null;size:64"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
}

func (GatewayRequest) TableName() string { return "fleet_gateway_requests" }
