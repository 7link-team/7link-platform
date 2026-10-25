package executor

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestHiddenVerifierRunsActualRuntimeAndPublishesOnlyBoundVerdict(t *testing.T) {
	source := runtimeFixture(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(source.Workdir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)
	for _, tc := range []struct{ runtime, family, model, want string }{
		{"CODEX", "openai", "judge", "PASSED"}, {"CLAUDE_CODE", "anthropic", "judge", "PASSED"}, {"CODEX", "openai", "judge-unverified", "INCONCLUSIVE"}, {"CODEX", "openai", "judge-major", "FAILED"}, {"CODEX", "openai", "judge-invalid", "UNAVAILABLE"}, {"CODEX", "openai", "judge-partial", "UNAVAILABLE"},
	} {
		input := HiddenVerifierInput{Nonce: strings.Repeat("a", 64), SuiteHash: strings.Repeat("b", 64), HeadCommit: source.BaseCommit, Model: tc.model, Family: tc.family, Cases: []json.RawMessage{json.RawMessage(`{"steps":"PRIVATE-HIDDEN-CASE"}`)}}
		raw, _ := json.Marshal(input)
		var output bytes.Buffer
		if err := ServeHiddenVerifier(t.Context(), tc.runtime, bytes.NewReader(raw), &output); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "PRIVATE-HIDDEN-CASE") {
			t.Fatal("hidden case leaked into public verdict")
		}
		var result HiddenVerifierOutput
		if json.Unmarshal(output.Bytes(), &result) != nil || result.Verdict != tc.want || result.Nonce != input.Nonce || result.SuiteHash != input.SuiteHash || result.HeadCommit != input.HeadCommit || result.Family != tc.family {
			t.Fatalf("bad receipt: %s", output.String())
		}
	}
}

func TestHiddenVerifierRejectsFamilySpoofAndMalformedInput(t *testing.T) {
	input := HiddenVerifierInput{Nonce: strings.Repeat("a", 64), SuiteHash: strings.Repeat("b", 64), HeadCommit: strings.Repeat("c", 40), Model: "judge", Family: "anthropic", Cases: []json.RawMessage{json.RawMessage(`{}`)}}
	raw, _ := json.Marshal(input)
	for _, request := range []string{string(raw), string(raw) + `{}`, `{"cases":[]}`, `not-json`} {
		var output bytes.Buffer
		if ServeHiddenVerifier(t.Context(), "CODEX", strings.NewReader(request), &output) == nil || output.Len() != 0 {
			t.Fatal("malformed or wrong-family verifier input accepted")
		}
	}
}

func TestHiddenAgentVerdictRejectsMalformedReports(t *testing.T) {
	for _, finding := range []VerificationAgentFinding{{Severity: "CRITICAL", Summary: "fails acceptance"}, {Severity: "MAJOR", Summary: ""}, {Severity: "INFO", Summary: strings.Repeat("x", 4001)}} {
		r := &VerificationAgentReport{Verdict: "PASSED", Findings: []VerificationAgentFinding{finding}, Unverified: []string{}}
		if hiddenAgentVerdict(r) != "INCONCLUSIVE" {
			t.Fatal("malformed report passed")
		}
	}
	r := &VerificationAgentReport{Verdict: "PASSED", Findings: []VerificationAgentFinding{}, Unverified: []string{}}
	if hiddenAgentVerdict(r) != "PASSED" {
		t.Fatal("valid pass refused")
	}
	r.Findings = append(r.Findings, VerificationAgentFinding{Severity: "MAJOR", Summary: "actual failure"})
	if hiddenAgentVerdict(r) != "FAILED" {
		t.Fatal("actual failure lost")
	}
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		_, args, e := Command(Plan{PrivateVerification: true, Runtime: runtime, Model: "available", Workdir: t.TempDir(), Objective: "Review", AuthorizationMode: "READ_ONLY"})
		if e != nil {
			t.Fatal(e)
		}
		flag := "--ephemeral"
		if runtime == "CLAUDE_CODE" {
			flag = "--no-session-persistence"
		}
		if !strings.Contains(strings.Join(args, " "), flag) {
			t.Fatal("private runtime persisted session")
		}
	}
}
