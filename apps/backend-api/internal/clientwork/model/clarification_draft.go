package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClarificationDraft is internal. Publishing creates a separate client-safe
// round, preserving the draft's exact author, source version and question bytes.
type ClarificationDraft struct {
	ID            guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID      guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID     guid.ID   `gorm:"column:project_id;not null"`
	IntakeID      guid.ID   `gorm:"column:intake_id;not null"`
	IntakeVersion int       `gorm:"column:intake_version;not null"`
	Questions     string    `gorm:"column:questions;not null"`
	ActorKind     string    `gorm:"column:actor_kind;not null"`
	ActorID       guid.ID   `gorm:"column:actor_id;not null"`
	Version       int       `gorm:"column:version;not null"`
	SentRoundID   *guid.ID  `gorm:"column:sent_round_id"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (ClarificationDraft) TableName() string { return "clientwork_clarification_drafts" }
