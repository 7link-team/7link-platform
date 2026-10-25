package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

func TestTerminalWorkspacePinsActualAttemptAndRejectsFallbacks(t *testing.T) {
	ctx := withTerminalWorkspaces(t.Context())
	source, actual, other := t.TempDir(), t.TempDir(), t.TempDir()
	control := new(executor.ProcessControl)
	if err := pinTerminalWorkspace(ctx, "601", 3, source, actual, control); err != nil {
		t.Fatal(err)
	}
	pin, err := resolveTerminalWorkspace(ctx, "601", 3, source)
	if err != nil || !sameTerminalWorkspace(pin.actual, actual) || sameTerminalWorkspace(pin.actual, source) || pin.processControl != control {
		t.Fatalf("wrong runtime workspace: %+v %v", pin, err)
	}
	for _, tc := range []struct {
		id     string
		epoch  int64
		source string
	}{
		{"602", 3, source}, {"601", 2, source}, {"601", 4, source}, {"601", 3, actual}, {"601", 3, other}, {"601", 3, "relative"},
	} {
		if _, err := resolveTerminalWorkspace(ctx, tc.id, tc.epoch, tc.source); !errors.Is(err, errTerminalWorkspace) {
			t.Fatalf("unfenced request accepted: %+v %v", tc, err)
		}
	}
	if err := pinTerminalWorkspace(ctx, "601", 3, source, actual); err != nil {
		t.Fatal("identical registration not idempotent", err)
	}
	if err := pinTerminalWorkspace(ctx, "601", 3, source, other); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("active workspace was rebound", err)
	}
	if err := pinTerminalWorkspace(ctx, "601", 4, source, actual); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("active epoch was rebound", err)
	}
	if err := pinTerminalWorkspace(ctx, "601", 3, source, actual, new(executor.ProcessControl)); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("active process authority was rebound", err)
	}
	dropTerminalWorkspace(ctx, "601", 2)
	if _, err := resolveTerminalWorkspace(ctx, "601", 3, source); err != nil {
		t.Fatal("stale drop removed current epoch", err)
	}
	dropTerminalWorkspace(ctx, "601", 3)
	select {
	case <-pin.lifetime.Done():
	default:
		t.Fatal("attempt end did not cancel terminal lifetime")
	}
	if _, err := resolveTerminalWorkspace(ctx, "601", 3, source); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("ended attempt accepted", err)
	}
	if err := pinTerminalWorkspace(ctx, "601", 4, source, actual); err != nil {
		t.Fatal("next attempt cannot pin", err)
	}
}

