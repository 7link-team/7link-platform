package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"7link.com/runner/internal/executor"
)

func TestAgentWorkerStartupFailureReportsFencedAttemptAndRetainsReplay(t *testing.T) {
	var snapshots [][]agentProcess
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var data any
		if r.Method == "GET" {
			gets++
			if gets == 1 {
				data = []workerProfile{{PrincipalID: "7", Runtime: "CODEX", Model: "model", ConfigRevision: 2, LaunchVersion: 9}}
			} else {
				data = []workerProfile{}
			}
		} else {
			var body struct {
				Agents []agentProcess `json:"agents"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			snapshots = append(snapshots, body.Agents)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	client, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	workers := &agentWorkers{ctx: stopped, available: map[string]bool{"CODEX": true}, workers: map[string]residentWorker{}}
	if e = workers.sync(t.Context(), client, Profile{MachineID: "1"}, fence{SessionID: "2", Epoch: 1}); !errors.Is(e, context.Canceled) {
		t.Fatalf("real cancelled startup must fail: %v", e)
	}
	if len(snapshots) != 2 || len(snapshots[1]) != 1 {
		t.Fatalf("startup failure not reported: %+v", snapshots)
	}
	failed := snapshots[1][0]
	if failed.PrincipalID != "7" || failed.State != "EXITED" || !failed.Attempted || failed.LaunchVersion != 9 || failed.ConfigRevision != nil || failed.PID != 0 {
		t.Fatalf("invented process/revision or lost attempt: %+v", failed)
	}
	if e = workers.sync(t.Context(), client, Profile{MachineID: "1"}, fence{SessionID: "2", Epoch: 1}); e != nil {
		t.Fatal(e)
	}
	if len(snapshots) != 4 || !reflect.DeepEqual(snapshots[1], snapshots[2]) || len(snapshots[3]) != 0 {
		t.Fatalf("failed report must replay until desired policy withdraws it: %+v", snapshots)
	}
}

func TestAgentWorkerMissingNeverFallsBackToDirectRuntime(t *testing.T) {
	w := &agentWorkers{workers: map[string]residentWorker{}}
	result, err := w.run(t.Context(), executor.Plan{ExecutorPrincipalID: "123", Runtime: "CLAUDE_CODE", Model: "sonnet"}, nil)
	if !errors.Is(err, executor.ErrUnsupported) || result.RuntimeCompleted {
		t.Fatal("missing Agent worker fell back", result, err)
	}
}

func TestAgentWorkerReportsObservedExitBeforeFetchingRestartPolicy(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var data any
		if r.Method == "GET" {
			data = []any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	c, e := NewClient(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	w := &agentWorkers{ctx: t.Context(), available: map[string]bool{}, workers: map[string]residentWorker{}}
	if e = w.sync(t.Context(), c, Profile{MachineID: "1"}, fence{SessionID: "2", Epoch: 1}); e != nil {
		t.Fatal(e)
	}
	want := []string{"/machines/1/agent-presences", "/machines/1/agent-workers", "/machines/1/agent-presences"}
	if !reflect.DeepEqual(want, paths) {
		t.Fatalf("observations must precede desired-state fetch: %v", paths)
	}
}

func TestAgentWorkerReplacementRequiresBindingChangeOrHumanRestart(t *testing.T) {
	current := workerProfile{PrincipalID: "21", Runtime: "CODEX", Model: "model", CredentialID: "31", ConfigRevision: 1, LaunchVersion: 4}
	for _, kind := range []string{"configuration", "restart", "same_restart", "runtime", "model", "credential"} {
		t.Run(kind, func(t *testing.T) {
			desired := current
			want := true
			switch kind {
			case "configuration":
				desired.ConfigRevision++
				desired.LaunchVersion++
				want = false
			case "restart":
				desired.RestartRequested = true
				desired.LaunchVersion++
			case "same_restart":
				desired.RestartRequested = true
				want = false
			case "runtime":
				desired.Runtime = "CLAUDE_CODE"
			case "model":
				desired.Model = "other"
			case "credential":
				desired.CredentialID = "32"
			}
			if got := workerNeedsReplacement(current, desired); got != want {
				t.Fatalf("replacement = %v, want %v", got, want)
			}
		})
	}
}
