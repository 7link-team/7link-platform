package executor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestCodexApprovalUsesOfficialAppServerItemGrant(t *testing.T) {
	input := `{"id":0,"result":{"userAgent":"actual"}}
{"id":1,"result":{"thread":{"id":"thread-1"}}}
{"id":2,"result":{"turn":{"id":"turn-1"}}}
{"id":17,"method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","command":"private command"}}
{"method":"item/completed","params":{"threadId":"thread-1","item":{"type":"agentMessage","text":"Actual runtime response"}}}
{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}
`
	var wire bytes.Buffer
	calls := 0
	plan := Plan{Runtime: "CODEX", Model: "configured", Workdir: "/scratch", RequestPermission: func(_ context.Context, r PermissionRequest) (PermissionDecision, error) {
		calls++
		if r.RequestID != "codex:17" || !strings.Contains(string(r.Input), "private command") {
			t.Error("request identity lost")
		}
		return PermissionDecision{State: "ALLOW"}, nil
	}}
	data, err := io.ReadAll(codexApprovalStream(context.Background(), strings.NewReader(input), &wire, plan, "Task"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || strings.Contains(string(data), "private command") || !strings.Contains(wire.String(), `"decision":"accept"`) || strings.Contains(wire.String(), "acceptForSession") {
		t.Fatalf("incorrect item-only permission %s", wire.String())
	}
	if err := Decode("CODEX", bytes.NewReader(data), nil); err != nil {
		t.Fatal(err)
	}
}
func TestCodexApprovalRejectsWrongThreadAndUnsupportedRequests(t *testing.T) {
	for _, input := range []string{`{"id":17,"method":"item/commandExecution/requestApproval","params":{"threadId":"foreign"}}`, `{"error":{"code":1}}`, `{"id":0,"result":{}}
{"id":1,"result":{"thread":{"id":"t"}}}
{"id":8,"method":"item/permissions/requestApproval","params":{"threadId":"t"}}`} {
		_, err := io.ReadAll(codexApprovalStream(context.Background(), strings.NewReader(input), io.Discard, Plan{}, "Task"))
		if err == nil {
			t.Fatal("untrusted runtime grant")
		}
	}
}

func TestCodexApprovalHonorsRuntimeAvailableDecisions(t *testing.T) {
	for _, decisions := range []string{`[]`, `["decline","cancel"]`, `["acceptForSession","decline"]`, `[{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["echo"]}},"decline"]`} {
		input := "{\"id\":0,\"result\":{}}\n{\"id\":1,\"result\":{\"thread\":{\"id\":\"t\"}}}\n" +
			`{"id":7,"method":"item/commandExecution/requestApproval","params":{"threadId":"t","availableDecisions":` + decisions + `}}` + "\n" +
			"{\"method\":\"turn/completed\",\"params\":{\"threadId\":\"t\",\"turn\":{\"status\":\"completed\"}}}\n"
		calls := 0
		plan := Plan{RequestPermission: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			calls++
			return PermissionDecision{State: "ALLOW"}, nil
		}}
		var wire bytes.Buffer
		_, err := io.ReadAll(codexApprovalStream(t.Context(), strings.NewReader(input), &wire, plan, "task"))
		if err != nil || calls != 0 || strings.Contains(wire.String(), `"decision":"accept"`) || !strings.Contains(wire.String(), `"decision":"decline"`) {
			t.Fatalf("runtime excludes item grant; err=%v calls=%d wire=%s", err, calls, wire.String())
		}
	}
}
