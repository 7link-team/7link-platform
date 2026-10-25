package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// Assignment is ownership with a lease, not a result Claim or an Execution.
type Assignment struct {
	TenantID            guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                  guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID           guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID          guid.ID    `gorm:"column:work_item_id;not null"`
	Role                string     `gorm:"column:role;not null"`
	AssigneeKind        string     `gorm:"column:assignee_kind;not null"`
	AssigneePrincipalID guid.ID    `gorm:"column:assignee_principal_id;not null"`
	AgentID             *guid.ID   `gorm:"column:agent_id"`
	ActiveSlot          *guid.ID   `gorm:"column:active_slot"`
	Status              string     `gorm:"column:status;not null"`
	LeaseTTLMS          int64      `gorm:"column:lease_ttl_ms;not null"`
	LeaseExpiresAt      *time.Time `gorm:"column:lease_expires_at"`
	LeaseRenewedAt      *time.Time `gorm:"column:lease_renewed_at"`
	DecisionID          *guid.ID   `gorm:"column:decision_id"`
	EndedAt             *time.Time `gorm:"column:ended_at"`
	EndReason           *string    `gorm:"column:end_reason"`
	EnsemblePlanID      *guid.ID   `gorm:"column:ensemble_plan_id"`
	Version             int        `gorm:"column:version;not null"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
	CreatedBy           *guid.ID   `gorm:"column:created_by"`
	UpdatedBy           *guid.ID   `gorm:"column:updated_by"`
}

func (Assignment) TableName() string { return "delivery_assignments" }
