package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"time"
)

type PermissionRequest struct {
	RequestID string          `json:"request_id"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
}
type PermissionDecision struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}
type PermissionHandler func(context.Context, PermissionRequest) (PermissionDecision, error)

// claudeApprovalStream consumes official bidirectional control requests. Tool
// inputs are sent only to the decision channel, never to runtime observations.
func claudeApprovalStream(ctx context.Context, in io.Reader, out io.Writer, handle PermissionHandler, prompt ...string) io.Reader {
	reader, writer := io.Pipe()
	go func() {
		var final error
		defer func() { _ = writer.CloseWithError(final) }()
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		encoder := json.NewEncoder(out)
		initialized := false
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			var event struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype string          `json:"subtype"`
					Tool    string          `json:"tool_name"`
					Input   json.RawMessage `json:"input"`
				} `json:"request"`
				Response struct {
					Subtype   string `json:"subtype"`
					RequestID string `json:"request_id"`
				} `json:"response"`
			}
			if json.Unmarshal(line, &event) != nil {
				final = ErrUnknownResult
				return
			}
			if event.Type == "control_response" {
				if event.Response.RequestID == "runner_initialize" && event.Response.Subtype == "success" {
					initialized = true
					if len(prompt) > 0 {
						if err := encoder.Encode(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt[0]}, "parent_tool_use_id": nil, "session_id": ""}); err != nil {
							final = err
							return
						}
					}
				}
				continue
			}
			if event.Type == "control_request" {
				if !initialized || event.Request.Subtype != "can_use_tool" || event.RequestID == "" || len(event.RequestID) > 160 || len(event.Request.Tool) > 160 || len(event.Request.Input) > 16384 || !json.Valid(event.Request.Input) {
					final = ErrUnsupported
					return
				}
				decision := PermissionDecision{State: "DENY", Reason: "Permission bridge unavailable"}
				if handle != nil {
					wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
					var err error
					decision, err = handle(wait, PermissionRequest{RequestID: event.RequestID, Tool: event.Request.Tool, Input: event.Request.Input})
					cancel()
					if err != nil {
						decision = PermissionDecision{State: "DENY", Reason: "Permission request expired or unavailable"}
					}
				}
				response := map[string]any{"behavior": "deny", "message": decision.Reason}
				if decision.State == "ALLOW" {
					response = map[string]any{"behavior": "allow", "updatedInput": event.Request.Input}
				}
				if err := encoder.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": event.RequestID, "response": response}}); err != nil {
					final = err
					return
				}
				continue
			}
			if _, err := writer.Write(append(line, '\n')); err != nil {
				return
			}
			if event.Type == "result" {
				if closer, ok := out.(io.Closer); ok {
					_ = closer.Close()
				}
				return
			}
		}
		final = scanner.Err()
	}()
	return reader
}
