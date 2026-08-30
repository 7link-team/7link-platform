package audit

import (
	"context"
	"encoding/json"
	"maps"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// operationLogsTable is the table backing the gorm sink. It must match the
// goose migration in sql/{sqlite,mysql,postgres}/00001_operation_logs.sql.
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

// GormSink persists audit events to the operation_logs table in batches wrapped
// in a single transaction.
type GormSink struct {
	db *gorm.DB
}

// NewGormSink returns a sink writing to db. The caller owns the migration; the
// sink does not auto-migrate.
func NewGormSink(db *gorm.DB) *GormSink {
	return &GormSink{db: db}
}

func (sink *GormSink) Write(ctx context.Context, events []Event) error {
	if sink == nil || sink.db == nil || len(events) == 0 {
		return nil
	}
	rows := make([]OperationLog, len(events))
	for index, event := range events {
		rows[index] = fromEvent(event)
	}
	return sink.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(&rows).Error
	})
}

// parseID converts a decimal-string snowflake ID to its guid.ID form. An empty
// or unparsable value (which should not happen for platform business IDs) maps
// to the zero ID; non-snowflake values are preserved verbatim by fromEvent.
func parseID(value string) guid.ID {
	id, _ := guid.Parse(value)
	return id
}

// preserveRawIDs keeps the verbatim form of any resource/actor/tenant
// identifier that is not a platform snowflake ID (external or non-numeric
// values) under a namespaced metadata key, so the database record never
// silently collapses it into the zero ID. The slog sink already carries the
// original string; this keeps both sinks lossless.
func preserveRawIDs(event Event, metadata map[string]string) {
	preserveRawID(metadata, "resource_id", event.ResourceID)
	preserveRawID(metadata, "actor_id", event.Actor.ID)
	preserveRawID(metadata, "tenant_id", event.Actor.TenantID)
}

func preserveRawID(metadata map[string]string, name, raw string) {
	if raw == "" {
		return
	}
	if _, err := guid.Parse(raw); err == nil {
		return // already persisted as a snowflake ID
	}
	metadata["raw_"+name] = raw
}

// fromEvent maps an Event to its persisted form.
func fromEvent(event Event) OperationLog {
	metadataFields := make(map[string]string, len(event.Metadata)+3)
	maps.Copy(metadataFields, event.Metadata)
	preserveRawIDs(event, metadataFields)
	metadata := ""
	if len(metadataFields) > 0 {
		if raw, err := json.Marshal(metadataFields); err == nil {
			metadata = string(raw)
		}
	}
	return OperationLog{
		ID:         parseID(event.ID),
		OccurredAt: event.OccurredAt,
		DurationMS: event.DurationMS,

		Entry:     string(event.Entry),
		Operation: event.Operation,
		Access:    string(event.Access),
		Action:    event.Action,

		ResourceType: event.ResourceType,
		ResourceID:   parseID(event.ResourceID),

		ActorID:   parseID(event.Actor.ID),
		ActorType: string(event.Actor.Type),
		ActorName: event.Actor.Name,
		TenantID:  parseID(event.Actor.TenantID),

		ClientIP:  event.Client.IP,
		UserAgent: event.Client.UserAgent,
		RequestID: event.Client.RequestID,
		SessionID: event.Client.SessionID,
		Host:      event.Client.Host,

		TargetMethod: event.Target.Method,
		TargetRoute:  event.Target.Route,

		Outcome:    string(event.Outcome),
		StatusCode: event.StatusCode,
		Code:       event.Code,
		Message:    event.Message,
		Error:      event.Error,

		RequestBody:      event.Request.Body,
		RequestSize:      event.Request.Size,
		RequestTruncated: event.Request.Truncated,
		RequestOmitted:   string(event.Request.Omitted),

		ResponseBody:      event.Response.Body,
		ResponseSize:      event.Response.Size,
		ResponseTruncated: event.Response.Truncated,
		ResponseOmitted:   string(event.Response.Omitted),

		TraceID:  event.TraceID,
		SpanID:   event.SpanID,
		Service:  event.Service,
		Version:  event.Version,
		Instance: event.Instance,

		Metadata:  metadata,
		CreatedAt: time.Now().UTC(),
	}
}
