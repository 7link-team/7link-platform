package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// PreviewSession records authority and lifecycle only; application bytes stay in the tunnel.
type PreviewSession struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID        guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID    `gorm:"column:project_id;not null"`
	ExecutionID     guid.ID    `gorm:"column:execution_id;not null"`
	MachineID       guid.ID    `gorm:"column:machine_id;not null"`
	RunnerSessionID guid.ID    `gorm:"column:runner_session_id;not null"`
	ConnEpoch       int64      `gorm:"column:conn_epoch;not null"`
	DispatchEpoch   int64      `gorm:"column:dispatch_epoch;not null"`
	Port            int        `gorm:"column:port;not null"`
	Label           string     `gorm:"column:label;size:26;not null"`
	State           string     `gorm:"column:state;size:16;not null"`
	StopReason      string     `gorm:"column:stop_reason;size:32;not null"`
	StartedBy       guid.ID    `gorm:"column:started_by;not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	LastAccessAt    time.Time  `gorm:"column:last_access_at;not null"`
	ExpiresAt       time.Time  `gorm:"column:expires_at;not null"`
	EndedAt         *time.Time `gorm:"column:ended_at"`
	Version         int        `gorm:"column:version;not null"`
}

func (PreviewSession) TableName() string { return "execution_preview_sessions" }

// PreviewAccess stores only token digests. Codes are consumed atomically once.
type PreviewAccess struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	SessionID       guid.ID    `gorm:"column:session_id;not null"`
	PrincipalID     guid.ID    `gorm:"column:principal_id;not null"`
	CodeHash        string     `gorm:"column:code_hash;size:64;not null"`
	CodeExpiresAt   time.Time  `gorm:"column:code_expires_at;not null"`
	ConsumedAt      *time.Time `gorm:"column:consumed_at"`
	CookieHash      *string    `gorm:"column:cookie_hash;size:64"`
	CookieExpiresAt *time.Time `gorm:"column:cookie_expires_at"`
	ReturnPath      string     `gorm:"column:return_path;size:2000;not null"`
}

func (PreviewAccess) TableName() string { return "execution_preview_access" }
