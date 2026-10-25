package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// TerminalSession stores lifecycle metadata only. Tunnel bytes belong to files.
type TerminalSession struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID        guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID       guid.ID    `gorm:"column:machine_id;not null"`
	ExecutionID     guid.ID    `gorm:"column:execution_id;not null"`
	ProjectID       guid.ID    `gorm:"column:project_id;not null"`
	OpenedBy        guid.ID    `gorm:"column:opened_by;not null"`
	RunnerSessionID guid.ID    `gorm:"column:runner_session_id;not null"`
	ConnEpoch       int64      `gorm:"column:conn_epoch;not null"`
	DispatchEpoch   int64      `gorm:"column:dispatch_epoch;not null"`
	ActiveExecution *guid.ID   `gorm:"column:active_execution"`
	State           string     `gorm:"column:state;size:16;not null"`
	Workspace       string     `gorm:"column:workspace;size:2048;not null"`
	Sandboxed       bool       `gorm:"column:sandboxed;not null"`
	OpenedAt        time.Time  `gorm:"column:opened_at;not null;autoCreateTime:false"`
	LastInputAt     time.Time  `gorm:"column:last_input_at;not null"`
	ExpiresAt       time.Time  `gorm:"column:expires_at;not null"`
	ClosedAt        *time.Time `gorm:"column:closed_at"`
	CloseReason     string     `gorm:"column:close_reason;size:64;not null"`
	RecordingChunks string     `gorm:"column:recording_chunks;type:text;not null"`
	RecordingFileID *guid.ID   `gorm:"column:recording_file_id"`
	RecordingSHA256 string     `gorm:"column:recording_sha256;size:64;not null"`
}

func (TerminalSession) TableName() string { return "fleet_terminal_sessions" }

// TerminalAccess is disabled until an authorized Human explicitly enables it.
type TerminalAccess struct {
	AllowRemotePreview  bool      `gorm:"column:allow_remote_preview;not null;default:false"`
	TunnelBandwidthKbps int       `gorm:"column:tunnel_bandwidth_kbps;not null;default:20000"`
	TunnelMaxStreams    int       `gorm:"column:tunnel_max_streams;not null;default:256"`
	MachineID           guid.ID   `gorm:"column:machine_id;primaryKey;autoIncrement:false;not null"`
	TenantID            guid.ID   `gorm:"column:tenant_id;not null"`
	Enabled             bool      `gorm:"column:enabled;not null"`
	Version             int       `gorm:"column:version;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (TerminalAccess) TableName() string { return "fleet_terminal_access" }
