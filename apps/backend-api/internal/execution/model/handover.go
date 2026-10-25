package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// HandoverArtifact is immutable. SYSTEM documents deliberately have NULL
// ExecutionID, preserving the distinction between source facts and model output.
type HandoverArtifact struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	RetirementID     guid.ID   `gorm:"column:retirement_id;not null"`
	AgentPrincipalID guid.ID   `gorm:"column:agent_principal_id;not null"`
	ExecutionID      *guid.ID  `gorm:"column:execution_id"`
	Source           string    `gorm:"column:source;size:16;not null"`
	Content          string    `gorm:"column:content;type:text;not null"`
	SHA256           string    `gorm:"column:sha256;size:64;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (HandoverArtifact) TableName() string { return "execution_handover_artifacts" }
