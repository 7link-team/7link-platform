package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestClaudeApprovalControlIsExplicitAndDoesNotLeakInput(t *testing.T) {
	for _, state := range []string{"ALLOW", "DENY"} {
		t.Run(state, func(t *testing.T) {
			input := `{"type":"control_response","response":{"subtype":"success","request_id":"runner_initialize"}}
{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"private command"}}}
{"type":"result","subtype":"success","result":"done"}
`
			var response bytes.Buffer
			calls := 0
			stream := claudeApprovalStream(context.Background(), strings.NewReader(input), &response, func(_ context.Context, r PermissionRequest) (PermissionDecision, error) {
				calls++
				if r.Tool != "Bash" || r.RequestID != "r1" {
					t.Error("wrong permission identity")
				}
				return PermissionDecision{State: state, Reason: "Human decision"}, nil
			})
			observed, err := io.ReadAll(stream)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || strings.Contains(string(observed), "private command") {
				t.Fatal("permission leaked or handler omitted")
			}
			var event struct {
				Response struct {
					Response struct {
						Behavior string          `json:"behavior"`
						Input    json.RawMessage `json:"updatedInput"`
					} `json:"response"`
				} `json:"response"`
			}
			if err = json.Unmarshal(response.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Response.Response.Behavior != strings.ToLower(state) {
				t.Fatal(response.String())
			}
			if state == "ALLOW" && !strings.Contains(string(event.Response.Response.Input), "private command") {
				t.Fatal("approved input changed")
			}
		})
	}
}
func TestClaudeApprovalRejectsUninitializedAndUnsupportedControl(t *testing.T) {
	for _, input := range []string{`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{}}}`, `invalid`} {
		_, err := io.ReadAll(claudeApprovalStream(context.Background(), strings.NewReader(input), io.Discard, nil))
		if err == nil {
			t.Fatal("untrusted stream accepted")
		}
	}
}
