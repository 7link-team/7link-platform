package executor

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCorrelationActualProcessReceivesOnlyFrozenTraceparent(t *testing.T) {
	if os.Getenv("TRACE_PROCESS_HELPER") == "1" {
		if os.Getenv("BAGGAGE") != "" || os.Getenv("TRACESTATE") != "" || os.Getenv("MACHINE_SECRET") != "" {
			os.Exit(9)
		}
		_, _ = os.Stdout.WriteString(os.Getenv("TRACEPARENT"))
		return
	}
	c := &Correlation{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), ParentSpanID: strings.Repeat("c", 16)}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCorrelationActualProcessReceivesOnlyFrozenTraceparent$")
	hideWindow(cmd)
	cmd.Env = append(correlationEnvironment(Environment(os.Environ()), c), "TRACE_PROCESS_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("process: %v %s", err, output)
	}
	if !bytes.HasPrefix(output, []byte("00-"+c.TraceID+"-"+c.SpanID+"-01")) {
		t.Fatalf("frozen context absent: %q", output)
	}
	clean := correlationEnvironment([]string{"TRACEPARENT=evil", "traceparent=evil", "BAGGAGE=secret", "TRACESTATE=secret", "PATH=safe"}, c)
	if len(clean) != 2 || clean[0] != "PATH=safe" {
		t.Fatal(clean)
	}
	for _, invalid := range []*Correlation{{}, {TraceID: strings.Repeat("0", 32), SpanID: c.SpanID}, {TraceID: c.TraceID, SpanID: "x\nsecret"}, {TraceID: c.TraceID, SpanID: c.SpanID, ParentSpanID: "invalid"}} {
		if validCorrelation(invalid) {
			t.Fatalf("invalid correlation accepted: %+v", invalid)
		}
		if env := correlationEnvironment([]string{"TRACEPARENT=evil"}, invalid); len(env) != 0 {
			t.Fatal(env)
		}
	}
	if !validCorrelation(nil) {
		t.Fatal("historical absent context must remain supported")
	}
}

func TestCorrelationVerifierFrozenJSONRoundTrip(t *testing.T) {
	// Preserve backend field order and unknown template data for its plan hash.
	const source = `{"registry_verifiers":[{"verifier_id":"9007199254740993","version_id":"9007199254740995","version_no":2,"key":"unit-tests","required":true,"definition_hash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}],"correlation":{"trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","span_id":"bbbbbbbbbbbbbbbb","parent_span_id":"cccccccccccccccc"},"id":"1","nonce":"n","result_id":"2","result_version":1,"criterion_id":"3","method":"COMMAND","source_workdir":"/work","head_commit":"abc","argv":["test"],"timeout_sec":10,"report_path":"","reviewer_id":"","reviewer_runtime":"","reviewer_model":"","reviewer_family":"","stage":"AUTOMATED","criterion_text":"","family_fallback":false}`
	var plan VerificationPlan
	if err := json.Unmarshal([]byte(source), &plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != source {
		t.Fatalf("frozen hash changes across wire: %s", encoded)
	}
}
