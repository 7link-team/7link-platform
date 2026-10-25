package delivery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// localGitCommand uses no shell and hides native child windows on Windows.
func localGitCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.pager=cat", "-c", "core.hooksPath=", "-C", path}, args...)...)
	command.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	if runtime.GOOS == "windows" {
		command.SysProcAttr = &syscall.SysProcAttr{}
		attrs := reflect.ValueOf(command.SysProcAttr).Elem()
		if f := attrs.FieldByName("HideWindow"); f.IsValid() && f.CanSet() {
			f.SetBool(true)
		}
		if f := attrs.FieldByName("CreationFlags"); f.IsValid() && f.CanSet() {
			f.SetUint(0x08000000)
		}
	}
	return command
}

type codeGitBuffer struct {
	bytes.Buffer
	limit int
}

func (b *codeGitBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrUnavailable
	}
	return b.Buffer.Write(p)
}
func localGitOutput(ctx context.Context, path string, limit int, args ...string) ([]byte, error) {
	command := localGitCommand(ctx, path, args...)
	buffer := &codeGitBuffer{limit: limit}
	command.Stdout = buffer
	if err := command.Run(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// ReadLocalGit reads actual objects from an administrator-configured local
// repository. It never clones, fetches, runs hooks, changes files or contacts a
// remote. A web caller can name a repository ID, never an arbitrary server path.
func ReadLocalGit(ctx context.Context, path string) ([]LocalGitObject, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrInvalidArgument
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, ErrUnavailable
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// NUL separates the immutable SHA, message and next object without a shell.
	output, err := localGitOutput(deadline, resolved, 2*1024*1024, "log", "--all", "--max-count=100", "--format=%H%x00%B%x00")
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if len(output) > 2*1024*1024 {
		return nil, ErrUnavailable
	}
	objects := []LocalGitObject{}
	fields := strings.Split(string(output), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		ref := strings.TrimSpace(fields[i])
		if ref == "" {
			continue
		}
		if !commitRef.MatchString(ref) {
			return nil, ErrUnavailable
		}
		objects = append(objects, LocalGitObject{Kind: "COMMIT", Ref: ref, Title: fields[i+1]})
	}
	branches, err := localGitOutput(deadline, resolved, 100*201, "for-each-ref", "--count=100", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if len(branches) > 100*201 {
		return nil, ErrUnavailable
	}
	for _, branch := range strings.Split(strings.TrimSpace(string(branches)), "\n") {
		branch = strings.TrimSpace(branch)
		if branch != "" {
			objects = append(objects, LocalGitObject{Kind: "BRANCH", Ref: branch, Title: branch})
		}
	}
	return objects, nil
}
