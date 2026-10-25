package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestDaemonPlanningTransportsOnlyActualCompletedDocumentWithoutClaim(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "completed"}[completed], func(t *testing.T) {
			store := testStore(t)
			terminal := make(chan receipt, 1)
			document := `{"files":["feature.go"]}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/runner/execution-pulls":
					respond(w, []command{{ID: "21", MachineID: "11", Epoch: 1, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Workdir: store.Dir, Objective: "Plan only", AuthorizationMode: "READ_ONLY", Planning: &executor.PlanningPlan{Binding: json.RawMessage(`{}`), Criteria: json.RawMessage(`[]`), DefaultBranch: "main"}}}})
				case "/executions/21/receipts":
					var body struct {
						Receipt receipt `json:"receipt"`
					}
					if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
						t.Error(e)
					}
					state := "RUNNING"
					if body.Receipt.Kind == "EVIDENCE" {
						t.Error("planning uploaded delivery evidence")
					}
					if body.Receipt.Kind == "COMPLETED" || body.Receipt.Kind == "LOST" || body.Receipt.Kind == "FAILED" {
						terminal <- body.Receipt
						state = "SUCCEEDED"
					}
					respond(w, receiptAck{Seq: body.Receipt.Seq, State: state})
				default:
					respond(w, []inspectionRequest{})
				}
			}))
			defer server.Close()
			client, _ := NewClient(server.URL)
			client.token, client.expires = "fixture-token", time.Now().Add(time.Hour)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				j := journal{}
				done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(_ context.Context, p executor.Plan, _ func(executor.Observation) error) (executor.Result, error) {
					if p.Planning == nil {
						t.Error("server planning purpose lost")
					}
					return executor.Result{RuntimeCompleted: completed, PlanningDocument: document, HeadCommit: strings.Repeat("a", 40)}, nil
				})
			}()
			select {
			case got := <-terminal:
				if got.Upload != nil || got.EvidenceKind != "" {
					t.Errorf("planning fabricated evidence %+v", got)
				}
				if completed {
					if got.Kind != "COMPLETED" || got.Content != document || got.HeadCommit != strings.Repeat("a", 40) {
						t.Errorf("actual planning document lost %+v", got)
					}
				} else if got.Kind == "COMPLETED" {
					t.Error("unknown runtime asserted planning success")
				}
			case <-time.After(8 * time.Second):
				t.Error("no planning terminal receipt")
			}
			cancel()
			<-done
		})
	}
}
