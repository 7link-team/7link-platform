package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// AttentionSession records measured Human activity, never elapsed review age.
type AttentionSession struct {
	TenantID        guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false"`
	ID              guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	ProjectID       guid.ID    `gorm:"column:project_id;not null"`
	WorkItemID      guid.ID    `gorm:"column:work_item_id;not null"`
	ClaimID         *guid.ID   `gorm:"column:claim_id"`
	InteractionID   *guid.ID   `gorm:"column:interaction_id"`
	PrincipalID     guid.ID    `gorm:"column:principal_id;not null"`
	Surface         string     `gorm:"column:surface;not null"`
	StartedAt       time.Time  `gorm:"column:started_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	LastHeartbeatAt time.Time  `gorm:"column:last_heartbeat_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	EndedAt         *time.Time `gorm:"column:ended_at;autoCreateTime:false;autoUpdateTime:false"`
	ActiveMS        int64      `gorm:"column:active_ms;not null"`
	LastSequence    int64      `gorm:"column:last_sequence;not null"`
	DecisionID      *guid.ID   `gorm:"column:decision_id"`
}

func (AttentionSession) TableName() string { return "delivery_attention_sessions" }
