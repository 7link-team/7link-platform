package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

type LoopContract struct {
	TenantID            guid.ID   `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID                  guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	ProjectID           *guid.ID  `gorm:"column:project_id"`
	AgentID             guid.ID   `gorm:"column:agent_id;not null"`
	OwnerPrincipalID    guid.ID   `gorm:"column:owner_principal_id;not null"`
	Kind                string    `gorm:"column:kind;not null"`
	Name                string    `gorm:"column:name;not null"`
	Anchor              string    `gorm:"column:anchor;not null"`
	ScheduleKind        string    `gorm:"column:schedule_kind;not null"`
	IntervalMinutes     *int      `gorm:"column:interval_minutes"`
	PeriodKind          *string   `gorm:"column:period_kind"`
	ReportBinding       *string   `gorm:"column:report_binding"`
	SelfChecks          string    `gorm:"column:self_checks;not null"`
	Budget              string    `gorm:"column:budget;not null"`
	Escalation          string    `gorm:"column:escalation;not null"`
	OutputFormat        string    `gorm:"column:output_format;not null"`
	AllowOverlap        bool      `gorm:"column:allow_overlap;not null"`
	State               string    `gorm:"column:state;not null"`
	ReminderID          *guid.ID  `gorm:"column:reminder_id"`
	ReminderFireOffset  int       `gorm:"column:reminder_fire_offset;not null"`
	ConsecutiveFailures int       `gorm:"column:consecutive_failures;not null"`
	Version             int       `gorm:"column:version;not null"`
	CreatedBy           guid.ID   `gorm:"column:created_by;not null"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
	UpdatedBy           guid.ID   `gorm:"column:updated_by;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;autoUpdateTime:false"`
}

func (LoopContract) TableName() string { return "planning_loop_contracts" }

type LoopRun struct {
	TenantID         guid.ID    `gorm:"column:tenant_id;primaryKey;autoIncrement:false;not null"`
	ID               guid.ID    `gorm:"column:id;primaryKey;autoIncrement:false;not null"`
	LoopContractID   guid.ID    `gorm:"column:loop_contract_id;not null"`
	FireNo           int        `gorm:"column:fire_no;not null"`
	ScheduledFor     time.Time  `gorm:"column:scheduled_for;not null"`
	State            string     `gorm:"column:state;not null"`
	ReportID         *guid.ID   `gorm:"column:report_id"`
	CheckResult      *string    `gorm:"column:check_result"`
	SelfCheckResults string     `gorm:"column:self_check_results;not null"`
	CostMicros       int64      `gorm:"column:cost_micros;not null"`
	OpenedAt         time.Time  `gorm:"column:opened_at;not null"`
	CompletedAt      *time.Time `gorm:"column:completed_at"`
	EscalationReason *string    `gorm:"column:escalation_reason"`
	Note             *string    `gorm:"column:note"`
}

func (LoopRun) TableName() string { return "planning_loop_runs" }
