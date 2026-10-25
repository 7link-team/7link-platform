// Package inspection reports observed local executables without invoking a model.
package inspection

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
)

type Executable struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Report struct {
	OS           string       `json:"os"`
	Arch         string       `json:"arch"`
	InspectedAt  time.Time    `json:"inspected_at"`
	Executables  []Executable `json:"executables"`
	RemoteAccess string       `json:"remote_access"`
}

// Inspect only runs fixed version commands for explicit runtime/tool names.
// Authentication, supported models, Skills and MCP are not inferred from a
// binary's presence. They require a separate, runtime-specific inspection.
func Inspect(ctx context.Context) Report {
	return inspect(ctx, exec.LookPath, version, time.Now)
}
func inspect(ctx context.Context, find func(string) (string, error), probe func(context.Context, string) (string, error), now func() time.Time) Report {
	r := Report{OS: runtime.GOOS, Arch: runtime.GOARCH, InspectedAt: now().UTC().Truncate(time.Millisecond), RemoteAccess: "UNAVAILABLE", Executables: []Executable{}}
	for _, spec := range []struct{ name, kind string }{{"codex", "CODEX"}, {"claude", "CLAUDE_CODE"}, {"node", "TOOL"}, {"python", "TOOL"}, {"ffmpeg", "TOOL"}, {"ffprobe", "TOOL"}} {
		item := Executable{Name: spec.name, Kind: spec.kind}
		path, err := find(spec.name)
		if err != nil {
			item.Error = "NOT_FOUND"
			r.Executables = append(r.Executables, item)
			continue
		}
		item.Path = path
		child, cancel := context.WithTimeout(ctx, 3*time.Second)
		v, err := probe(child, path)
		if err != nil {
			if errors.Is(child.Err(), context.DeadlineExceeded) {
				item.Error = "TIMEOUT"
			} else {
				item.Error = "VERSION_FAILED"
			}
		} else {
			value := firstLine(v)
			if value == "" || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				item.Error = "VERSION_FAILED"
			} else {
				item.Available = true
				item.Version = value
			}
		}
		cancel()
		r.Executables = append(r.Executables, item)
	}
	return r
}
func firstLine(v string) string {
	v = strings.TrimSpace(v)
	if line, _, found := strings.Cut(v, "\n"); found {
		v = strings.TrimSpace(line)
	}
	// Do not persist unbounded version output from an unexpected executable.
	r := []rune(v)
	if len(r) > 64 {
		v = string(r[:64])
	}
	return v
}

type boundedOutput struct{ data []byte }

func (w *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if left := 4096 - len(w.data); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func version(ctx context.Context, path string) (string, error) {
	args := []string{"--version"}
	// FFmpeg tools use a single dash; no shell is involved.
	name := strings.ToLower(path)
	if strings.HasSuffix(name, "ffmpeg") || strings.HasSuffix(name, "ffmpeg.exe") || strings.HasSuffix(name, "ffprobe") || strings.HasSuffix(name, "ffprobe.exe") {
		args = []string{"-version"}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	hideWindow(cmd)
	configureProbeCancellation(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	w := &boundedOutput{}
	cmd.Stdout = w
	cmd.Stderr = w
	err := cmd.Run()
	return string(w.data), err
}
