package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Merge retains the exact authorized command and immutable fenced outcome.
type Merge struct {
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID         guid.ID    `gorm:"column:project_id;not null"`
	ExecutionID       guid.ID    `gorm:"column:execution_id;not null"`
	MachineID         guid.ID    `gorm:"column:machine_id;not null"`
	ActorID           guid.ID    `gorm:"column:actor_id;not null"`
	SessionID         guid.ID    `gorm:"column:session_id;not null"`
	ConnEpoch         int64      `gorm:"column:conn_epoch;not null"`
	Attempt           int        `gorm:"column:attempt;not null"`
	ActiveExecutionID *guid.ID   `gorm:"column:active_execution_id"`
	State             string     `gorm:"column:state;size:16;not null"`
	AuthoritySource   string     `gorm:"column:authority_source;size:200;not null"`
	ActorKind         string     `gorm:"column:actor_kind;size:16;not null"`
	Reason            string     `gorm:"column:reason;type:text;not null"`
	PlanJSON          string     `gorm:"column:plan_json;type:text;not null"`
	PlanHash          string     `gorm:"column:plan_hash;size:64;not null"`
	ObservationJSON   string     `gorm:"column:observation_json;type:text;not null"`
	ReceiptHash       string     `gorm:"column:receipt_hash;size:64;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	DeadlineAt        time.Time  `gorm:"column:deadline_at;not null"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
}

func (Merge) TableName() string { return "execution_merges" }
