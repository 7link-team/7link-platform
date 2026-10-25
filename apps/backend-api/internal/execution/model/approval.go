package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Approval binds one runtime permission to one leased execution attempt.
type Approval struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	ExecutionID   guid.ID    `gorm:"column:execution_id;not null"`
	DispatchEpoch int64      `gorm:"column:dispatch_epoch;not null"`
	SessionID     guid.ID    `gorm:"column:session_id;not null"`
	ConnEpoch     int64      `gorm:"column:conn_epoch;not null"`
	RequestID     string     `gorm:"column:request_id;size:160;not null"`
	Tool          string     `gorm:"column:tool;size:160;not null"`
	InputJSON     string     `gorm:"column:input_json;not null"`
	RequestHash   string     `gorm:"column:request_hash;size:64;not null"`
	State         string     `gorm:"column:state;size:32;not null"`
	ActorID       *guid.ID   `gorm:"column:actor_id"`
	Reason        string     `gorm:"column:reason;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	ExpiresAt     time.Time  `gorm:"column:expires_at;not null"`
	DecidedAt     *time.Time `gorm:"column:decided_at"`
}

func (Approval) TableName() string { return "execution_approvals" }
