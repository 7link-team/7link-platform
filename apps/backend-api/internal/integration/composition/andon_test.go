package composition_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func TestAndonComposedRuntimeRequestReceiptAndCurrentReplayAuthority(t *testing.T) {
	f := newApprovalFixture(t)
	db := f.app.Runtime().Database().Write()
	var work struct {
		ProjectID guid.ID
		Version   int
	}
	require.NoError(t, db.Table("delivery_work_items").Where("id=501").Take(&work).Error)
	path := fmt.Sprintf("/entities/%s/work-items/501/andon", work.ProjectID)
	router := moduleRouter(t, f.app)
	command := func(path, key string, version int) *httptest.ResponseRecorder {
		raw, err := json.Marshal(map[string]any{"version": version, "reason": "Human investigates the actual source inconsistency"})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+f.humanToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	readRuntime := func(state, reason string) {
		t.Helper()
		var row struct{ State, StateReason string }
		require.NoError(t, db.Table("execution_executions").Where("id=601").Take(&row).Error)
		require.Equal(t, state, row.State)
		require.Equal(t, reason, row.StateReason)
	}
	pull := command(path, "composed-andon-pull", work.Version)
	require.Equal(t, 200, pull.Code, pull.Body.String())
	require.Contains(t, pull.Body.String(), `"runner_pause":"REQUESTED"`)
	require.Contains(t, pull.Body.String(), `"control":"PAUSE_REQUESTED"`)
	readRuntime("RUNNING", "PAUSE_REQUESTED")
	replay := command(path, "composed-andon-pull", work.Version)
	require.Equal(t, 200, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))

	// The real authenticated runner must observe the request before acknowledging
	// it. An authenticated HTTP receipt is still only a runner-reported fact;
	// native process suspension is covered by the Runner's own control tests.
	receipt := func(seq int, kind string) map[string]any {
		var ack map[string]any
		status := f.api("POST", "/executions/601/receipts", f.runnerToken, map[string]any{
			"fence":   map[string]any{"machine_id": "401", "session_id": "402", "conn_epoch": 1},
			"receipt": execution.ReceiptInput{DispatchEpoch: 1, Seq: int64(seq), Kind: kind},
		}, &ack)
		require.Equal(t, 200, status)
		return ack
	}
	heartbeat := receipt(2, "HEARTBEAT")
	require.Equal(t, true, heartbeat["pause_requested"])
	receipt(3, "PAUSED")
	readRuntime("PAUSED", "HOLD_ACKNOWLEDGED")
	// Replaying an old command preserves its original request-time facts.
	replay = command(path, "composed-andon-pull", work.Version)
	require.Contains(t, replay.Body.String(), `"runner_pause":"REQUESTED"`)
	readRuntime("PAUSED", "HOLD_ACKNOWLEDGED")
	require.NoError(t, db.Table("delivery_work_items").Where("id=501").Take(&work).Error)
	release := command(path+"/clear", "composed-andon-clear", work.Version)
	require.Equal(t, 200, release.Code, release.Body.String())
	require.Contains(t, release.Body.String(), `"control":"RESUME_REQUESTED"`)
	readRuntime("PAUSED", "RESUME_REQUESTED")
	heartbeat = receipt(4, "HEARTBEAT")
	require.Equal(t, true, heartbeat["resume_requested"])
	receipt(5, "RESUMED")
	readRuntime("RUNNING", "HOLD_RELEASE_ACKNOWLEDGED")

	var count int64
	require.NoError(t, db.Table("domain_events").Where("tenant_id=201 AND event_type IN ?", []string{"andon.pulled", "andon.cleared"}).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Table("iam_audit_events").Where("tenant_id=201 AND event_type='delivery.andon_disposition'").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Table("iam_tenant_members").Where("tenant_id=201 AND principal_id=301").Update("status", "disabled").Error)
	denied := command(path, "composed-andon-pull", work.Version-1)
	require.Contains(t, []int{403, 404}, denied.Code, denied.Body.String())
	readRuntime("RUNNING", "HOLD_RELEASE_ACKNOWLEDGED")
}
