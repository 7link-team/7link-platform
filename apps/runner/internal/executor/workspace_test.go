package executor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceWriteCreatesActualIsolatedCommitForBothRuntimes(t *testing.T) {
	p := runtimeFixture(t)
	p.AuthorizationMode, p.Model = "WORKSPACE_WRITE", "workspace-change"
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		p.Runtime = runtime
		result, err := Run(t.Context(), p, nil)
		if err != nil || !result.RuntimeCompleted || result.Workspace == p.Workdir || result.HeadCommit == p.BaseCommit || !bytes.Contains(result.Diff, []byte("+actual runtime change")) {
			t.Fatalf("%s: %+v err=%v", runtime, result, err)
		}
		rel, err := filepath.Rel(p.Workdir, result.Workspace)
		if err != nil || strings.HasPrefix(rel, "..") || !strings.HasPrefix(rel, ".runner-executions"+string(filepath.Separator)) {
			t.Fatal("workspace escaped registered root", result.Workspace, err)
		}
		if _, err = os.Stat(filepath.Join(p.Workdir, "result.txt")); !os.IsNotExist(err) {
			t.Fatal("runtime mutated registered checkout", err)
		}
		head, err := workspaceGit(t.Context(), p.Workdir, "rev-parse", "HEAD")
		if err != nil || strings.TrimSpace(string(head)) != p.BaseCommit {
			t.Fatal("source HEAD moved", err)
		}
		committed, err := workspaceGit(t.Context(), p.Workdir, "show", result.HeadCommit+":result.txt")
		if err != nil || string(committed) != "actual runtime change\n" {
			t.Fatal("commit evidence does not identify actual bytes", err)
		}
		status, err := workspaceGit(t.Context(), result.Workspace, "status", "--porcelain")
		if err != nil || len(status) != 0 {
			t.Fatal("uncommitted work omitted", string(status), err)
		}
	}
}

func TestWorkspaceWriteFlagsRefuseBroaderAuthorization(t *testing.T) {
	p := Plan{Runtime: "CODEX", Model: "observed", Workdir: t.TempDir(), Objective: "Implement assigned change", AuthorizationMode: "WORKSPACE_WRITE"}
	_, args, err := Command(p)
	if err != nil || !strings.Contains(strings.Join(args, " "), "--sandbox workspace-write") {
		t.Fatal(args, err)
	}
	p.Runtime = "CLAUDE_CODE"
	_, args, err = Command(p)
	if err != nil || !strings.Contains(strings.Join(args, " "), "--permission-mode acceptEdits") || !strings.Contains(strings.Join(args, " "), "--tools Read,Edit,Write,Glob,Grep") {
		t.Fatal(args, err)
	}
	for _, mode := range []string{"FULL_AUTO", "ON_REQUEST", "BYPASS"} {
		p.AuthorizationMode = mode
		if _, _, err = Command(p); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsupported authorization launched", mode, err)
		}
	}
}

func TestWorkspaceRejectsOutsideDirectoryAndOversizedArtifact(t *testing.T) {
	p := runtimeFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(p.Workdir, ".runner-executions")); err == nil {
		if _, err = createWorkspace(t.Context(), p); !errors.Is(err, ErrUnsupported) {
			t.Fatal("symlink escape accepted", err)
		}
	} else {
		t.Log("symlink unavailable; escape fixture not exercised", err)
	}
	var b bytes.Buffer
	w := boundedOutput{buffer: &b, limit: 65536}
	if _, err := w.Write(make([]byte, 65537)); err == nil || b.Len() != 0 {
		t.Fatal("oversized artifact silently truncated", err)
	}
}
