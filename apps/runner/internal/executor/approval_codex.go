package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// codexApprovalStream drives the official stdio app-server protocol. The exec
// JSON output is not an approval transport. Grants apply to this item only.
func codexApprovalStream(ctx context.Context, in io.Reader, out io.Writer, plan Plan, prompt string) io.Reader {
	reader, writer := io.Pipe()
	go func() {
		var final error
		defer func() { _ = writer.CloseWithError(final) }()
		send := json.NewEncoder(out)
		emit := json.NewEncoder(writer)
		if err := send.Encode(map[string]any{"id": 0, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "execution_runner", "version": "1"}}}); err != nil {
			final = err
			return
		}
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		threadID := ""
		total := 0
		for scanner.Scan() {
			line := scanner.Bytes()
			total += len(line) + 1
			if total > 32*1024*1024 {
				final = ErrUnknownResult
				return
			}
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Error  json.RawMessage `json:"error"`
				Result struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				} `json:"result"`
				Params struct {
					AvailableDecisions []json.RawMessage `json:"availableDecisions"`
					ThreadID           string            `json:"threadId"`
					Turn               struct {
						Status string `json:"status"`
					} `json:"turn"`
					Item struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				} `json:"params"`
			}
			if json.Unmarshal(line, &msg) != nil || len(msg.Error) > 0 {
				final = ErrRuntimeFailed
				return
			}
			if msg.Method == "" {
				switch string(msg.ID) {
				case "0":
					if err := send.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
						final = err
						return
					}
					if err := send.Encode(map[string]any{"id": 1, "method": "thread/start", "params": map[string]any{"model": plan.Model, "cwd": plan.Workdir, "approvalPolicy": "unlessTrusted", "sandbox": "workspaceWrite"}}); err != nil {
						final = err
						return
					}
				case "1":
					threadID = msg.Result.Thread.ID
					if threadID == "" {
						final = ErrUnknownResult
						return
					}
					if err := send.Encode(map[string]any{"id": 2, "method": "turn/start", "params": map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": prompt}}, "approvalPolicy": "unlessTrusted", "sandboxPolicy": map[string]any{"type": "workspaceWrite", "writableRoots": []string{plan.Workdir}, "networkAccess": false}}}); err != nil {
						final = err
						return
					}
				}
				continue
			}
			if len(msg.ID) > 0 {
				if threadID == "" || msg.Params.ThreadID != threadID {
					final = ErrUnknownResult
					return
				}
				switch msg.Method {
				case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
					var payload map[string]json.RawMessage
					if json.Unmarshal(line, &payload) != nil || len(payload["params"]) > 16384 {
						final = ErrUnsupported
						return
					}
					decision := PermissionDecision{State: "DENY"}
					canAccept := msg.Params.AvailableDecisions == nil
					for _, available := range msg.Params.AvailableDecisions {
						canAccept = canAccept || string(available) == `"accept"`
					}
					if canAccept && plan.RequestPermission != nil {
						wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
						var err error
						decision, err = plan.RequestPermission(wait, PermissionRequest{RequestID: "codex:" + string(msg.ID), Tool: msg.Method, Input: payload["params"]})
						cancel()
						if err != nil {
							decision.State = "DENY"
						}
					}
					value := "decline"
					if decision.State == "ALLOW" {
						value = "accept"
					}
					if err := send.Encode(map[string]any{"id": msg.ID, "result": map[string]any{"decision": value}}); err != nil {
						final = err
						return
					}
				default:
					// Unsupported tool/elicitation/permission variants never inherit a grant.
					final = ErrUnsupported
					return
				}
				continue
			}
			switch msg.Method {
			case "item/completed":
				if msg.Params.Item.Type == "agentMessage" {
					if err := emit.Encode(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": msg.Params.Item.Text}}); err != nil {
						return
					}
				}
			case "turn/completed":
				if msg.Params.ThreadID != threadID || msg.Params.Turn.Status != "completed" {
					final = ErrRuntimeFailed
					return
				}
				if err := emit.Encode(map[string]any{"type": "turn.completed"}); err != nil {
					return
				}
				if closer, ok := out.(io.Closer); ok {
					_ = closer.Close()
				}
				return
			case "error":
				final = ErrRuntimeFailed
				return
			}
		}
		final = scanner.Err()
		if final == nil {
			final = ErrUnknownResult
		}
	}()
	return reader
}

func validApprovalProtocol(plan Plan) bool {
	return (plan.Runtime == "CLAUDE_CODE" && plan.ApprovalProtocol == "CLAUDE_CONTROL_V1") || (plan.Runtime == "CODEX" && strings.EqualFold(plan.ApprovalProtocol, "CODEX_APP_SERVER_V1"))
}
