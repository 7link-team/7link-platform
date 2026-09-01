package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogSinkWritesFullEvent(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	slog.SetDefault(logger)

	sink := NewSlogSink()
	event := Event{
		ID:           "evt-1",
		OccurredAt:   time.Now().UTC(),
		DurationMS:   12.5,
		Entry:        KindHTTP,
		Operation:    "POST /orders",
		Access:       AccessWrite,
		Action:       "order.create",
		ResourceType: "order",
		ResourceID:   "42",
		Actor:        Actor{ID: "u1", Type: ActorUser, Name: "alice", TenantID: "t1"},
		Client:       Client{IP: "127.0.0.1", UserAgent: "curl", RequestID: "r1"},
		Target:       Target{Method: "POST", Route: "/orders"},
		Outcome:      OutcomeSuccess,
		StatusCode:   201,
		Message:      "ok",
		Request:      Payload{Body: `{"a":1}`, Size: 7},
		TraceID:      "trace-1",
		Service:      "svc",
		Version:      "1",
		Instance:     "inst",
		Metadata:     map[string]string{"region": "cn"},
	}
	require.NoError(t, sink.Write(context.Background(), []Event{event}))

	var line map[string]any
	require.NoError(t, json.Unmarshal(buffer.Bytes(), &line))
	assert.Equal(t, "operation", line["msg"])
	assert.Equal(t, "evt-1", line["audit.id"])
	assert.Equal(t, "POST /orders", line["audit.operation"])
	assert.Equal(t, "order.create", line["audit.action"])
	assert.Equal(t, "u1", line["audit.actor_id"])
	assert.Equal(t, "127.0.0.1", line["audit.client_ip"])
	assert.InDelta(t, float64(201), line["audit.status_code"], 1e-9)
	assert.Equal(t, `{"a":1}`, line["audit.request_body"])
	assert.JSONEq(t, `{"region":"cn"}`, line["audit.metadata"].(string))
}

func TestSlogSinkEmptyBatch(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	slog.SetDefault(logger)
	sink := NewSlogSink()
	require.NoError(t, sink.Write(context.Background(), nil))
	assert.Empty(t, buffer.String())
}

func TestEncodeMetadata(t *testing.T) {
	assert.Equal(t, "", encodeMetadata(nil))
	assert.Equal(t, "", encodeMetadata(map[string]string{}))
	assert.JSONEq(t, `{"a":"1"}`, encodeMetadata(map[string]string{"a": "1"}))
}
