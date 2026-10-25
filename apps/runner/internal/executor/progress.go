package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ProgressPolicy struct {
	NoProgressLimit         int      `json:"no_progress_limit"`
	DependencyMissLimit     int      `json:"dependency_miss_limit"`
	ObservablePaths         []string `json:"observable_paths"`
	RequiredDependencyPaths []string `json:"required_dependency_paths"`
}
type ProgressObservation struct {
	Observable   map[string]string `json:"observable"`
	Dependencies map[string]bool   `json:"dependencies"`
	ChangedFiles map[string]string `json:"changed_files"`
	Truncated    bool              `json:"truncated"`
	HeadCommit   string            `json:"head_commit,omitempty"`
	Final        bool              `json:"final"`
	Baseline     bool              `json:"baseline"`
}

func progressPath(root, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\:\x00") || filepath.IsAbs(name) {
		return "", ErrUnsupported
	}
	full := filepath.Join(root, filepath.FromSlash(name))
	rel, e := filepath.Rel(root, full)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrUnsupported
	}
	// Inspect each existing ancestor so a missing leaf behind an escaping symlink
	// cannot be misreported as a safe missing input.
	cursor := full
	for {
		resolved, e := filepath.EvalSymlinks(cursor)
		if e == nil {
			r, e := filepath.Rel(root, resolved)
			if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return "", ErrUnsupported
			}
			break
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		next := filepath.Dir(cursor)
		if next == cursor {
			return "", ErrUnsupported
		}
		cursor = next
	}
	return full, nil
}
func measuredFile(root, name string) (string, error) {
	full, e := progressPath(root, name)
	if e != nil {
		return "", e
	}
	f, e := os.Open(full)
	if errors.Is(e, os.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", ErrUnsupported
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func captureProgress(ctx context.Context, p Plan, head string) (*ProgressObservation, error) {
	if p.ProgressRootCommit == "" {
		return nil, nil
	}
	out := &ProgressObservation{Observable: map[string]string{}, Dependencies: map[string]bool{}, ChangedFiles: map[string]string{}, Final: head != "", HeadCommit: head}
	if p.ProgressPolicy != nil {
		for _, name := range p.ProgressPolicy.ObservablePaths {
			digest, e := measuredFile(p.Workdir, name)
			if e != nil {
				return nil, e
			}
			out.Observable[name] = digest
		}
		for _, name := range p.ProgressPolicy.RequiredDependencyPaths {
			full, e := progressPath(p.Workdir, name)
			if e != nil {
				return nil, e
			}
			_, e = os.Stat(full)
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
			out.Dependencies[name] = e == nil
		}
	}
	if head == "" {
		return out, nil
	}
	args := []string{"diff", "--name-only", "-z", "--no-ext-diff", p.ProgressRootCommit}
	if head != "" {
		args = append(args, head)
	}
	args = append(args, "--")
	paths, e := workspaceGit(ctx, p.Workdir, args...)
	if e != nil {
		// An unavailable/incomplete path set cannot support a spinning verdict.
		out.Truncated = true
		return out, nil
	}
	for _, name := range strings.Split(string(paths), "\x00") {
		if name == "" {
			continue
		}
		if _, ok := out.ChangedFiles[name]; ok {
			continue
		}
		if len(out.ChangedFiles) >= 20000 {
			out.Truncated = true
			break
		}
		// Read Git blob identities from the immutable final tree, not mutable files.
		exists, e := workspaceGit(ctx, p.Workdir, "ls-tree", "-z", head, "--", name)
		if e != nil {
			out.Truncated = true
			continue
		}
		if len(exists) == 0 {
			out.ChangedFiles[name] = ""
			continue
		}
		fields := strings.Fields(strings.SplitN(string(exists), "\t", 2)[0])
		if len(fields) != 3 || fields[1] != "blob" {
			out.Truncated = true
			continue
		}
		out.ChangedFiles[name] = fields[2]
	}
	return out, nil
}

func actionCompleted(runtime string, o Observation) bool {
	if o.Kind != "RUNTIME_EVENT" {
		return false
	}
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type string `json:"type"`
		} `json:"item"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(o.Content), &event) != nil {
		return false
	}
	if runtime == "CODEX" {
		if event.Type != "item.completed" {
			return false
		}
		switch event.Item.Type {
		case "command_execution", "file_change", "mcp_tool_call", "web_search":
			return true
		}
		return false
	}
	if runtime == "CLAUDE_CODE" && event.Type == "user" {
		for _, part := range event.Message.Content {
			if part.Type == "tool_result" {
				return true
			}
		}
	}
	return false
}
