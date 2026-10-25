package control

import (
	"7link.com/runner/internal/executor"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimePermissionBridgeReturnsOnlyServerDecision(t *testing.T) {
	for _, state := range []string{"ALLOW", "DENY", "EXPIRED", "UNKNOWN"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/executions/1/approval-requests" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"id": "2", "state": state, "reason": "Actual Human decision"}})
			}))
			defer server.Close()
			c, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			result, err := runtimePermissionHandler(c, fence{MachineID: "3", SessionID: "4", Epoch: 1}, "1", 1)(context.Background(), executor.PermissionRequest{RequestID: "r", Tool: "Read", Input: json.RawMessage(`{}`)})
			if state == "UNKNOWN" {
				if err == nil {
					t.Fatal("unknown state accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := state
			if state == "EXPIRED" {
				want = "DENY"
			}
			if result.State != want {
				t.Fatalf("got %s", result.State)
			}
		})
	}
}
