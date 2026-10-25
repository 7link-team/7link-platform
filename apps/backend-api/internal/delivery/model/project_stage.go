package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ProjectStageTransition is an immutable authorized stage-change receipt.
type ProjectStageTransition struct {
	TenantID         guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false" json:"-"`
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	ProjectID        guid.ID   `gorm:"column:project_id;not null" json:"project_id"`
	FromStage        string    `gorm:"column:from_stage;not null" json:"from_stage"`
	ToStage          string    `gorm:"column:to_stage;not null" json:"to_stage"`
	CriteriaSnapshot string    `gorm:"column:criteria_snapshot;not null" json:"-"`
	BlockedKind      *string   `gorm:"column:blocked_kind" json:"blocked_kind"`
	ScopeChangeID    *guid.ID  `gorm:"column:scope_change_id" json:"scope_change_id"`
	DecisionID       *guid.ID  `gorm:"column:decision_id" json:"decision_id"`
	Reason           string    `gorm:"column:reason;not null" json:"reason"`
	ActorKind        string    `gorm:"column:actor_kind;not null" json:"actor_kind"`
	ActorID          guid.ID   `gorm:"column:actor_id;not null" json:"actor_id"`
	AuthoritySource  string    `gorm:"column:authority_source;not null" json:"authority_source"`
	VersionAfter     int       `gorm:"column:version_after;not null" json:"version_after"`
	OccurredAt       time.Time `gorm:"column:occurred_at;not null;autoCreateTime:false" json:"occurred_at"`
}

func (ProjectStageTransition) TableName() string { return "delivery_project_stage_transitions" }
