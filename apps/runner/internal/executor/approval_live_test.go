package executor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: invokes an installed, authenticated model and can incur usage.
// Its test callback is an explicit item decision, never a Human governance event.
func TestApprovalInstalledRuntimeLive(t *testing.T) {
	runtime := os.Getenv("RUNNER_APPROVAL_LIVE_RUNTIME")
	model := os.Getenv("RUNNER_APPROVAL_LIVE_MODEL")
	if runtime == "" || model == "" {
		t.Skip("requires RUNNER_APPROVAL_LIVE_RUNTIME and RUNNER_APPROVAL_LIVE_MODEL")
	}
	protocol := map[string]string{"CODEX": "CODEX_APP_SERVER_V1", "CLAUDE_CODE": "CLAUDE_CONTROL_V1"}[runtime]
	if protocol == "" {
		t.Fatal("unsupported live runtime")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		hideWindow(cmd)
		data, err := cmd.Output()
		if err != nil {
			t.Fatalf("scratch git %s failed: %v", args[0], err)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "--quiet")
	git("-c", "user.name=Tester", "-c", "user.email=tester@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--allow-empty", "-m", "baseline")
	base := git("rev-parse", "HEAD")
	calls := 0
	allowedCalls := 0
	writeCommand := `python -c "from pathlib import Path; Path('hello.txt').write_bytes(bytes([104,101,108,108,111,10]))"`
	plan := Plan{Runtime: runtime, Model: model, AuthorizationMode: "ON_REQUEST", ApprovalProtocol: protocol, Workdir: dir, BaseCommit: base,
		Objective: "Create hello.txt containing exactly hello followed by one newline. Run exactly this shell command without edits: " + writeCommand + ". Use the shell tool, not an edit or patch tool, and request permission when required. The callback permits only that exact command. Do not run setup commands or inspect files, change other files, inspect unrelated directories, use network, or commit. This is a throwaway scratch repository and permission decisions are supplied by an explicit automated test callback, not a Human.",
		RequestPermission: func(_ context.Context, req PermissionRequest) (PermissionDecision, error) {
			if req.RequestID == "" || req.Tool == "" || !json.Valid(req.Input) {
				t.Error("runtime omitted permission request identity")
				return PermissionDecision{State: "DENY", Reason: "Invalid test request"}, nil
			}
			calls++
			if !approvalLiveWriteAllowed(req, writeCommand, filepath.Join(dir, ".runner-executions")) {
				return PermissionDecision{State: "DENY", Reason: "Only the exact hello.txt scratch write is approved"}, nil
			}
			allowedCalls++
			return PermissionDecision{State: "ALLOW", Reason: "Explicit scratch test item approval"}, nil
		},
	}
	result, err := Run(ctx, plan, func(Observation) error { return nil })
	if err != nil {
		t.Fatalf("installed runtime bridge failed (permission requests=%d): %v", calls, err)
	}
	if calls == 0 || allowedCalls == 0 {
		t.Fatal("runtime completed without exercising and resuming an allowed permission request")
	}
	if !result.RuntimeCompleted || result.HeadCommit == base || result.Workspace == "" {
		t.Fatalf("runtime did not produce an isolated completed commit: %+v", result)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", result.Workspace, "show", result.HeadCommit+":hello.txt")
	hideWindow(cmd)
	data, err := cmd.Output()
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("actual final commit does not contain exact required bytes: %v", err)
	}
	if git("rev-parse", "HEAD") != base {
		t.Fatal("runtime changed source HEAD")
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("runtime changed source checkout")
	}
	t.Logf("runtime=%s permission_requests=%d completed=true exact_bytes=true source_unchanged=true (automated callback only)", runtime, calls)
}

func approvalLiveWriteAllowed(req PermissionRequest, command, workspaceRoot string) bool {
	if req.Tool != "Bash" && req.Tool != "item/commandExecution/requestApproval" {
		return false
	}
	var input struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if json.Unmarshal(req.Input, &input) != nil {
		return false
	}
	if input.Cwd != "" {
		rel, err := filepath.Rel(workspaceRoot, input.Cwd)
		if err != nil || !filepath.IsAbs(input.Cwd) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return strings.TrimSpace(input.Command) == command
}

func TestApprovalLiveCallbackRejectsOtherCommands(t *testing.T) {
	command := `python -c "write exact scratch bytes"`
	root := t.TempDir()
	for _, input := range []string{command + "; other-command", "other-command", ""} {
		data, _ := json.Marshal(map[string]string{"command": input})
		if approvalLiveWriteAllowed(PermissionRequest{Tool: "Bash", Input: data}, command, root) {
			t.Fatal("callback granted a command outside the exact scratch write")
		}
	}
	data, _ := json.Marshal(map[string]string{"command": command})
	if !approvalLiveWriteAllowed(PermissionRequest{Tool: "Bash", Input: data}, command, root) || approvalLiveWriteAllowed(PermissionRequest{Tool: "Write", Input: data}, command, root) {
		t.Fatal("callback did not bind approval to the exact command and tool")
	}
	data, _ = json.Marshal(map[string]string{"command": command, "cwd": filepath.Dir(root)})
	if approvalLiveWriteAllowed(PermissionRequest{Tool: "item/commandExecution/requestApproval", Input: data}, command, root) {
		t.Fatal("callback approved a write outside the scratch workspace")
	}
}
