package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Entry struct {
	TTLDays           int        `gorm:"column:ttl_days;not null;default:180"`
	ExpiresAt         *time.Time `gorm:"column:expires_at"`
	LastUsedAt        *time.Time `gorm:"column:last_used_at"`
	UseCount          int64      `gorm:"column:use_count;not null;default:0"`
	SourceExecutionID *guid.ID   `gorm:"column:source_execution_id"`
	SupersedesID      *guid.ID   `gorm:"column:supersedes_id"`
	RiskFlags         string     `gorm:"column:risk_flags;not null;default:[]"`
	Origin            string     `gorm:"column:origin;not null;default:HUMAN"`
	LiveSlot          *int       `gorm:"column:live_slot"`
	ID                guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID          guid.ID    `gorm:"column:tenant_id;not null"`
	Scope             string     `gorm:"column:scope;not null"`
	ProjectID         *guid.ID   `gorm:"column:project_id"`
	AgentPrincipalID  *guid.ID   `gorm:"column:agent_principal_id"`
	Kind              string     `gorm:"column:kind;not null"`
	State             string     `gorm:"column:state;not null"`
	Title             string     `gorm:"column:title;not null;size:200"`
	Body              string     `gorm:"column:body;not null"`
	ContentHash       string     `gorm:"column:content_hash;not null;size:64"`
	Provenance        string     `gorm:"column:provenance;not null;size:512"`
	Version           int        `gorm:"column:version;not null"`
	CreatedBy         guid.ID    `gorm:"column:created_by;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Entry) TableName() string { return "knowledge_entries" }

type Review struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	EntryID   guid.ID   `gorm:"column:entry_id;not null"`
	Action    string    `gorm:"column:action;not null"`
	FromState string    `gorm:"column:from_state;not null"`
	ToState   string    `gorm:"column:to_state;not null"`
	Reason    string    `gorm:"column:reason;not null;size:2000"`
	ActorID   guid.ID   `gorm:"column:actor_id;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (Review) TableName() string { return "knowledge_reviews" }
