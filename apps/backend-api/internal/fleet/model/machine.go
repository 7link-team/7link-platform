package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Machine records an enrolled IAM service account, never a parallel credential.
type Machine struct {
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	PrincipalID      guid.ID    `gorm:"column:principal_id;not null"`
	Name             string     `gorm:"column:name;not null;size:100"`
	NameKey          *string    `gorm:"column:name_key;size:100"`
	State            string     `gorm:"column:state;not null;size:32"`
	DrainRequested   bool       `gorm:"column:drain_requested;not null"`
	OS               string     `gorm:"column:os;not null;size:32"`
	SlotCount        int        `gorm:"column:slot_count;not null"`
	ConnEpoch        int64      `gorm:"column:conn_epoch;not null"`
	CurrentSessionID *guid.ID   `gorm:"column:current_session_id"`
	LastSeenAt       *time.Time `gorm:"column:last_seen_at"`
	Version          int        `gorm:"column:version;not null"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Machine) TableName() string { return "fleet_machines" }

// RunnerSession fences stale connections even if they retain a valid IAM token.
type RunnerSession struct {
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID     guid.ID    `gorm:"column:machine_id;not null"`
	IncarnationID string     `gorm:"column:incarnation_id;not null;size:64"`
	ConnEpoch     int64      `gorm:"column:conn_epoch;not null"`
	State         string     `gorm:"column:state;not null;size:32"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	LastSeenAt    time.Time  `gorm:"column:last_seen_at;not null"`
	ClosedAt      *time.Time `gorm:"column:closed_at"`
}

func (RunnerSession) TableName() string { return "fleet_runner_sessions" }

// MachineRuntime is an observed inventory; absence never means installation.
type MachineRuntime struct {
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID   `gorm:"column:tenant_id;not null"`
	MachineID      guid.ID   `gorm:"column:machine_id;not null"`
	Kind           string    `gorm:"column:kind;not null;size:32"`
	Version        string    `gorm:"column:version;not null;size:64"`
	ExecutablePath string    `gorm:"column:executable_path;not null;size:1024"`
	ModelsJSON     string    `gorm:"column:models_json;not null"`
	InspectedAt    time.Time `gorm:"column:inspected_at;not null"`
}

func (MachineRuntime) TableName() string { return "fleet_machine_runtimes" }

type Workdir struct {
	Version      int        `gorm:"column:version;not null;default:1"`
	ArchivedAt   *time.Time `gorm:"column:archived_at"`
	ID           guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID    guid.ID    `gorm:"column:machine_id;not null"`
	ProjectID    guid.ID    `gorm:"column:project_id;not null"`
	RepositoryID guid.ID    `gorm:"column:repository_id;not null"`
	Path         string     `gorm:"column:path;not null;size:1024"`
	PathKey      string     `gorm:"column:path_key;not null;size:64"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Workdir) TableName() string { return "fleet_workdirs" }

// ActiveSlot is nullable after release so unique slot ownership is portable.
type SlotReservation struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	MachineID   guid.ID    `gorm:"column:machine_id;not null"`
	ExecutionID guid.ID    `gorm:"column:execution_id;not null"`
	SlotNo      int        `gorm:"column:slot_no;not null"`
	ActiveSlot  *int       `gorm:"column:active_slot"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	ReleasedAt  *time.Time `gorm:"column:released_at"`
}

func (SlotReservation) TableName() string { return "fleet_slot_reservations" }

type MachineTool struct {
	ID             guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID       guid.ID   `gorm:"column:tenant_id;not null"`
	MachineID      guid.ID   `gorm:"column:machine_id;not null"`
	Name           string    `gorm:"column:name;not null;size:64"`
	Version        string    `gorm:"column:version;not null;size:64"`
	ExecutablePath string    `gorm:"column:executable_path;not null;size:1024"`
	InspectedAt    time.Time `gorm:"column:inspected_at;not null"`
}

func (MachineTool) TableName() string { return "fleet_machine_tools" }

// Prices are integer USD micros per million tokens; unknown is not free.
type ModelPrice struct {
	CacheReadMicros  int64      `gorm:"column:cache_read_micros;not null;default:0"`
	CacheWriteMicros int64      `gorm:"column:cache_write_micros;not null;default:0"`
	Version          int        `gorm:"column:version;not null;default:1"`
	DeletedAt        *time.Time `gorm:"column:deleted_at"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID         guid.ID    `gorm:"column:tenant_id;not null"`
	Runtime          string     `gorm:"column:runtime;not null;size:32"`
	Model            string     `gorm:"column:model;not null;size:160"`
	InputMicros      int64      `gorm:"column:input_micros;not null"`
	OutputMicros     int64      `gorm:"column:output_micros;not null"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ModelPrice) TableName() string { return "fleet_model_prices" }
