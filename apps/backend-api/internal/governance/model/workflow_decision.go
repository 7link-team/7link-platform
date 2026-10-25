package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// WorkflowDecision is append-only and bound to one exact pinned node expansion.
type WorkflowDecision struct {
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID        guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	BindingID       guid.ID   `gorm:"column:binding_id;not null"`
	DefinitionHash  string    `gorm:"column:definition_hash;size:64;not null"`
	ExpansionKey    string    `gorm:"column:expansion_key;size:512;not null"`
	Gate            string    `gorm:"column:gate;size:16;not null"`
	Outcome         string    `gorm:"column:outcome;size:16;not null"`
	Reason          string    `gorm:"column:reason;size:2000;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	AuthoritySource string    `gorm:"column:authority_source;size:160;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (WorkflowDecision) TableName() string { return "governance_workflow_decisions" }
