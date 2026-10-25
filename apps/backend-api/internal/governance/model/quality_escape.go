package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// QualityEscape preserves the source event and immutable gate facts; only the
// human learning decision and delivery-owned draft references may change.
type QualityEscape struct {
	LearningDraft  *string    `gorm:"column:learning_draft" json:"-"`
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null" json:"tenant_id"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null" json:"project_id"`
	WorkItemID     guid.ID    `gorm:"column:work_item_id;not null" json:"work_item_id"`
	SourceEventID  guid.ID    `gorm:"column:source_event_id;not null" json:"source_event_id"`
	EscapeKey      string     `gorm:"column:escape_key;not null" json:"escape_key"`
	Stage          string     `gorm:"column:stage;not null" json:"stage"`
	Category       string     `gorm:"column:category;not null" json:"category"`
	Summary        string     `gorm:"column:summary;not null" json:"summary"`
	PassedBy       string     `gorm:"column:passed_by;not null" json:"-"`
	LearningState  string     `gorm:"column:learning_state;not null" json:"learning_state"`
	MemoryIDs      string     `gorm:"column:memory_ids;not null" json:"-"`
	EvalCaseRef    *string    `gorm:"column:eval_case_ref" json:"eval_case_ref,omitempty"`
	DecisionReason *string    `gorm:"column:decision_reason" json:"decision_reason,omitempty"`
	DecidedBy      *guid.ID   `gorm:"column:decided_by" json:"decided_by,omitempty"`
	DecidedAt      *time.Time `gorm:"column:decided_at" json:"decided_at,omitempty"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	Version        int        `gorm:"column:version;not null" json:"version"`
}

func (QualityEscape) TableName() string { return "governance_quality_escapes" }
