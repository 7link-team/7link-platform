package execution

import (
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCorrelationHTTPExportCurrentAuthoritySafeEnvelopeAndPagination(t *testing.T) {
	f, handler := executionRoutesFixture(t)
	v := f.create(t)
	f.pull(t, v.ID)
	for _, r := range []ReceiptInput{{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"}, {DispatchEpoch: 1, Seq: 2, Kind: "HEARTBEAT"}} {
		_, err := f.receipt(v.ID, r)
		require.NoError(t, err)
	}
	url := "/executions/" + v.ID.String() + "/trace"
	response := executionRouteRequest(t, handler, http.MethodGet, url+"?limit=1", nil, 0, false)
	page := executionRouteData[ExecutionTraceExport](t, response, 200)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Len(t, page.Events, 1)
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, "CUSTOM", page.Events[0].Type)
	require.Equal(t, "execution.receipt", page.Events[0].Name)
	require.Equal(t, "ACCEPTED", page.Events[0].Value.Kind)
	require.Equal(t, v.Plan.Correlation, page.Events[0].Value.Correlation)
	second := executionRouteData[ExecutionTraceExport](t, executionRouteRequest(t, handler, http.MethodGet, url+"?limit=1&cursor="+page.NextCursor, nil, 0, false), 200)
	require.Len(t, second.Events, 1)
	require.False(t, second.HasMore)
	require.Equal(t, "HEARTBEAT", second.Events[0].Value.Kind)
	require.Greater(t, second.Events[0].Value.ID, page.Events[0].Value.ID)
	for _, secret := range []string{"objective", "workdir", "argv", "nonce", "content", "authorization_mode"} {
		require.NotContains(t, response.Body.String(), `"`+secret+`"`)
	}
	f.authorized = false
	require.Equal(t, 403, executionRouteRequest(t, handler, http.MethodGet, url, nil, 0, false).Code)
	f.authorized = true
	f.humanActive = false
	require.Equal(t, 403, executionRouteRequest(t, handler, http.MethodGet, url, nil, 0, false).Code)
	f.humanActive = true
	require.Equal(t, 404, executionRouteRequest(t, handler, http.MethodGet, "/executions/999/trace", nil, 0, false).Code)
	for _, q := range []string{"?cursor=bad", "?limit=101"} {
		require.Equal(t, 422, executionRouteRequest(t, handler, http.MethodGet, url+q, nil, 0, false).Code)
	}
}

func TestCorrelationExportVerifierUsesOwnContextAndRejectsCorruptedSource(t *testing.T) {
	f, request := verificationFixture(t)
	var id guid.ID
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { var e error; id, e = f.m.queueVerificationOn(f.human, tx, request); return e }))
	var out ExecutionTraceExport
	read := func(tenant guid.ID, limit int) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = f.m.traceExportOn(f.human, tx, tenant, request.ExecutionID, 0, limit)
			return e
		})
	}
	require.NoError(t, read(7, 100))
	require.Len(t, out.Events, 1)
	e := out.Events[0]
	require.Equal(t, "execution.verification", e.Name)
	require.Equal(t, id, e.Value.ID)
	require.Equal(t, request.ResultID, *e.Value.ResultID)
	require.Equal(t, "QUEUED", e.Value.State)
	require.Empty(t, e.Value.ReceiptHash)
	require.Equal(t, f.read(t, request.ExecutionID).Plan.Correlation.SpanID, e.Value.Correlation.ParentSpanID)
	data, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(data), "nonce")
	require.NotContains(t, string(data), "source_workdir")
	require.ErrorIs(t, read(8, 100), ErrNotFound)
	require.ErrorIs(t, read(7, 0), ErrInvalidArgument)
	q := query.BindVerificationTasks(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(id)).Update(q.PlanHash, "corrupt")
	require.NoError(t, err)
	require.ErrorIs(t, read(7, 100), ErrUnavailable)
}

func TestTraceExportStorageFailuresNeverExposePartialExecutionFacts(t *testing.T) {
	executionStorageFailures(t, func(t *testing.T) (*executionFixture, func(*gorm.DB) error) {
		f := executionHarness(t)
		v := f.create(t)
		f.pull(t, v.ID)
		_, e := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
		require.NoError(t, e)
		return f, func(tx *gorm.DB) error {
			out, e := f.m.traceExportOn(f.human, tx, 7, v.ID, 0, 50)
			if e == nil {
				require.NotEmpty(t, out.Events)
			}
			return e
		}
	})
}
