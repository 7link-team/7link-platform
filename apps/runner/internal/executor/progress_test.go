package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProgressMeasuresContentMissingDependenciesAndPinnedCommit(t *testing.T) {
	p := runtimeFixture(t)
	p.AuthorizationMode = "WORKSPACE_WRITE"
	p.ProgressRootCommit = p.BaseCommit
	workspace, err := createWorkspace(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.Workdir = workspace
	p.ProgressPolicy = &ProgressPolicy{NoProgressLimit: 2, ObservablePaths: []string{"progress.txt"}, DependencyMissLimit: 2, RequiredDependencyPaths: []string{"dependency.txt"}}
	first, e := captureProgress(t.Context(), p, "")
	if e != nil {
		t.Fatal(e)
	}
	if first.Observable["progress.txt"] != "" || first.Dependencies["dependency.txt"] {
		t.Fatal("missing paths became progress")
	}
	if e = os.WriteFile(filepath.Join(p.Workdir, "progress.txt"), []byte("actual output"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p.Workdir, "dependency.txt"), []byte("actual dependency"), 0600); e != nil {
		t.Fatal(e)
	}
	next, e := captureProgress(t.Context(), p, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(next.Observable["progress.txt"]) != 64 || !next.Dependencies["dependency.txt"] {
		t.Fatal("actual files were not measured")
	}
	result, e := finishWorkspace(t.Context(), p, Result{RuntimeCompleted: true})
	if e != nil {
		t.Fatal(e)
	}
	if result.Progress == nil || !result.Progress.Final || result.Progress.HeadCommit != result.HeadCommit {
		t.Fatal("final manifest not pinned")
	}
	frozen := result.Progress.ChangedFiles["progress.txt"]
	if e = os.WriteFile(filepath.Join(p.Workdir, "progress.txt"), []byte("later mutable changes"), 0600); e != nil {
		t.Fatal(e)
	}
	final, e := captureProgress(t.Context(), p, result.HeadCommit)
	if e != nil {
		t.Fatal(e)
	}
	if final.ChangedFiles["progress.txt"] != frozen {
		t.Fatal("final manifest read later worktree")
	}
	if _, e = progressPath(p.Workdir, "../outside"); e == nil {
		t.Fatal("escaping dependency accepted")
	}
}

func TestProgressOnlyCountsCompletedRuntimeActions(t *testing.T) {
	cases := []struct {
		runtime, body string
		want          bool
	}{
		{"CODEX", `{"type":"item.started"}`, false},
		{"CODEX", `{"type":"item.completed"}`, false},
		{"CODEX", `{"type":"item.completed","item":{"type":"reasoning"}}`, false},
		{"CODEX", `{"type":"item.completed","item":{"type":"agent_message"}}`, false},
		{"CODEX", `{"type":"item.completed","item":{"type":"command_execution"}}`, true},
		{"CODEX", `{"type":"item.completed","item":{"type":"file_change"}}`, true},
		{"CODEX", `{"type":"item.completed","item":{"type":"mcp_tool_call"}}`, true},
		{"CODEX", `{"type":"item.completed","item":{"type":"web_search"}}`, true},
		{"CLAUDE_CODE", `{"type":"user","message":{"content":[{"type":"tool_result"}]}}`, true},
		{"CLAUDE_CODE", `{"type":"assistant"}`, false},
	}
	for _, c := range cases {
		if got := actionCompleted(c.runtime, Observation{Kind: "RUNTIME_EVENT", Content: c.body}); got != c.want {
			t.Fatalf("%s %s: %v", c.runtime, c.body, got)
		}
	}
}
