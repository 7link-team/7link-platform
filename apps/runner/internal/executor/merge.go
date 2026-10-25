package executor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type MergePlan struct {
	ID            string    `json:"id"`
	Nonce         string    `json:"nonce"`
	ExecutionID   string    `json:"execution_id"`
	SourceWorkdir string    `json:"source_workdir"`
	Workspace     string    `json:"workspace"`
	SourceCommit  string    `json:"source_commit"`
	TargetBranch  string    `json:"target_branch"`
	Push          bool      `json:"push"`
	DeadlineAt    time.Time `json:"deadline_at"`
}
type MergeObservation struct {
	Contained     bool      `json:"contained"`
	Signature     string    `json:"signature,omitempty" maxLength:"64"`
	PlanHash      string    `json:"plan_hash"`
	State         string    `json:"state"`
	SourceCommit  string    `json:"source_commit"`
	MergeCommit   string    `json:"merge_commit,omitempty"`
	Pushed        bool      `json:"pushed"`
	ConflictFiles []string  `json:"conflict_files,omitempty"`
	Reason        string    `json:"reason"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
}

func validMergeCommit(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil && strings.ToLower(s) == s
}

// RunMerge only integrates the pinned commit from an actual Git worktree into
// the registered target checkout. It never runs hooks or trusts an Agent's
// textual claim that a merge succeeded. Result state comes from Git objects.
func RunMerge(ctx context.Context, p MergePlan) (MergeObservation, error) {
	var o MergeObservation
	if !filepath.IsAbs(p.Workspace) || !filepath.IsAbs(p.SourceWorkdir) || !validMergeCommit(p.SourceCommit) || len(p.Nonce) != 64 || p.ID == "" || p.ExecutionID == "" || p.TargetBranch == "" || strings.HasPrefix(p.TargetBranch, "-") {
		return o, ErrUnsupported
	}
	raw, _ := json.Marshal(p)
	digest := sha256.Sum256(raw)
	o = MergeObservation{PlanHash: hex.EncodeToString(digest[:]), State: "FAILED", SourceCommit: p.SourceCommit, StartedAt: time.Now().UTC()}
	finish := func(reason string) (MergeObservation, error) {
		o.Reason = reason
		o.EndedAt = time.Now().UTC()
		unsigned, _ := json.Marshal(o)
		mac := hmac.New(sha256.New, []byte(p.Nonce))
		_, _ = mac.Write(unsigned)
		o.Signature = hex.EncodeToString(mac.Sum(nil))
		return o, nil
	}
	if !p.DeadlineAt.After(time.Now()) {
		return finish("DEADLINE_EXCEEDED")
	}
	runCtx, cancel := context.WithDeadline(ctx, p.DeadlineAt)
	defer cancel()
	git := func(dir string, args ...string) ([]byte, error) {
		c := exec.CommandContext(runCtx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-C", dir}, args...)...)
		hideWindow(c)
		configureCancellation(c)
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
		c.Stderr = io.Discard
		return c.Output()
	}
	text := func(dir string, args ...string) (string, error) {
		b, e := git(dir, args...)
		return strings.TrimSpace(string(b)), e
	}
	if _, e := git(p.Workspace, "check-ref-format", "--branch", p.TargetBranch); e != nil {
		return finish("INVALID_TARGET")
	}
	common, e := text(p.Workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return finish("WORKSPACE_UNAVAILABLE")
	}
	if sameWorkspacePath(p.SourceWorkdir, p.Workspace) {
		// Backend freezes the registered root; the Runner owns its random retained
		// worktree names. Locate exactly one retained worktree at the pinned head.
		list, e := text(p.Workspace, "worktree", "list", "--porcelain")
		if e != nil {
			return finish("SOURCE_NOT_WORKTREE")
		}
		match := ""
		for _, block := range strings.Split(list, "\n\n") {
			path, head := "", ""
			for _, line := range strings.Split(block, "\n") {
				if strings.HasPrefix(line, "worktree ") {
					path = strings.TrimPrefix(line, "worktree ")
				}
				if strings.HasPrefix(line, "HEAD ") {
					head = strings.TrimPrefix(line, "HEAD ")
				}
			}
			rel, e := filepath.Rel(filepath.Join(p.Workspace, ".runner-executions"), path)
			if e == nil && head == p.SourceCommit && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..") && strings.HasPrefix(rel, "execution-") && !strings.ContainsAny(rel, "/\\") {
				if match != "" {
					return finish("SOURCE_WORKTREE_AMBIGUOUS")
				}
				match = path
			}
		}
		if match == "" {
			return finish("SOURCE_NOT_WORKTREE")
		}
		p.SourceWorkdir = match
	}
	sourceCommon, e := text(p.SourceWorkdir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil || common != sourceCommon {
		return finish("SOURCE_NOT_WORKTREE")
	}
	gitdir, e := text(p.SourceWorkdir, "rev-parse", "--path-format=absolute", "--git-dir")
	if e != nil || gitdir == sourceCommon {
		return finish("SOURCE_NOT_WORKTREE")
	}
	source, e := text(p.SourceWorkdir, "rev-parse", "HEAD")
	if e != nil || source != p.SourceCommit {
		return finish("SOURCE_COMMIT_CHANGED")
	}
	clean, e := text(p.Workspace, "status", "--porcelain=v1", "--untracked-files=all")
	if e != nil || clean != "" {
		return finish("TARGET_DIRTY")
	}
	clean, e = text(p.SourceWorkdir, "status", "--porcelain=v1", "--untracked-files=all")
	if e != nil || clean != "" {
		return finish("SOURCE_DIRTY")
	}
	branch, e := text(p.Workspace, "symbolic-ref", "--quiet", "--short", "HEAD")
	if e != nil || branch != p.TargetBranch {
		return finish("TARGET_BRANCH_CHANGED")
	}
	if _, e = git(p.Workspace, "rev-parse", "--verify", "MERGE_HEAD"); e == nil {
		return finish("TARGET_MERGE_IN_PROGRESS")
	}
	marker := "Integrate execution " + p.ExecutionID + "; merge " + p.ID + "; plan " + o.PlanHash
	before, e := text(p.Workspace, "rev-parse", "HEAD")
	if e != nil {
		return finish("TARGET_UNAVAILABLE")
	}
	// A restarted command may observe its own exact two-parent merge. Reuse the
	// object, never create another integration or mistake an unrelated commit.
	message, _ := text(p.Workspace, "log", "-1", "--format=%B")
	parents, _ := text(p.Workspace, "log", "-1", "--format=%P")
	parentList := strings.Fields(parents)
	if message == marker && len(parentList) == 2 && parentList[1] == p.SourceCommit {
		o.MergeCommit = before
	} else {
		if _, e = git(p.Workspace, "merge-base", "--is-ancestor", p.SourceCommit, "HEAD"); e == nil {
			return finish("ALREADY_INTEGRATED")
		}
		if _, e = git(p.Workspace, "merge", "--no-ff", "--no-edit", "-m", marker, p.SourceCommit); e != nil {
			conflicts, _ := git(p.Workspace, "diff", "--name-only", "--diff-filter=U", "-z")
			for _, path := range strings.Split(string(conflicts), "\x00") {
				if path != "" && len(o.ConflictFiles) < 500 {
					o.ConflictFiles = append(o.ConflictFiles, path)
				}
			}
			// Abort only the merge this command started; a pre-existing MERGE_HEAD is
			// rejected by the clean/merge preflight below and is never discarded.
			if _, abortErr := git(p.Workspace, "merge", "--abort"); abortErr != nil {
				return finish("MERGE_ABORT_FAILED")
			}
			after, _ := text(p.Workspace, "rev-parse", "HEAD")
			if after != before {
				return finish("TARGET_CHANGED_DURING_MERGE")
			}
			if len(o.ConflictFiles) > 0 {
				o.State = "CONFLICT"
				return finish("MERGE_CONFLICT")
			}
			return finish("MERGE_FAILED")
		}
		merged, e := text(p.Workspace, "rev-parse", "HEAD")
		parents, e2 := text(p.Workspace, "log", "-1", "--format=%P")
		if e != nil || e2 != nil || parents != before+" "+p.SourceCommit || !validMergeCommit(merged) {
			return finish("MERGE_OBJECT_INVALID")
		}
		o.MergeCommit = merged
	}
	if _, e = git(p.Workspace, "merge-base", "--is-ancestor", p.SourceCommit, "refs/heads/"+p.TargetBranch); e != nil {
		return finish("CONTAINMENT_FAILED")
	}
	o.Contained = true
	o.State = "MERGED"
	if p.Push {
		if _, e = git(p.Workspace, "push", "--porcelain", "origin", "HEAD:refs/heads/"+p.TargetBranch); e != nil {
			return finish("PUSH_FAILED")
		}
		o.Pushed = true
	}
	return finish("MERGE_COMMITTED")
}
