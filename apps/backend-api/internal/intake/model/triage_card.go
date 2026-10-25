package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// TriageCard freezes an internal assessment or an assessed client change.
// New assessments produce new revisions; published cards are never overwritten.
type TriageCard struct {
	ID           guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID     guid.ID   `gorm:"column:tenant_id;not null"`
	IntakeID     guid.ID   `gorm:"column:intake_id;not null"`
	Revision     int       `gorm:"column:revision;not null"`
	ChangeID     *guid.ID  `gorm:"column:change_id"`
	ProposalHash string    `gorm:"column:proposal_hash;size:64;not null"`
	ProposalJSON string    `gorm:"column:proposal_json;not null"`
	ImpactJSON   string    `gorm:"column:impact_json;not null"`
	ActorID      guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (TriageCard) TableName() string { return "intake_triage_cards" }
