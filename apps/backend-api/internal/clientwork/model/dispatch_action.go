package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// DispatchAction is immutable evidence of the disposition of an exact proposal.
// InputsDigest binds the frozen ranking; current authorization is checked again
// before every command and replay by the owning domain.
type DispatchAction struct {
	TenantID        guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID              guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID       guid.ID   `gorm:"column:project_id;not null"`
	ProposalID      guid.ID   `gorm:"column:proposal_id;not null"`
	ProposalVersion int       `gorm:"column:proposal_version;not null"`
	WorkItemID      guid.ID   `gorm:"column:work_item_id;not null"`
	WorkVersion     int       `gorm:"column:work_version;not null"`
	Action          string    `gorm:"column:action;not null"`
	InputsDigest    string    `gorm:"column:inputs_digest;not null"`
	SelectedID      *guid.ID  `gorm:"column:selected_id"`
	AssignmentID    *guid.ID  `gorm:"column:assignment_id"`
	ActorID         guid.ID   `gorm:"column:actor_id;not null"`
	AuthoritySource string    `gorm:"column:authority_source;not null"`
	Reason          string    `gorm:"column:reason;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (DispatchAction) TableName() string { return "clientwork_dispatch_actions" }
