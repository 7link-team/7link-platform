package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type Settings struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ValueJSON string    `gorm:"column:value_json;not null"`
	Version   int       `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (Settings) TableName() string { return "governance_settings" }

type EscalationRule struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	Kind      string    `gorm:"column:kind;size:48;not null"`
	ValueJSON string    `gorm:"column:value_json;not null"`
	Version   int       `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (EscalationRule) TableName() string { return "governance_escalation_rules" }

// ProjectEscalationRule adds a stricter floor to the tenant's current rule.
type ProjectEscalationRule struct {
	ID        guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID  guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID guid.ID   `gorm:"column:project_id;not null"`
	Kind      string    `gorm:"column:kind;size:48;not null"`
	ValueJSON string    `gorm:"column:value_json;not null"`
	Version   int       `gorm:"column:version;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (ProjectEscalationRule) TableName() string { return "governance_project_escalation_rules" }

type EscalationIncident struct {
	ID             guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID       guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID      guid.ID    `gorm:"column:project_id;not null"`
	Kind           string     `gorm:"column:kind;size:48;not null"`
	SubjectKind    string     `gorm:"column:subject_kind;size:32;not null"`
	SubjectID      guid.ID    `gorm:"column:subject_id;not null"`
	TriggerEventID guid.ID    `gorm:"column:trigger_event_id;not null"`
	Reason         string     `gorm:"column:reason;size:2000;not null"`
	FactsJSON      string     `gorm:"column:facts_json;not null"`
	State          string     `gorm:"column:state;size:32;not null"`
	OpenKey        *string    `gorm:"column:open_key;size:160"`
	Version        int        `gorm:"column:version;not null"`
	HandledBy      *guid.ID   `gorm:"column:handled_by"`
	HandledAt      *time.Time `gorm:"column:handled_at"`
	ResolutionNote *string    `gorm:"column:resolution_note;size:2000"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (EscalationIncident) TableName() string { return "governance_escalation_incidents" }
