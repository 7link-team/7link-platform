package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// VerificationTask pins a platform command independently of model receipts.
type VerificationTask struct {
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID        guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID       guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID    `gorm:"column:work_item_id;not null"`
	ExecutionID     guid.ID    `gorm:"column:execution_id;not null"`
	ResultID        guid.ID    `gorm:"column:result_id;not null"`
	CriterionID     guid.ID    `gorm:"column:criterion_id;not null"`
	ResultVersion   int        `gorm:"column:result_version;not null"`
	MachineID       guid.ID    `gorm:"column:machine_id;not null"`
	SessionID       guid.ID    `gorm:"column:session_id;not null"`
	ConnEpoch       int64      `gorm:"column:conn_epoch;not null"`
	State           string     `gorm:"column:state;size:32;not null"`
	PlanJSON        string     `gorm:"column:plan_json;not null"`
	PlanHash        string     `gorm:"column:plan_hash;size:64;not null"`
	ObservationJSON string     `gorm:"column:observation_json;not null"`
	ReceiptHash     string     `gorm:"column:receipt_hash;size:64;not null"`
	LeaseExpiresAt  *time.Time `gorm:"column:lease_expires_at"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (VerificationTask) TableName() string { return "execution_verification_tasks" }
