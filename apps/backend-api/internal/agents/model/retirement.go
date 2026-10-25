package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// Retirement persists protocol progress independently of Fleet's runtime state.
// ActiveAgentID is NULL in terminal records, allowing a portable unique index.
type Retirement struct {
	ID               guid.ID            `gorm:"column:id;primaryKey;autoIncrement:false" json:"id"`
	TenantID         guid.ID            `gorm:"column:tenant_id;not null" json:"-"`
	AgentPrincipalID guid.ID            `gorm:"column:agent_principal_id;not null" json:"agent_principal_id"`
	ActiveAgentID    *guid.ID           `gorm:"column:active_agent_id" json:"-"`
	Mode             string             `gorm:"column:mode;size:16;not null" json:"mode"`
	State            string             `gorm:"column:state;size:16;not null" json:"state"`
	Step             string             `gorm:"column:step;size:16;not null" json:"step"`
	Reason           string             `gorm:"column:reason;size:2000;not null" json:"reason"`
	Precheck         RetirementPrecheck `gorm:"column:precheck;serializer:json;type:text;not null" json:"precheck"`
	AgentVersion     int                `gorm:"column:agent_version;not null" json:"agent_version"`
	Version          int                `gorm:"column:version;not null" json:"version"`
	CreatedBy        guid.ID            `gorm:"column:created_by;not null" json:"created_by"`
	CreatedAt        time.Time          `gorm:"column:created_at;not null;autoCreateTime:false" json:"created_at"`
	StepStartedAt    time.Time          `gorm:"column:step_started_at;not null;autoCreateTime:false;autoUpdateTime:false" json:"step_started_at"`
	CompletedAt      *time.Time         `gorm:"column:completed_at" json:"completed_at,omitempty"`
}

func (Retirement) TableName() string { return "agents_retirements" }

// The retirement manifest freezes the source-owned work/version facts before
// runtime retirement begins. Unknown source facts must never be encoded as zero.
type RetirementPrecheck struct {
	LeadProjectIDs []guid.ID             `json:"lead_project_ids,omitempty"`
	Machine        *RetirementMachine    `json:"machine,omitempty"`
	Progress       *RetirementProgress   `json:"progress,omitempty"`
	Lost           *RetirementLoss       `json:"lost,omitempty"`
	WorkItems      []RetirementWorkItem  `json:"work_items"`
	Executions     []RetirementExecution `json:"executions"`
}

// Progress is stored with the frozen source manifest; only protocol commands
// change it. IDs identify genuine source-owned records, never synthetic evidence.
type RetirementProgress struct {
	SourceWorkItems      []RetirementSourceWork     `json:"source_work_items,omitempty"`
	SourceRuleIDs        []guid.ID                  `json:"source_rule_ids,omitempty"`
	ReconciliationStatus string                     `json:"reconciliation_status,omitempty"`
	HandoverErrorCode    string                     `json:"handover_error_code,omitempty"`
	HandoverExecutionID  *guid.ID                   `json:"handover_execution_id,omitempty"`
	HandoverArtifactID   *guid.ID                   `json:"handover_artifact_id,omitempty"`
	HandoverSource       string                     `json:"handover_source,omitempty"`
	HandoverRetryCount   int                        `json:"handover_retry_count"`
	SuccessorKind        string                     `json:"successor_kind,omitempty"`
	SuccessorPrincipalID *guid.ID                   `json:"successor_principal_id,omitempty"`
	CleanupStatus        string                     `json:"cleanup_status,omitempty"`
	ReassignResult       []RetirementTransferResult `json:"reassign_result,omitempty"`
}

type RetirementSourceWork struct {
	ProjectID  guid.ID `json:"project_id"`
	WorkItemID guid.ID `json:"work_item_id"`
}

type RetirementTransferResult struct {
	WorkItemID   guid.ID  `json:"work_item_id"`
	State        string   `json:"state"`
	Code         string   `json:"code,omitempty"`
	AssignmentID *guid.ID `json:"assignment_id,omitempty"`
}

type RetirementLoss struct {
	OpenItems         int `json:"open_items"`
	RunningExecutions int `json:"running_executions"`
}

type RetirementExecution struct {
	ID         guid.ID `json:"id"`
	ProjectID  guid.ID `json:"project_id"`
	WorkItemID guid.ID `json:"work_item_id"`
	State      string  `json:"state"`
	Version    int     `json:"version"`
}

type RetirementMachine struct {
	MachineID     *guid.ID `json:"machine_id,omitempty"`
	MachineState  string   `json:"machine_state"`
	ProcessStatus string   `json:"process_status"`
}

type RetirementWorkItem struct {
	BlockReasons      string  `json:"block_reasons"`
	BlockNote         *string `json:"block_note,omitempty"`
	AssignmentID      guid.ID `json:"assignment_id"`
	ProjectID         guid.ID `json:"project_id"`
	WorkItemID        guid.ID `json:"work_item_id"`
	WorkVersion       int     `json:"work_version"`
	AssignmentVersion int     `json:"assignment_version"`
	Title             string  `json:"title"`
	State             string  `json:"state"`
}
