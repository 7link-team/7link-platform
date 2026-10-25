package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// Inspection records an explicit request; terminal reports are never redispatched.
type Inspection struct {
	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	MachineID   guid.ID   `gorm:"column:machine_id;not null"`
	RequestedBy guid.ID   `gorm:"column:requested_by;not null"`
	Kind        string    `gorm:"column:kind;not null;size:32"`
	State       string    `gorm:"column:state;not null;size:32"`
	ReasonCode  string    `gorm:"column:reason_code;not null;size:32"`
	ActiveKey   *int      `gorm:"column:active_key"`
	SessionID   guid.ID   `gorm:"column:session_id;not null"`
	ConnEpoch   int64     `gorm:"column:conn_epoch;not null"`
	ScopeKey    string    `gorm:"column:scope_key;not null;size:40;default:'home'"`
	WorkdirPath string    `gorm:"column:workdir_path;not null;size:1024;default:''"`
	ResultJSON  string    `gorm:"column:result_json;not null;type:text;default:'[]'"`
	ResultHash  string    `gorm:"column:result_hash;not null;size:64"`
	ExpiresAt   time.Time `gorm:"column:expires_at;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Inspection) TableName() string { return "fleet_inspections" }
