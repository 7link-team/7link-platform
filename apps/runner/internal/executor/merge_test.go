package executor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mergeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	hideWindow(c)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func mergeWrite(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func mergeFixture(t *testing.T) MergePlan {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	mergeGit(t, root, "init", "-b", "main")
	mergeGit(t, root, "config", "user.name", "Test")
	mergeGit(t, root, "config", "user.email", "test@example.invalid")
	mergeGit(t, root, "config", "core.autocrlf", "false")
	mergeWrite(t, root, "shared.txt", "base\n")
	mergeGit(t, root, "add", "shared.txt")
	mergeGit(t, root, "commit", "-m", "base")
	mergeWrite(t, filepath.Join(root, ".git", "info"), "exclude", ".runner-executions/\n")
	source := filepath.Join(root, ".runner-executions", "execution-2")
	mergeGit(t, root, "worktree", "add", "--detach", source, "HEAD")
	mergeWrite(t, source, "source.txt", "actual source bytes\n")
	mergeGit(t, source, "add", "source.txt")
	mergeGit(t, source, "commit", "-m", "source")
	return MergePlan{ID: "1", ExecutionID: "2", Nonce: strings.Repeat("a", 64), Workspace: root, SourceWorkdir: source, SourceCommit: mergeGit(t, source, "rev-parse", "HEAD"), TargetBranch: "main", DeadlineAt: time.Now().Add(time.Minute)}
}

func TestMergeActualGitPinnedCommitReplayAndNoNonceDisclosure(t *testing.T) {
	p := mergeFixture(t)
	// Production plans retain the registered root; discover the one worktree at
	// the pinned source commit without changing the transport hash.
	p.SourceWorkdir = p.Workspace
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	before := mergeGit(t, p.Workspace, "rev-parse", "HEAD")
	o, err := RunMerge(t.Context(), p)
	if err != nil || o.State != "MERGED" || o.Pushed || o.Reason != "MERGE_COMMITTED" || o.PlanHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("%+v %v", o, err)
	}
	if got := mergeGit(t, p.Workspace, "log", "-1", "--format=%P"); got != before+" "+p.SourceCommit {
		t.Fatalf("parents %q", got)
	}
	if b, e := os.ReadFile(filepath.Join(p.Workspace, "source.txt")); e != nil || string(b) != "actual source bytes\n" {
		t.Fatal("source bytes not integrated", e)
	}
	message := mergeGit(t, p.Workspace, "log", "-1", "--format=%B")
	if strings.Contains(message, p.Nonce) || !strings.Contains(message, o.PlanHash) {
		t.Fatal("commit disclosed receipt credential or lost command identity", message)
	}
	replay, err := RunMerge(t.Context(), p)
	if err != nil || replay.State != "MERGED" || replay.MergeCommit != o.MergeCommit || replay.PlanHash != o.PlanHash {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if got := mergeGit(t, p.Workspace, "rev-list", "--count", "HEAD"); got != "3" {
		t.Fatal("replay created an extra commit", got)
	}
	p.ID = "3"
	different, err := RunMerge(t.Context(), p)
	if err != nil || different.State != "FAILED" || different.Reason != "ALREADY_INTEGRATED" {
		t.Fatalf("unrelated command adopted merge: %+v %v", different, err)
	}
}

func TestMergeConflictAbortsOnlyNewMergeAndPreservesTarget(t *testing.T) {
	p := mergeFixture(t)
	mergeWrite(t, p.SourceWorkdir, "shared.txt", "source change\n")
	mergeGit(t, p.SourceWorkdir, "add", "shared.txt")
	mergeGit(t, p.SourceWorkdir, "commit", "-m", "source conflict")
	p.SourceCommit = mergeGit(t, p.SourceWorkdir, "rev-parse", "HEAD")
	mergeWrite(t, p.Workspace, "shared.txt", "target change\n")
	mergeGit(t, p.Workspace, "add", "shared.txt")
	mergeGit(t, p.Workspace, "commit", "-m", "target conflict")
	before := mergeGit(t, p.Workspace, "rev-parse", "HEAD")
	o, err := RunMerge(t.Context(), p)
	if err != nil || o.State != "CONFLICT" || o.Reason != "MERGE_CONFLICT" || len(o.ConflictFiles) != 1 || o.ConflictFiles[0] != "shared.txt" || o.MergeCommit != "" || o.Pushed {
		t.Fatalf("%+v %v", o, err)
	}
	if mergeGit(t, p.Workspace, "rev-parse", "HEAD") != before || mergeGit(t, p.Workspace, "status", "--porcelain") != "" {
		t.Fatal("conflict changed target or left merge state")
	}
	if b, e := os.ReadFile(filepath.Join(p.Workspace, "shared.txt")); e != nil || string(b) != "target change\n" {
		t.Fatal("target bytes lost", e)
	}
}

func TestMergeRejectsDirtyChangedAndExpiredSourcesWithoutTargetMutation(t *testing.T) {
	for _, mode := range []string{"target_dirty", "source_dirty", "source_head", "expired", "wrong_branch", "wrong_source"} {
		t.Run(mode, func(t *testing.T) {
			p := mergeFixture(t)
			before := mergeGit(t, p.Workspace, "rev-parse", "HEAD")
			want := ""
			switch mode {
			case "target_dirty":
				mergeWrite(t, p.Workspace, "dirty.txt", "preserve")
				want = "TARGET_DIRTY"
			case "source_dirty":
				mergeWrite(t, p.SourceWorkdir, "dirty.txt", "preserve")
				want = "SOURCE_DIRTY"
			case "source_head":
				mergeGit(t, p.SourceWorkdir, "commit", "--allow-empty", "-m", "changed")
				want = "SOURCE_COMMIT_CHANGED"
			case "expired":
				p.DeadlineAt = time.Now().Add(-time.Second)
				want = "DEADLINE_EXCEEDED"
			case "wrong_branch":
				p.TargetBranch = "other"
				want = "TARGET_BRANCH_CHANGED"
			case "wrong_source":
				p.SourceWorkdir = t.TempDir()
				want = "SOURCE_NOT_WORKTREE"
			}
			o, err := RunMerge(t.Context(), p)
			if err != nil || o.State != "FAILED" || o.Reason != want || o.MergeCommit != "" || o.Pushed {
				t.Fatalf("%+v %v, want %s", o, err, want)
			}
			if mergeGit(t, p.Workspace, "rev-parse", "HEAD") != before {
				t.Fatal("rejected command changed target")
			}
			if mode == "target_dirty" || mode == "source_dirty" {
				dir := p.Workspace
				if mode == "source_dirty" {
					dir = p.SourceWorkdir
				}
				if b, e := os.ReadFile(filepath.Join(dir, "dirty.txt")); e != nil || string(b) != "preserve" {
					t.Fatal("dirty bytes lost", e)
				}
			}
		})
	}
}

func TestMergePushRecordsActualRemoteAndLocalCommitSeparately(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed_push", true: "actual_push"}[success], func(t *testing.T) {
			p := mergeFixture(t)
			p.Push = true
			remote := filepath.Join(t.TempDir(), "remote.git")
			if success {
				mergeGit(t, p.Workspace, "init", "--bare", remote)
			}
			mergeGit(t, p.Workspace, "remote", "add", "origin", remote)
			o, err := RunMerge(t.Context(), p)
			if err != nil || o.State != "MERGED" || !validMergeCommit(o.MergeCommit) || o.Pushed != success {
				t.Fatalf("%+v %v", o, err)
			}
			if !success && o.Reason != "PUSH_FAILED" {
				t.Fatal("push failure hidden", o)
			}
			if success && mergeGit(t, remote, "rev-parse", "refs/heads/main") != o.MergeCommit {
				t.Fatal("remote does not contain observed merge")
			}
			if mergeGit(t, p.Workspace, "rev-parse", "HEAD") != o.MergeCommit {
				t.Fatal("local merge lost")
			}
		})
	}
}

func TestMergeObservationSignsActualTargetContainment(t *testing.T) {
	p := mergeFixture(t)
	o, err := RunMerge(t.Context(), p)
	if err != nil || o.State != "MERGED" || !o.Contained || len(o.Signature) != 64 {
		t.Fatalf("missing signed actual containment: %+v %v", o, err)
	}
	if got := mergeGit(t, p.Workspace, "merge-base", "--is-ancestor", p.SourceCommit, "refs/heads/"+p.TargetBranch); got != "" {
		t.Fatal(got)
	}
	signature, err := hex.DecodeString(o.Signature)
	if err != nil {
		t.Fatal(err)
	}
	o.Signature = ""
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(p.Nonce))
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("machine command signature is not bound to exact observation")
	}
	o.SourceCommit = strings.Repeat("f", 40)
	raw, _ = json.Marshal(o)
	mac.Reset()
	_, _ = mac.Write(raw)
	if hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("signature accepted substituted artifact")
	}
}
