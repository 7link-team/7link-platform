package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// ClaimEvaluation is the immutable escalation receipt for an exact completion
// claim. It is committed in the same transaction as any incidents it triggered.
type ClaimEvaluation struct {
	ID                  guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID            guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID           guid.ID   `gorm:"column:project_id;not null"`
	WorkItemID          guid.ID   `gorm:"column:work_item_id;not null"`
	ClaimID             guid.ID   `gorm:"column:claim_id;not null"`
	TriggerEventID      guid.ID   `gorm:"column:trigger_event_id;not null"`
	LowConfidenceResult string    `gorm:"column:low_confidence_result;not null"`
	SensitivePathResult string    `gorm:"column:sensitive_path_result;not null"`
	EvaluatedAt         time.Time `gorm:"column:evaluated_at;not null;autoCreateTime:false"`
}

func (ClaimEvaluation) TableName() string { return "governance_claim_evaluations" }
