package audit

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openTestDB opens an in-memory SQLite database for the gorm sink tests.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestGormSinkPersistsEvents(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, Migrate(t.Context(), db))

	sink := NewGormSink(db)
	occurredAt := time.Now().UTC().Truncate(time.Millisecond)
	event := Event{
		ID:           "1001",
		OccurredAt:   occurredAt,
		DurationMS:   3.25,
		Entry:        KindHTTP,
		Operation:    "POST /orders",
		Access:       AccessWrite,
		Action:       "order.create",
		ResourceType: "order",
		ResourceID:   "42",
		Actor:        Actor{ID: "2001", Type: ActorUser, Name: "alice", TenantID: "2002"},
		Client:       Client{IP: "10.0.0.1", UserAgent: "curl", RequestID: "r1", SessionID: "s1"},
		Target:       Target{Method: "POST", Route: "/orders"},
		Outcome:      OutcomeSuccess,
		StatusCode:   201,
		Code:         0,
		Message:      "ok",
		Error:        "",
		Request:      Payload{Body: `{"a":1}`, Size: 7, Omitted: OmittedNone},
		Response:     Payload{Size: 12, Omitted: OmittedSuccessSummary},
		TraceID:      "trace-1",
		SpanID:       "span-1",
		Service:      "svc",
		Version:      "1",
		Instance:     "inst",
		Metadata:     map[string]string{"region": "cn"},
	}
	require.NoError(t, sink.Write(t.Context(), []Event{event, {
		ID: "1002", OccurredAt: occurredAt, Entry: KindJob, Operation: "job.x", Access: AccessWrite, Outcome: OutcomeFailure,
	}}))

	var rows []OperationLog
	require.NoError(t, db.Where("id IN ?", []int64{1001, 1002}).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)

	row := rows[0]
	assert.Equal(t, guid.ID(1001), row.ID)
	assert.Equal(t, "POST /orders", row.Operation)
	assert.Equal(t, "order", row.ResourceType)
	assert.Equal(t, guid.ID(42), row.ResourceID)
	assert.Equal(t, guid.ID(2001), row.ActorID)
	assert.Equal(t, guid.ID(2002), row.TenantID)
	assert.Equal(t, "alice", row.ActorName)
	assert.Equal(t, "10.0.0.1", row.ClientIP)
	assert.Equal(t, "success", row.Outcome)
	assert.Equal(t, `{"a":1}`, row.RequestBody)
	assert.Equal(t, int64(7), row.RequestSize)
	assert.Equal(t, "success_summary", row.ResponseOmitted)
	assert.Equal(t, "trace-1", row.TraceID)
	assert.Equal(t, `{"region":"cn"}`, row.Metadata)
	assert.False(t, row.CreatedAt.IsZero())
}

func TestGormSinkNilSinkAndEmptyBatch(t *testing.T) {
	var sink *GormSink
	require.NoError(t, sink.Write(t.Context(), []Event{{}}))
	require.NoError(t, NewGormSink(nil).Write(t.Context(), []Event{{}}))
	db := openTestDB(t)
	require.NoError(t, NewGormSink(db).Write(t.Context(), nil))
}

func TestFromEventMetadataEncoding(t *testing.T) {
	row := fromEvent(Event{Metadata: map[string]string{"a": "1"}})
	assert.Equal(t, `{"a":"1"}`, row.Metadata)
	row = fromEvent(Event{})
	assert.Equal(t, "", row.Metadata)
	assert.Equal(t, operationLogsTable, row.TableName())
}

func TestFromEventPreservesNonSnowflakeIDs(t *testing.T) {
	// Snowflake IDs are stored in the numeric columns; non-numeric identifiers
	// (external or opaque values) must be preserved verbatim, not collapsed to 0.
	row := fromEvent(Event{
		ResourceID: "ext-order-uuid",
		Actor:      Actor{ID: "u1", Type: ActorUser, TenantID: "9"},
		Metadata:   map[string]string{"region": "cn"},
	})
	assert.Equal(t, guid.ID(0), row.ResourceID)
	assert.Equal(t, guid.ID(0), row.ActorID)
	assert.Equal(t, guid.ID(9), row.TenantID)
	assert.Contains(t, row.Metadata, `"raw_resource_id":"ext-order-uuid"`)
	assert.Contains(t, row.Metadata, `"raw_actor_id":"u1"`)
	assert.Contains(t, row.Metadata, `"region":"cn"`)
	assert.NotContains(t, row.Metadata, `raw_tenant_id`)
}
