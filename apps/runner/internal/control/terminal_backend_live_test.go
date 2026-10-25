package control

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"7link.com/runner/internal/executor"
)

// TestTerminalBackendLiveWorker is a native runner process launched only by the
// composed API acceptance harness. No service is installed or global profile used.
func TestTerminalBackendLiveWorker(t *testing.T) {
	private := os.Getenv("RUNNER_TERMINAL_BACKEND_CONFIG")
	if private == "" || runtime.GOOS != "windows" {
		t.Skip("requires Windows native terminal backend harness")
	}
	var cfg struct{ URL, Token, MachineID, SessionID, ExecutionID, Repository, Workspace, StopFile, PreviewPortFile string }
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("cannot read private terminal harness configuration")
	}
	if json.Unmarshal(data, &cfg) != nil {
		t.Fatal("invalid private terminal harness configuration")
	}
	store, err := OpenStore(filepath.Join(t.TempDir(), "profile"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveProfile(Profile{Server: cfg.URL, TenantID: "201", MachineID: cfg.MachineID, ClientID: "live-terminal-service"}); err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(cfg.URL)
	if err != nil {
		t.Fatal(err)
	}
	c.token = cfg.Token
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	ctx = withTerminalWorkspaces(ctx)
	var runtimeDone chan error
	if cfg.PreviewPortFile != "" {
		executable, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		binary, e := os.ReadFile(executable)
		if e != nil {
			t.Fatal(e)
		}
		bin := t.TempDir()
		if e = os.WriteFile(filepath.Join(bin, "codex.exe"), binary, 0700); e != nil {
			t.Fatal(e)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		git := exec.CommandContext(ctx, "git", "-C", cfg.Workspace, "rev-parse", "HEAD")
		hideServiceWindow(git)
		head, e := git.Output()
		if e != nil {
			t.Fatal(e)
		}
		controller := new(executor.ProcessControl)
		runtimeDone = make(chan error, 1)
		go func() {
			_, e := executor.Run(ctx, executor.Plan{ProcessControl: controller, Runtime: "CODEX", Model: "native-fixture", Workdir: cfg.Workspace, BaseCommit: strings.TrimSpace(string(head)), Objective: "native-preview-fixture:" + cfg.PreviewPortFile, AuthorizationMode: "READ_ONLY"}, func(o executor.Observation) error {
				if o.Kind == "WORKSPACE_READY" {
					return pinTerminalWorkspace(ctx, cfg.ExecutionID, 1, cfg.Repository, o.Content, controller)
				}
				return nil
			})
			if e != nil {
				_ = os.WriteFile(cfg.PreviewPortFile+".error", []byte(e.Error()), 0600)
			}
			runtimeDone <- e
		}()
		defer func() { cancel(); <-runtimeDone }()
	} else if err := pinTerminalWorkspace(ctx, cfg.ExecutionID, 1, cfg.Repository, cfg.Workspace); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeTerminalTunnel(ctx, c, fence{MachineID: cfg.MachineID, SessionID: cfg.SessionID, Epoch: 1})
	}()
	previewDone := make(chan struct{})
	go func() {
		defer close(previewDone)
		if cfg.PreviewPortFile != "" {
			consumePreviewTunnel(ctx, c, fence{MachineID: cfg.MachineID, SessionID: cfg.SessionID, Epoch: 1})
		}
	}()
	defer func() { cancel(); <-previewDone }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			<-done
			t.Fatal("native terminal harness deadline exceeded")
		case <-ticker.C:
			if _, err := os.Stat(cfg.StopFile); err == nil {
				cancel()
				<-done
				t.Log("native_runner=true temporary_profile=true service_installed=false tunnel_cleanup_joined=true")
				return
			}
		}
	}
}
