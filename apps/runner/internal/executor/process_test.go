package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a deterministic runtime executable. These tests
// execute real subprocesses, never an installed model CLI or paid request.
func TestMain(m *testing.M) {
	if len(os.Args) == 6 && os.Args[1] == "agent-worker" {
		if err := ServeWorker(context.Background(), os.Args[3], os.Args[5], os.Stdin, os.Stdout); err != nil {
			os.Exit(30)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "fixture-verification-report" {
		if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("MACHINE_SECRET") != "" {
			os.Exit(26)
		}
		report := `{"passed":2,"failed":0,"skipped":0}`
		if len(os.Args) > 2 && os.Args[2] == "bad" {
			report += "garbage"
		}
		if os.WriteFile("report.json", []byte(report), 0600) != nil {
			os.Exit(25)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "fixture-verification-timeout" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "fixture-descendant" {
		for {
			_ = os.WriteFile("descendant-heartbeat", []byte(time.Now().String()), 0600)
			time.Sleep(10 * time.Millisecond)
		}
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	if base == "codex" || base == "claude" {
		model := ""
		for i, arg := range os.Args {
			if arg == "--model" && i+1 < len(os.Args) {
				model = os.Args[i+1]
			}
		}
		if os.Getenv("MACHINE_SECRET") != "" || os.Getenv("OPENAI_API_KEY") != "" {
			os.Exit(23)
		}
		if model == "quality-feedback" || model == "planning-output" || model == "planning-observe" {
			input, _ := io.ReadAll(os.Stdin)
			if model == "planning-output" {
				input = []byte(strings.SplitN(string(input), "\n\nPlanning task instructions:", 2)[0])
			}
			if model == "planning-observe" {
				input, _ = json.Marshal(map[string]string{"observed_prompt": string(input)})
			}
			if base == "codex" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": string(input)}})
				fmt.Println(`{"type":"turn.completed"}`)
			} else {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "subtype": "success", "result": string(input)})
			}
			os.Exit(0)
		}
		if model == "workspace-change" {
			if err := os.WriteFile("result.txt", []byte("actual runtime change\n"), 0600); err != nil {
				os.Exit(29)
			}
		}
		if strings.HasPrefix(model, "judge") {
			input, _ := io.ReadAll(os.Stdin)
			parts := strings.SplitN(string(input), "Review input:\n", 2)
			if len(parts) != 2 {
				os.Exit(27)
			}
			var request struct {
				CriterionID string `json:"criterion_id"`
				HeadCommit  string `json:"head_commit"`
			}
			if json.Unmarshal([]byte(parts[1]), &request) != nil {
				os.Exit(28)
			}
			report := VerificationAgentReport{CriterionID: request.CriterionID, HeadCommit: request.HeadCommit, Verdict: "PASSED", Findings: []VerificationAgentFinding{}, Unverified: []string{}}
			if model == "judge-unverified" {
				report.Unverified = []string{"criterion not observed"}
			}
			if model == "judge-major" {
				report.Findings = []VerificationAgentFinding{{Severity: "MAJOR", Summary: "Actual fixture finding"}}
			}
			raw, _ := json.Marshal(report)
			text := string(raw)
			if model == "judge-invalid" {
				text = "not JSON"
			}
			if base == "codex" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": text}})
				if model != "judge-partial" {
					fmt.Println(`{"type":"turn.completed"}`)
				}
			} else {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "result", "subtype": "success", "result": text})
			}
			os.Exit(0)
		}
		if model == "tree" {
			child := exec.Command(os.Args[0], "fixture-descendant")
			hideWindow(child)
			child.Stdout = os.Stdout
			child.Stderr = io.Discard
			if child.Start() != nil {
				os.Exit(24)
			}
			fmt.Println(`{"type":"thread.started"}`)
			for {
				time.Sleep(time.Second)
			}
		}
		if model == "partial" {
			fmt.Println(`{"type":"thread.started"}`)
			os.Exit(0)
		}
		if base == "codex" {
			fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":2,"output_tokens":1}}`)
		} else {
			fmt.Println(`{"type":"result","subtype":"success","is_error":false}`)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runtimeFixture(t *testing.T) Plan {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable for actual repository fixture")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", dir}, {"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		hideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	git := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	hideWindow(git)
	head, err := git.Output()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err = os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MACHINE_SECRET", "must-not-reach-runtime")
	t.Setenv("OPENAI_API_KEY", "must-not-reach-runtime")
	return Plan{Runtime: "CODEX", Model: "fixture", Workdir: dir, BaseCommit: strings.TrimSpace(string(head)), Objective: "Observe repository", AuthorizationMode: "READ_ONLY"}
}

func TestRunActualSubprocessContractForBothRuntimes(t *testing.T) {
	plan := runtimeFixture(t)
	for _, kind := range []string{"CODEX", "CLAUDE_CODE"} {
		t.Run(kind, func(t *testing.T) {
			plan.Runtime = kind
			var observed []Observation
			result, err := Run(t.Context(), plan, func(o Observation) error { observed = append(observed, o); return nil })
			if err != nil || !result.RuntimeCompleted || result.HeadCommit != plan.BaseCommit || result.ResultState != "RUNTIME_CONFIRMED" {
				t.Fatalf("runtime contract: %+v %v", result, err)
			}
			if len(observed) != 2 || observed[0].Kind != "WORKSPACE_READY" || !sameWorkspacePath(observed[0].Content, plan.Workdir) || observed[1].Kind != "RUNTIME_COMPLETED" {
				t.Fatal(observed)
			}
		})
	}
	plan.Runtime = "CODEX"
	plan.Model = "partial"
	result, err := Run(t.Context(), plan, nil)
	if !errors.Is(err, ErrUnknownResult) || result.RuntimeCompleted {
		t.Fatal("partial output/exit zero became completion", result, err)
	}
	plan.BaseCommit = strings.Repeat("f", 40)
	if _, err = Run(t.Context(), plan, nil); err == nil {
		t.Fatal("base commit mismatch launched runtime")
	}
}

func TestRunCancellationStopsActualRuntimeAndDescendant(t *testing.T) {
	plan := runtimeFixture(t)
	plan.Model = "tree"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := Run(ctx, plan, nil)
		if !result.CancellationConfirmed {
			t.Error("actual runtime tree shutdown was not confirmed")
		}
		done <- err
	}()
	path := filepath.Join(plan.Workdir, "descendant-heartbeat")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("descendant never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnknownResult) {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("runtime cleanup exceeded bound")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("descendant survived runtime cancellation")
	}
}
