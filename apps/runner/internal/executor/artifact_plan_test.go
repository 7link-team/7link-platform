package executor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPlannedArtifactsCaptureCommittedBytesAndRejectUnsafeOrMissingFiles(t *testing.T) {
	p := runtimeFixture(t)
	p.AuthorizationMode, p.Model = "WORKSPACE_WRITE", "workspace-change"
	p.Artifacts = []PlannedArtifact{{CriterionID: "123", Path: "result.txt", Note: "Actual output"}}
	r, err := Run(t.Context(), p, nil)
	if err != nil || len(r.Artifacts) != 1 || !bytes.Equal(r.Artifacts[0].Data, []byte("actual runtime change\n")) || r.Artifacts[0].CriterionID != "123" {
		t.Fatalf("actual artifact missing: %+v %v", r, err)
	}
	// Later mutable worktree edits cannot change evidence from the final commit.
	if err = os.WriteFile(filepath.Join(r.Workspace, "result.txt"), []byte("changed after commit"), 0600); err != nil {
		t.Fatal(err)
	}
	p.Workdir = r.Workspace
	a, err := capturePlannedArtifacts(t.Context(), p, r.HeadCommit)
	if err != nil || !bytes.Equal(a[0].Data, r.Artifacts[0].Data) {
		t.Fatal("mutable bytes were captured", err)
	}
	for _, file := range []string{"../outside", "/outside", "C:/outside", "dir\\file", ".git/config", "missing.txt", "."} {
		p.Artifacts[0].Path = file
		if _, err = capturePlannedArtifacts(t.Context(), p, r.HeadCommit); err == nil {
			t.Fatal("unsafe/missing artifact accepted", file)
		}
	}
	// Create a real Git symlink entry without relying on OS symlink privileges.
	blob, err := workspaceGit(t.Context(), r.Workspace, "hash-object", "-w", "--", "result.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = workspaceGit(t.Context(), r.Workspace, "update-index", "--add", "--cacheinfo", "120000", string(bytes.TrimSpace(blob)), "link.txt"); err != nil {
		t.Fatal(err)
	}
	tree, err := workspaceGit(t.Context(), r.Workspace, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	p.Artifacts[0].Path = "link.txt"
	if _, err = capturePlannedArtifacts(t.Context(), p, string(bytes.TrimSpace(tree))); err == nil {
		t.Fatal("committed symlink accepted")
	}
}

func TestMissingDeclaredArtifactPreventsSuccessfulCompletion(t *testing.T) {
	p := runtimeFixture(t)
	p.AuthorizationMode, p.Model = "WORKSPACE_WRITE", "workspace-change"
	p.Artifacts = []PlannedArtifact{{CriterionID: "123", Path: "not-produced.txt", Note: "Required output"}}
	r, err := Run(t.Context(), p, nil)
	if err == nil || r.ResultState != "UNKNOWN" || len(r.Artifacts) != 0 {
		t.Fatalf("missing evidence reported successful: %+v %v", r, err)
	}
}
