package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventJSONRoundTrip(t *testing.T) {
	event := Event{
		ID:           "1001",
		OccurredAt:   time.Now().UTC(),
		DurationMS:   2.5,
		Entry:        KindHTTP,
		Operation:    "POST /orders",
		Access:       AccessWrite,
		Action:       "order.create",
		ResourceType: "order",
		ResourceID:   "42",
		Actor:        Actor{ID: "u1", Type: ActorUser, Name: "alice", TenantID: "t1"},
		Client:       Client{IP: "10.0.0.1", UserAgent: "curl", RequestID: "r1"},
		Target:       Target{Method: "POST", Route: "/orders"},
		Outcome:      OutcomeSuccess,
		StatusCode:   201,
		Code:         0,
		Message:      "ok",
		Request:      Payload{Body: `{"a":1}`, Size: 7},
		Response:     Payload{Size: 12, Omitted: OmittedSuccessSummary},
		TraceID:      "trace-1",
		SpanID:       "span-1",
		Service:      "svc",
		Version:      "1",
		Instance:     "inst",
		Metadata:     map[string]string{"region": "cn"},
	}

	raw, err := json.Marshal(event)
	require.NoError(t, err)
	var decoded Event
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, event.ID, decoded.ID)
	assert.Equal(t, event.Operation, decoded.Operation)
	assert.Equal(t, event.Access, decoded.Access)
	assert.Equal(t, event.Outcome, decoded.Outcome)
	assert.Equal(t, event.Response.Omitted, decoded.Response.Omitted)
	assert.Equal(t, "cn", decoded.Metadata["region"])
	assert.Equal(t, event.OccurredAt.UTC(), decoded.OccurredAt.UTC())
}

func TestEventOmittedZeroValue(t *testing.T) {
	var payload Payload
	assert.Equal(t, Omitted(""), payload.Omitted)
	assert.Empty(t, payload.Body)
	assert.Zero(t, payload.Size)
	assert.False(t, payload.Truncated)
}
