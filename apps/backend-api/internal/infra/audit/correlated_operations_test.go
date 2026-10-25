package audit

import (
	"encoding/json"
	"time"

	"7link.com/platform/internal/infra/audit/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCorrelationMatchesOriginalTraceAndRecordedJobMetadataOnly(t *testing.T) {
	db := openTestDB(t)
	migrateTestDatabase(t, db)
	sink := NewGormSink(db, "")
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, sink.Write(t.Context(), []Event{
		{ID: "701", OccurredAt: now, TraceID: "request-1", Operation: "request", Actor: Actor{TenantID: "7"}},
		{ID: "702", OccurredAt: now, TraceID: "worker-2", Operation: "job", Metadata: map[string]string{"correlation_id": "request-1"}, Request: Payload{Body: `{"secret":"private"}`}},
		{ID: "703", OccurredAt: now, TraceID: "request-10", Metadata: map[string]string{"correlation_id": "request-10"}},
		{ID: "704", OccurredAt: now, Metadata: map[string]string{"note": `"correlation_id":"request-1"`}},
	}))
	rows, total, err := readCorrelatedOperations(t.Context(), db, "", "request-1", 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 1)
	require.EqualValues(t, 702, rows[0].ID)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
	require.NotContains(t, string(encoded), "metadata")
	rows, _, err = readCorrelatedOperations(t.Context(), db, "", "request-1", 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 701, rows[0].ID)
	for _, invalid := range []string{"", "%", "request_1", `x"`, "x\\y"} {
		_, _, err = readCorrelatedOperations(t.Context(), db, "", invalid, 0, 50)
		require.ErrorIs(t, err, ErrInvalidCorrelation)
	}
	_, _, err = readCorrelatedOperations(t.Context(), nil, "", "request-1", 0, 50)
	require.ErrorIs(t, err, ErrDatabaseSinkUnavailable)
	require.NoError(t, db.Table("pref_operation_logs").AutoMigrate(&model.OperationLog{}))
	rows, total, err = readCorrelatedOperations(t.Context(), db, "pref_", "request-1", 0, 50)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
}

func TestAdministrativeJSONUsesCredentialRedaction(t *testing.T) {
	out, err := RedactAdministrativeJSON([]byte(`{"id":9007199254740993,"client_secret":"private","nested":{"accessToken":"private"}}`))
	require.NoError(t, err)
	require.NotContains(t, out, "private")
	require.Contains(t, out, "9007199254740993")
	out, err = RedactAdministrativeJSON([]byte(`{"secret":`))
	require.Error(t, err)
	require.Empty(t, out)
}
