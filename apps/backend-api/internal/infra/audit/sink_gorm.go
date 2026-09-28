package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"7link.com/platform/internal/infra/audit/model"
	"7link.com/platform/internal/infra/audit/query"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

var ErrInvalidEventID = errors.New("audit: invalid database event ID")

// OperationLog is the persisted form of an audit Event. It lives in the model
// package so the generated query code can reference it; the alias keeps the
// name audit code has always used.
type OperationLog = model.OperationLog

// GormSink persists audit events to the operation_logs table in batches wrapped
// in a single transaction.
type GormSink struct {
	db           *gorm.DB
	writerSource gormx.Writer

	// prefix is the deployment's table prefix. OperationLog.TableName is a
	// static method with no view of configuration, so the sink binds the
	// generated query to the prefixed table on every batch instead.
	prefix string
}

// NewGormSink returns a sink writing to db. The caller owns the migration; the
// sink does not auto-migrate.
func NewGormSink(db *gorm.DB, prefix string) *GormSink {
	return &GormSink{db: db, prefix: prefix}
}

// newRoutedGormSink retains the router capability and resolves a writer for
// every batch. It avoids pinning audit persistence to whichever writer happened
// to be selected during module construction.
func newRoutedGormSink(writerSource gormx.Writer, prefix string) *GormSink {
	return &GormSink{writerSource: writerSource, prefix: prefix}
}

func (sink *GormSink) Write(ctx context.Context, events []Event) error {
	if sink == nil || len(events) == 0 {
		return nil
	}
	db := sink.db
	if sink.writerSource != nil {
		db = sink.writerSource.Write()
		if db == nil {
			return ErrDatabaseSinkUnavailable
		}
	}
	if db == nil {
		return nil
	}
	// An event without a snowflake ID (the recorder's UUID fallback) has no
	// valid primary key. It is reported, but it must not cost the valid events
	// flushed in the same batch their persistence.
	rows := make([]OperationLog, 0, len(events))
	var invalid error
	for index, event := range events {
		if !isSnowflakeID(event.ID) {
			invalid = errors.Join(invalid, fmt.Errorf("%w %q at batch index %d", ErrInvalidEventID, event.ID, index))
			continue
		}
		rows = append(rows, fromEvent(event))
	}
	if len(rows) == 0 {
		return invalid
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return query.Bind(tx, sink.prefix).WithContext(ctx).Create(rowPointers(rows)...)
	}); err != nil {
		return errors.Join(err, invalid)
	}
	return invalid
}

// rowPointers adapts the batch to the generated Create signature, which takes
// pointers so it can write back generated values.
func rowPointers(rows []OperationLog) []*model.OperationLog {
	pointers := make([]*model.OperationLog, len(rows))
	for index := range rows {
		pointers[index] = &rows[index]
	}
	return pointers
}

// parseID converts a decimal-string snowflake ID to its guid.ID form. An empty
// or unparsable related-entity value maps to the zero ID. Event primary keys
// are validated by GormSink.Write before this mapping is called.
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
