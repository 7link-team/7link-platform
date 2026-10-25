package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// AutomationRule retains the creator independently of subsequent policy edits.
type AutomationRule struct {
	Definition string     `gorm:"column:definition;not null;default:''"`
	ArchivedAt *time.Time `gorm:"column:archived_at"`
	ID         guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID   guid.ID    `gorm:"column:tenant_id;not null"`
	ProjectID  guid.ID    `gorm:"column:project_id;not null"`
	CreatedBy  guid.ID    `gorm:"column:created_by;not null"`
	Name       string     `gorm:"column:name;size:80;not null"`
	Version    int        `gorm:"column:version;not null"`
	Enabled    bool       `gorm:"column:enabled;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (AutomationRule) TableName() string { return "delivery_automation_rules" }

// AutomationRun is append only. No mutation command is exposed for receipts.
type AutomationRun struct {
	RunAs              string    `gorm:"column:run_as;size:100;not null;default:''"`
	ActionsResult      string    `gorm:"column:actions_result;size:3000;not null;default:'[]'"`
	TriggerKey         *string   `gorm:"column:trigger_key;size:160"`
	DefinitionSnapshot string    `gorm:"column:definition_snapshot;not null;default:''"`
	ID                 guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	TenantID           guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID          guid.ID   `gorm:"column:project_id;not null"`
	RuleID             guid.ID   `gorm:"column:rule_id;not null"`
	RuleVersion        int       `gorm:"column:rule_version;not null"`
	RuleName           string    `gorm:"column:rule_name;size:80;not null"`
	CreatedBy          guid.ID   `gorm:"column:created_by;not null"`
	TriggerEventID     guid.ID   `gorm:"column:trigger_event_id;not null"`
	WorkItemID         guid.ID   `gorm:"column:work_item_id;not null"`
	Outcome            string    `gorm:"column:outcome;size:16;not null"`
	Error              string    `gorm:"column:error;size:500;not null"`
	DurationMS         int64     `gorm:"column:duration_ms;not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (AutomationRun) TableName() string { return "delivery_automation_runs" }
