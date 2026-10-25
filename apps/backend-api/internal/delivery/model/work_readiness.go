package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ReadinessCheck preserves the preparation verdict for a particular work version.
type ReadinessCheck struct {
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	WorkItemVersion int       `gorm:"column:work_item_version;not null"`
	Trigger         string    `gorm:"column:trigger_kind;not null"`
	OK              bool      `gorm:"column:ok;not null"`
	Issues          string    `gorm:"column:issues;not null"`
	CheckedBy       string    `gorm:"column:checked_by;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ReadinessCheck) TableName() string { return "delivery_readiness_checks" }

// WorkItemDependency is an instance-local finish-to-start relation.
type WorkItemDependency struct {
	TenantID    guid.ID `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	WorkItemID  guid.ID `gorm:"column:work_item_id;primaryKey;autoIncrement:false;not null"`
	DependsOnID guid.ID `gorm:"column:depends_on_id;primaryKey;autoIncrement:false;not null"`
	ProjectID   guid.ID `gorm:"column:project_id;not null"`
}

func (WorkItemDependency) TableName() string { return "delivery_work_item_dependencies" }

// WorkItemTransition is immutable state history, including same-state checks.
type WorkItemTransition struct {
	FromStateKey    *string   `gorm:"column:from_state_key"`
	ToStateKey      *string   `gorm:"column:to_state_key"`
	Comment         *string   `gorm:"column:comment"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	FromState       *string   `gorm:"column:from_state"`
	ToState         string    `gorm:"column:to_state;not null"`
	Trigger         string    `gorm:"column:trigger_kind;not null"`
	ActorKind       string    `gorm:"column:actor_kind;not null"`
	ActorID         *guid.ID  `gorm:"column:actor_id"`
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	DecisionID      *guid.ID  `gorm:"column:decision_id"`
	ReasonCode      string    `gorm:"column:reason_code;not null"`
	SourceRef       *string   `gorm:"column:source_ref"`
	VersionAfter    int       `gorm:"column:version_after;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (WorkItemTransition) TableName() string { return "delivery_work_item_transitions" }
