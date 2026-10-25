package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// CommandDecision is an immutable domain action, separate from its proposal or
// exception. SourceID binds the exact source version used by the command.
type CommandDecision struct {
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	TargetKind      string    `gorm:"column:target_kind;not null"`
	TargetID        guid.ID   `gorm:"column:target_id;not null"`
	Action          string    `gorm:"column:action;not null"`
	SourceID        guid.ID   `gorm:"column:source_id;not null"`
	SourceVersion   int       `gorm:"column:source_version;not null"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	Reason          string    `gorm:"column:reason;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (CommandDecision) TableName() string { return "clientwork_command_decisions" }
