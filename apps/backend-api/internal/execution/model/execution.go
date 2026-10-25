package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Execution struct {
	ProgressJSON      *string    `gorm:"column:progress_json;type:text"`
	StrategyParentID  *guid.ID   `gorm:"column:strategy_parent_id"`
	PendingClaimJSON  *string    `gorm:"column:pending_claim_json;type:text"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID         guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID        guid.ID    `gorm:"column:work_item_id;not null"`
	AssignmentID      guid.ID    `gorm:"column:assignment_id;not null"`
	MachineID         guid.ID    `gorm:"column:machine_id;not null"`
	InitiatorID       guid.ID    `gorm:"column:initiator_id;not null"`
	ParentExecutionID *guid.ID   `gorm:"column:parent_execution_id"`
	SourceDecisionID  *guid.ID   `gorm:"column:source_decision_id"`
	BranchReason      string     `gorm:"column:branch_reason;size:32;not null"`
	StartPoint        string     `gorm:"column:start_point;size:32;not null"`
	FeedbackSnapshot  string     `gorm:"column:feedback_snapshot;not null"`
	Autostart         bool       `gorm:"column:autostart;not null"`
	State             string     `gorm:"column:state;size:32;not null"`
	StateReason       string     `gorm:"column:state_reason;size:64;not null"`
	ResultState       string     `gorm:"column:result_state;size:32;not null"`
	PlanJSON          string     `gorm:"column:plan_json;not null"`
	PlanHash          string     `gorm:"column:plan_hash;size:64;not null"`
	DispatchEpoch     int64      `gorm:"column:dispatch_epoch;not null"`
	LastSeq           int64      `gorm:"column:last_seq;not null"`
	LeaseExpiresAt    *time.Time `gorm:"column:lease_expires_at"`
	CancelRequestedAt *time.Time `gorm:"column:cancel_requested_at"`
	Version           int        `gorm:"column:version;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Execution) TableName() string { return "execution_executions" }

type Receipt struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ExecutionID   guid.ID   `gorm:"column:execution_id;not null"`
	DispatchEpoch int64     `gorm:"column:dispatch_epoch;not null"`
	Seq           int64     `gorm:"column:seq;not null"`
	Kind          string    `gorm:"column:kind;size:32;not null"`
	ContentHash   string    `gorm:"column:content_hash;size:64;not null"`
	State         string    `gorm:"column:state;size:32;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Receipt) TableName() string { return "execution_receipts" }

type Log struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ExecutionID   guid.ID   `gorm:"column:execution_id;not null"`
	DispatchEpoch int64     `gorm:"column:dispatch_epoch;not null"`
	Seq           int64     `gorm:"column:seq;not null"`
	Content       string    `gorm:"column:content;not null"`
	ContentHash   string    `gorm:"column:content_hash;size:64;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Log) TableName() string { return "execution_logs" }

type Evidence struct {
	State        string     `gorm:"column:state;size:16;not null;default:DRAFT"`
	ProducerKind string     `gorm:"column:producer_kind;size:16;not null;default:SYSTEM"`
	ProducerID   guid.ID    `gorm:"column:producer_id;not null;default:0"`
	ClaimID      *guid.ID   `gorm:"column:claim_id"`
	ScopeText    *string    `gorm:"column:scope_text;type:text"`
	Limitations  *string    `gorm:"column:limitations;type:text"`
	ObservedAt   *time.Time `gorm:"column:observed_at"`

	ID          guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID   guid.ID   `gorm:"column:project_id;not null"`
	ExecutionID guid.ID   `gorm:"column:execution_id;not null"`
	Kind        string    `gorm:"column:kind;size:32;not null"`
	Title       string    `gorm:"column:title;size:200;not null"`
	Content     string    `gorm:"column:content;not null"`
	ContentHash string    `gorm:"column:content_hash;size:64;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Evidence) TableName() string { return "execution_evidence" }
