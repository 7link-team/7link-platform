package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// EnsemblePlan owns comparison decisions; executions remain owned by execution.
type EnsemblePlan struct {
	BranchSnapshot      *string    `gorm:"column:branch_snapshot;type:text"`
	ApprovalRequestedAt *time.Time `gorm:"column:approval_requested_at"`
	ApprovedBy          *guid.ID   `gorm:"column:approved_by"`
	ApprovedAt          *time.Time `gorm:"column:approved_at"`
	SelectedBy          *guid.ID   `gorm:"column:selected_by"`
	SelectedAt          *time.Time `gorm:"column:selected_at"`

	ID                   guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID             guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID            guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID           guid.ID   `gorm:"column:work_item_id;not null"`
	State                string    `gorm:"column:state;size:32;not null"`
	Reason               string    `gorm:"column:reason;type:text;not null"`
	ComparisonJSON       string    `gorm:"column:comparison_json;type:text;not null"`
	SelectedBranchID     *guid.ID  `gorm:"column:selected_branch_id"`
	SynthesisExecutionID *guid.ID  `gorm:"column:synthesis_execution_id"`
	CreatedBy            guid.ID   `gorm:"column:created_by;not null"`
	Version              int       `gorm:"column:version;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt            time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (EnsemblePlan) TableName() string { return "fleet_ensemble_plans" }

type EnsembleBranch struct {
	ID          guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID    guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID   guid.ID    `gorm:"column:project_id;not null"`
	PlanID      guid.ID    `gorm:"column:plan_id;not null"`
	ExecutionID guid.ID    `gorm:"column:execution_id;not null"`
	ArchivedAt  *time.Time `gorm:"column:archived_at"`
}

func (EnsembleBranch) TableName() string { return "fleet_ensemble_branches" }
