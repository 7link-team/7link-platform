package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// InjectionLog freezes ranked reference bytes before execution starts. Updates
// only record source-verified inclusion/citation facts, never rewrite selection.
type InjectionLog struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID        guid.ID   `gorm:"column:project_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	ExecutionID      guid.ID   `gorm:"column:execution_id;not null"`
	WorkItemID       guid.ID   `gorm:"column:work_item_id;not null"`
	ResultJSON       string    `gorm:"column:result_json;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (InjectionLog) TableName() string { return "knowledge_injection_logs" }
