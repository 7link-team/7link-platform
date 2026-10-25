package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// DispatchProposal freezes the inputs used for one reproducible recommendation.
type DispatchProposal struct {
	TenantID      guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID            guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID     guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID    guid.ID    `gorm:"column:work_item_id;not null"`
	ActiveSlot    *guid.ID   `gorm:"column:active_slot"`
	WorkVersion   int        `gorm:"column:work_version;not null"`
	Trigger       string     `gorm:"column:trigger;not null"`
	State         string     `gorm:"column:state;not null"`
	Result        string     `gorm:"column:result;not null"`
	Snapshot      string     `gorm:"column:snapshot;not null"`
	InputsDigest  string     `gorm:"column:inputs_digest;not null"`
	RecommendedID *guid.ID   `gorm:"column:recommended_id"`
	AppliedID     *guid.ID   `gorm:"column:applied_id"`
	AssignmentID  *guid.ID   `gorm:"column:assignment_id"`
	Reason        *string    `gorm:"column:reason"`
	DecidedBy     *guid.ID   `gorm:"column:decided_by"`
	ExpiresAt     time.Time  `gorm:"column:expires_at;not null"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	DecidedAt     *time.Time `gorm:"column:decided_at"`
	Version       int        `gorm:"column:version;not null"`
}

func (DispatchProposal) TableName() string { return "clientwork_dispatch_proposals" }
