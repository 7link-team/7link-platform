package control

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

// TestApprovalBackendLiveWorker is launched by the backend composition test.
// Decisions come exclusively from its authenticated Human API, never this worker.
func TestApprovalBackendLiveWorker(t *testing.T) {
	path := os.Getenv("RUNNER_APPROVAL_BACKEND_CONFIG")
	if path == "" {
		t.Skip("requires a live backend harness")
	}
	var cfg struct {
		URL, Token, ExecutionID, MachineID, SessionID, Repository, BaseCommit, Model, Objective string
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal("invalid private harness configuration")
	}
	c, err := NewClient(cfg.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.token = cfg.Token
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	bridge := runtimePermissionHandler(c, fence{MachineID: cfg.MachineID, SessionID: cfg.SessionID, Epoch: 1}, cfg.ExecutionID, 1)
	allowed, denied := 0, 0
	plan := executor.Plan{Runtime: "CLAUDE_CODE", Model: cfg.Model, AuthorizationMode: "ON_REQUEST", ApprovalProtocol: "CLAUDE_CONTROL_V1", Workdir: cfg.Repository, BaseCommit: cfg.BaseCommit,
		Objective: cfg.Objective,
		RequestPermission: func(ctx context.Context, req executor.PermissionRequest) (executor.PermissionDecision, error) {
			decision, err := bridge(ctx, req)
			if err == nil {
				if decision.State == "ALLOW" {
					allowed++
				}
				if decision.State == "DENY" {
					denied++
				}
			}
			return decision, err
		},
	}
	result, err := executor.Run(ctx, plan, func(executor.Observation) error { return nil })
	if err != nil {
		t.Fatalf("live runtime failed: %v (allow=%d deny=%d)", err, allowed, denied)
	}
	if allowed != 1 || denied < 1 || !result.RuntimeCompleted {
		t.Fatalf("missing real API decisions: allow=%d deny=%d completed=%v", allowed, denied, result.RuntimeCompleted)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", result.Workspace, "show", result.HeadCommit+":hello.txt")
	hideServiceWindow(cmd)
	actual, err := cmd.Output()
	if err != nil || string(actual) != "hello\n" {
		t.Fatalf("allowed write missing from final commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.Workspace, "denied.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write reached workspace")
	}
	cmd = exec.CommandContext(ctx, "git", "-C", result.Workspace, "cat-file", "-e", result.HeadCommit+":denied.txt")
	hideServiceWindow(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("denied write reached final commit")
	}
	cmd = exec.CommandContext(ctx, "git", "-C", cfg.Repository, "rev-parse", "HEAD")
	hideServiceWindow(cmd)
	actual, err = cmd.Output()
	if err != nil || strings.TrimSpace(string(actual)) != cfg.BaseCommit {
		t.Fatal("scratch source HEAD changed")
	}
	for _, name := range []string{"hello.txt", "denied.txt"} {
		if _, err := os.Stat(filepath.Join(cfg.Repository, name)); !os.IsNotExist(err) {
			t.Fatal("runtime modified source checkout")
		}
	}
	t.Logf("real_backend=true claude=true allowed=%d denied=%d exact_bytes=true denied_write_absent=true source_unchanged=true", allowed, denied)
}
