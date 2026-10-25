package executor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SuggestIntake invokes the configured real runtime in a disposable read-only
// directory. It produces model text only: no Execution, claim or evidence.
func SuggestIntake(ctx context.Context, runtime, model, prompt string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "intake-suggestion-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	name, args, err := Command(Plan{Runtime: runtime, Model: model, Workdir: dir, Objective: prompt, AuthorizationMode: "READ_ONLY"})
	if err != nil {
		return nil, err
	}
	if runtime == "CODEX" {
		args = append(args[:len(args)-1], "--skip-git-repo-check", "--ephemeral", "-")
	}
	if runtime == "CLAUDE_CODE" {
		args = append(args, "--no-session-persistence")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, name, args...)
	hideWindow(cmd)
	configureCancellation(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = Environment(os.Environ())
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = io.Discard
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	var result []byte
	decodeErr := Decode(runtime, output, func(ob Observation) error {
		text := ""
		if ob.Kind == "LOG" {
			text = ob.Content
		}
		if runtime == "CLAUDE_CODE" && ob.Kind == "RUNTIME_COMPLETED" {
			var v struct {
				Result string `json:"result"`
			}
			if json.Unmarshal([]byte(ob.Content), &v) == nil {
				text = v.Result
			}
		}
		if text != "" {
			if len(text) > 65535 {
				return ErrUnknownResult
			}
			result = []byte(text)
		}
		return nil
	})
	if decodeErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if decodeErr != nil {
		return nil, decodeErr
	}
	if waitErr != nil {
		return nil, ErrRuntimeFailed
	}
	result = []byte(strings.TrimSpace(string(result)))
	if !json.Valid(result) {
		return nil, ErrUnknownResult
	}
	return result, nil
}
