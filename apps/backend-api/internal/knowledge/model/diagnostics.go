package model

import (
	"7link.com/platform/internal/infra/guid"
	"time"
)

// DiagnosticLog stores an immutable diagnostic snapshot; it is never runtime usage.
type DiagnosticLog struct {
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	ActorID          guid.ID   `gorm:"column:actor_id;not null"`
	Scope            string    `gorm:"column:scope;not null;size:16"`
	ProjectID        *guid.ID  `gorm:"column:project_id"`
	AgentPrincipalID *guid.ID  `gorm:"column:agent_principal_id"`
	QueryText        string    `gorm:"column:query_text;not null;size:500"`
	ResultJSON       string    `gorm:"column:result_json;not null"`
	LatencyMS        float64   `gorm:"column:latency_ms;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (DiagnosticLog) TableName() string { return "knowledge_diagnostic_logs" }

// RuntimeMetricSample is accepted only after the runtime owner verifies its frozen fact.
type RuntimeMetricSample struct {
	AgentPrincipalID *guid.ID  `gorm:"column:agent_principal_id"`
	ID               guid.ID   `gorm:"column:id;primaryKey;autoIncrement:false"`
	TenantID         guid.ID   `gorm:"column:tenant_id;not null"`
	ProjectID        *guid.ID  `gorm:"column:project_id"`
	Kind             string    `gorm:"column:kind;not null;size:24"`
	ExecutionID      *guid.ID  `gorm:"column:execution_id"`
	EvidenceID       guid.ID   `gorm:"column:evidence_id;not null"`
	EntryID          *guid.ID  `gorm:"column:entry_id"`
	Hit              bool      `gorm:"column:hit;not null"`
	LatencyMS        *float64  `gorm:"column:latency_ms"`
	Outcome          *string   `gorm:"column:outcome;size:16"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;autoCreateTime:false"`
}

func (RuntimeMetricSample) TableName() string { return "knowledge_runtime_metric_samples" }
