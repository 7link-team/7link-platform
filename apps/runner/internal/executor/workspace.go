package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Workspace Git commands never inherit hooks, signing, credentials or a shell.
func workspaceGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-C", dir}, args...)...)
	hideWindow(cmd)
	configureExecutionCancellation(cmd, ctx)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = verificationEnvironment()
	cmd.Stderr = io.Discard
	var output bytes.Buffer
	cmd.Stdout = &boundedOutput{buffer: &output, limit: 65536}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: workspace Git operation failed", ErrRuntimeFailed)
	}
	return output.Bytes(), nil
}

type boundedOutput struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.buffer.Len() {
		return 0, fmt.Errorf("executor: artifact exceeds 64 KiB")
	}
	return w.buffer.Write(p)
}

func createWorkspace(ctx context.Context, p Plan) (string, error) {
	root, err := filepath.EvalSymlinks(p.Workdir)
	if err != nil {
		return "", ErrUnsupported
	}
	top, err := workspaceGit(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	repository, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil || !sameWorkspacePath(repository, root) {
		return "", fmt.Errorf("%w: registered workdir must be the repository root", ErrUnsupported)
	}
	parent := filepath.Join(root, ".runner-executions")
	if err = os.Mkdir(parent, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(parent)
	if err != nil || !sameWorkspacePath(actual, parent) {
		return "", fmt.Errorf("%w: execution workspace escapes registered workdir", ErrUnsupported)
	}
	dir, err := os.MkdirTemp(parent, "execution-")
	if err != nil {
		return "", err
	}
	branch := "runner/" + filepath.Base(dir)
	// The empty directory is reserved atomically, preventing attempt collisions.
	if _, err = workspaceGit(ctx, root, "worktree", "add", "-b", branch, dir, p.BaseCommit); err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	return dir, nil
}

func finishWorkspace(ctx context.Context, p Plan, result Result) (Result, error) {
	unknown := Result{ResultState: "UNKNOWN", Workspace: p.Workdir}
	branch, err := workspaceGit(ctx, p.Workdir, "symbolic-ref", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != "refs/heads/runner/"+filepath.Base(p.Workdir) {
		return unknown, fmt.Errorf("%w: execution branch changed", ErrRuntimeFailed)
	}
	if _, err = workspaceGit(ctx, p.Workdir, "merge-base", "--is-ancestor", p.BaseCommit, "HEAD"); err != nil {
		return unknown, err
	}
	if _, err := workspaceGit(ctx, p.Workdir, "add", "--all", "--", "."); err != nil {
		return unknown, err
	}
	status, err := workspaceGit(ctx, p.Workdir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return unknown, err
	}
	if len(status) > 0 {
		if _, err = workspaceGit(ctx, p.Workdir, "-c", "user.name=Runner", "-c", "user.email=runner@localhost", "commit", "--no-verify", "-m", "Record execution workspace changes"); err != nil {
			return unknown, err
		}
	}
	head, err := workspaceGit(ctx, p.Workdir, "rev-parse", "HEAD")
	if err != nil {
		return unknown, err
	}
	result.HeadCommit = strings.TrimSpace(string(head))
	// Capture the entire execution delta, including commits made by the runtime.
	result.Diff, err = workspaceGit(ctx, p.Workdir, "diff", "--binary", "--no-ext-diff", "--no-textconv", p.BaseCommit, result.HeadCommit, "--")
	if err != nil {
		return unknown, err
	}
	result.Workspace = p.Workdir
	result.Artifacts, err = capturePlannedArtifacts(ctx, p, result.HeadCommit)
	if err != nil {
		return unknown, err
	}
	result.Progress, err = captureProgress(ctx, p, result.HeadCommit)
	if err != nil {
		return unknown, err
	}
	return result, nil
}

func sameWorkspacePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