func TestTerminalWorkspaceDaemonConsumesReadyWithoutUploadingLocalPath(t *testing.T) {
	store := testStore(t)
	actual := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = withTerminalWorkspaces(ctx)
	completed := make(chan struct{}, 1)
	var mu sync.Mutex
	var logs []receipt
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/machines/11/agent-workspace-pulls", "/runner/merge-pulls", "/runner/verification-pulls", "/machines/11/agent-reset-pulls", "/machines/11/agent-command-pulls", "/machines/11/inspection-pulls", "/machines/11/credential-deliveries":
			respond(w, []inspectionRequest{})
		case "/runner/execution-pulls":
			respond(w, []command{{ID: "601", MachineID: "11", Epoch: 3, State: "CREATED", Plan: executor.Plan{Runtime: "CODEX", Model: "fixture", Workdir: store.Dir, Objective: "Observe", AuthorizationMode: "READ_ONLY"}}})
		case "/executions/601/receipts":
			var in struct {
				Receipt receipt `json:"receipt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Receipt.Kind == "LOG" {
				mu.Lock()
				logs = append(logs, in.Receipt)
				mu.Unlock()
			}
			state := "RUNNING"
			if in.Receipt.Kind == "LOST" {
				state = "LOST"
				select {
				case completed <- struct{}{}:
				default:
				}
			}
			respond(w, receiptAck{Seq: in.Receipt.Seq, State: state})
		default:
			respond(w, true)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	client.token, client.expires = "test-token", time.Now().Add(time.Hour)
	done := make(chan error, 1)
	go func() {
		j := journal{}
		done <- daemonLoop(ctx, store, client, testProfile(server.URL), fence{}, &j, func(runCtx context.Context, plan executor.Plan, emit func(executor.Observation) error) (executor.Result, error) {
			if err := emit(executor.Observation{Kind: "WORKSPACE_READY", Content: actual}); err != nil {
				return executor.Result{}, err
			}
			pin, err := resolveTerminalWorkspace(runCtx, "601", 3, plan.Workdir)
			if err != nil || !sameTerminalWorkspace(pin.actual, actual) || pin.processControl == nil || pin.processControl != plan.ProcessControl {
				t.Error("daemon did not register executor's actual checkout", err)
			}
			if err := emit(executor.Observation{Kind: "LOG", Content: "ordinary runtime observation"}); err != nil {
				return executor.Result{}, err
			}
			return executor.Result{}, executor.ErrUnknownResult
		})
	}()
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Error("daemon did not complete attempt")
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || logs[0].Content != "Runtime observation received" {
		t.Fatal("internal workspace readiness leaked as receipt", logs)
	}
	if _, err := resolveTerminalWorkspace(ctx, "601", 3, store.Dir); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("attempt pin survived daemon exit", err)
	}
}

func TestTerminalWorkspaceRegistryIsBoundedConcurrentAndEphemeral(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	ctx := withTerminalWorkspaces(parent)
	source, actual := t.TempDir(), t.TempDir()
	var workers sync.WaitGroup
	for i := 0; i < terminalWorkspaceLimit; i++ {
		workers.Go(func() {
			id := fmt.Sprint(100 + i)
			if err := pinTerminalWorkspace(ctx, id, 1, source, actual); err != nil {
				t.Error(err)
			}
			for j := 0; j < 25; j++ {
				if _, err := resolveTerminalWorkspace(ctx, id, 1, source); err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	if err := pinTerminalWorkspace(ctx, "999", 1, source, actual); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("registry exceeded capacity", err)
	}
	pin, err := resolveTerminalWorkspace(ctx, "100", 1, source)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-pin.lifetime.Done():
	default:
		t.Fatal("daemon disconnect did not cancel terminal lifetime")
	}
	if _, err := resolveTerminalWorkspace(ctx, "100", 1, source); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("disconnected registry resolved", err)
	}
	newCtx := withTerminalWorkspaces(t.Context())
	if _, err := resolveTerminalWorkspace(newCtx, "100", 1, source); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("pin survived daemon reconnection", err)
	}
	clearTerminalWorkspaces(ctx)
	registry := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.pins) != 0 {
		t.Fatal("disconnect retained pins")
	}
}

func TestTerminalWorkspaceRefusesInvalidLocalFactsAndUnpinnedNativeOpen(t *testing.T) {
	ctx := withTerminalWorkspaces(t.Context())
	source := t.TempDir()
	for _, tc := range []struct {
		id     string
		epoch  int64
		actual string
	}{
		{"bad", 1, source}, {"601", 0, source}, {"601", 1, "relative"}, {"601", 1, filepath.Join(source, "missing")},
	} {
		if err := pinTerminalWorkspace(ctx, tc.id, tc.epoch, source, tc.actual); !errors.Is(err, errTerminalWorkspace) {
			t.Fatal("invalid pin accepted", tc, err)
		}
	}
	if err := pinTerminalWorkspace(t.Context(), "601", 1, source, source); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("missing registry accepted", err)
	}
	if _, err := openTunnelShell(ctx, source, 80, 24); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("native opener fell back to source root", err)
	}
	attemptCtx := context.WithValue(ctx, terminalAttemptKey{}, terminalAttempt{id: "601", epoch: 1})
	if _, err := openTunnelShell(attemptCtx, source, 80, 24); !errors.Is(err, errTerminalWorkspace) {
		t.Fatal("native opener accepted unknown attempt", err)
	}
	if err := pinTerminalWorkspace(ctx, "601", 1, source, source); err != nil {
		t.Fatal("read-only workspace cannot pin validated root", err)
	}
	pin, err := resolveTerminalWorkspace(ctx, "601", 1, source)
	if err != nil || !sameTerminalWorkspace(pin.actual, source) || pin.processControl != nil {
		t.Fatal("read-only root changed", pin, err)
	}
}
