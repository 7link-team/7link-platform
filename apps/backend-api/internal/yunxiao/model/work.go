package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Worklog struct {
	Timezone    *string    `gorm:"column:timezone"`
	ConfirmedBy *guid.ID   `gorm:"column:confirmed_by"`
	ConfirmedAt *time.Time `gorm:"column:confirmed_at"`
	WorkItemID  *guid.ID   `gorm:"column:work_item_id"`
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID   guid.ID    `gorm:"column:project_id;not null"`
	PrincipalID guid.ID    `gorm:"column:principal_id;not null"`
	ActorKind   string     `gorm:"column:actor_kind;size:8;not null"`
	Category    string     `gorm:"column:category;size:32;not null"`
	WorkDate    string     `gorm:"column:work_date;size:10;not null"`
	Minutes     int64      `gorm:"column:minutes;not null"`
	CostMicros  *int64     `gorm:"column:cost_micros"`
	ExecutionID *guid.ID   `gorm:"column:execution_id"`
	Note        string     `gorm:"column:note;size:500;not null"`
	State       string     `gorm:"column:state;size:16;not null"`
	ApprovedBy  *guid.ID   `gorm:"column:approved_by"`
	ApprovedAt  *time.Time `gorm:"column:approved_at"`
	Version     int        `gorm:"column:version;not null"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Worklog) TableName() string { return "delivery_worklogs" }

// WorklogRevision is an immutable observation of an exact worklog version.
// LEGACY_OBSERVED explicitly lacks the original action's actor/time provenance.
type WorklogRevision struct {
	ID         guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID   guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID  guid.ID   `gorm:"column:project_id;not null"`
	WorklogID  guid.ID   `gorm:"column:worklog_id;not null"`
	Version    int       `gorm:"column:version;not null"`
	Snapshot   string    `gorm:"column:snapshot;not null"`
	Action     string    `gorm:"column:action;not null"`
	Reason     string    `gorm:"column:reason;not null"`
	ActorID    *guid.ID  `gorm:"column:actor_id"`
	RecordedAt time.Time `gorm:"column:recorded_at;not null;autoCreateTime:false"`
}

func (WorklogRevision) TableName() string { return "delivery_worklog_revisions" }

type WorkflowConfig struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID    guid.ID   `gorm:"column:project_id;not null"`
	WorkItemType string    `gorm:"column:work_item_type;size:16;not null"`
	Document     string    `gorm:"column:document;not null"`
	Version      int       `gorm:"column:version;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	UpdatedBy    guid.ID   `gorm:"column:updated_by;not null"`
}

func (WorkflowConfig) TableName() string { return "delivery_workflow_configs" }
