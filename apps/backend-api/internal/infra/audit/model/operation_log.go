// Package model holds audit's persisted row type. It is a package of its own
// so the generated query code can reference the row without importing audit,
// which would close an import cycle back through the query package.
package model

import (
	"time"

	"7link.com/platform/internal/infra/guid"
)

// operationLogsTable is the base table name, before any deployment prefix.
const operationLogsTable = "operation_logs"

// OperationLog is the persisted form of an audit Event. Payloads are stored
// flat so the table can be queried without JSON extraction.
type OperationLog struct {
	// ID is the platform snowflake ID (guid.ID), consistent with every business
	// table primary key. Stored as BIGINT; the event's decimal-string form is
	// parsed back into it by fromEvent.
	ID         guid.ID   `gorm:"primaryKey" json:"id"`
	OccurredAt time.Time `gorm:"not null" json:"occurred_at"`
	DurationMS float64   `json:"duration_ms"`

	Entry     string `gorm:"size:32;not null" json:"entry"`
	Operation string `gorm:"size:255;not null" json:"operation"`
	Access    string `gorm:"size:16;not null" json:"access"`
	Action    string `gorm:"size:128" json:"action"`

	ResourceType string  `gorm:"size:128" json:"resource_type"`
	ResourceID   guid.ID `json:"resource_id"`

	ActorID   guid.ID `json:"actor_id"`
	ActorType string  `gorm:"size:32" json:"actor_type"`
	ActorName string  `gorm:"size:255" json:"actor_name"`
	TenantID  guid.ID `json:"tenant_id"`

	ClientIP  string `gorm:"size:64" json:"client_ip"`
	UserAgent string `gorm:"size:512" json:"user_agent"`
	RequestID string `gorm:"size:128" json:"request_id"`
	SessionID string `gorm:"size:128" json:"session_id"`
	Host      string `gorm:"size:255" json:"host"`

	TargetMethod string `gorm:"size:32" json:"target_method"`
	TargetRoute  string `gorm:"size:512" json:"target_route"`

	Outcome    string `gorm:"size:16;not null" json:"outcome"`
	StatusCode int    `json:"status_code"`
	Code       int    `json:"code"`
	Message    string `gorm:"size:512" json:"message"`
	Error      string `gorm:"type:text" json:"error"`

	RequestBody      string `gorm:"type:text" json:"request_body"`
	RequestSize      int64  `json:"request_size"`
	RequestTruncated bool   `json:"request_truncated"`
	RequestOmitted   string `gorm:"size:64" json:"request_omitted"`

	ResponseBody      string `gorm:"type:text" json:"response_body"`
	ResponseSize      int64  `json:"response_size"`
	ResponseTruncated bool   `json:"response_truncated"`
	ResponseOmitted   string `gorm:"size:64" json:"response_omitted"`

	TraceID  string `gorm:"size:64" json:"trace_id"`
	SpanID   string `gorm:"size:32" json:"span_id"`
	Service  string `gorm:"size:128" json:"service"`
	Version  string `gorm:"size:64" json:"version"`
	Instance string `gorm:"size:128" json:"instance"`

	Metadata  string    `gorm:"type:text" json:"metadata"`
	CreatedAt time.Time `json:"created_at"`
}

func (OperationLog) TableName() string { return operationLogsTable }
