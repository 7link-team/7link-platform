package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// AutomatedDecision is a command receipt, never evidence that a review passed.
type AutomatedDecision struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	ClaimID         guid.ID   `gorm:"column:claim_id;not null"`
	TriggerEventID  guid.ID   `gorm:"column:trigger_event_id;not null"`
	State           string    `gorm:"column:state;not null"`
	Reason          string    `gorm:"column:reason;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	DecisionID      guid.ID   `gorm:"column:decision_id;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (AutomatedDecision) TableName() string { return "governance_automated_decisions" }
