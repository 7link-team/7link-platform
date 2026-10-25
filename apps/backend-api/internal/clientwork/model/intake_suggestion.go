package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// IntakeSuggestion is an immutable internal Agent/Human proposal. It never
// changes the source, sends client messages or grants conversion authority.
type IntakeSuggestion struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	IntakeID      guid.ID   `gorm:"column:intake_id;not null"`
	IntakeVersion int       `gorm:"column:intake_version;not null"`
	Proposal      string    `gorm:"column:proposal;not null"`
	ActorKind     string    `gorm:"column:actor_kind;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (IntakeSuggestion) TableName() string { return "clientwork_intake_suggestions" }
