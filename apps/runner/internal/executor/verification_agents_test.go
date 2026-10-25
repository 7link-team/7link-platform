package executor

import (
	"testing"
)

func TestAgentVerificationRequiresActualTerminalRuntimeReportForExactCriterion(t *testing.T) {
	source := runtimeFixture(t)
	base := VerificationPlan{ID: "1", Nonce: "nonce", ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "AGENT_JUDGE", SourceWorkdir: source.Workdir, HeadCommit: source.BaseCommit, TimeoutSec: 10, ReviewerID: "4", ReviewerRuntime: "CODEX", ReviewerModel: "judge", ReviewerFamily: "review-family", Stage: "AGENT_TEST", CriterionText: "Review this exact fixture criterion."}
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		t.Run(runtime, func(t *testing.T) {
			p := base
			p.ReviewerRuntime = runtime
			o, err := RunVerification(t.Context(), p)
			if err != nil || o.Reason != "AGENT_REPORTED" || o.AgentReport == nil || o.AgentReport.CriterionID != "3" || o.AgentReport.HeadCommit != source.BaseCommit || o.AgentReport.Verdict != "PASSED" {
				t.Fatalf("runtime report: %+v %v", o, err)
			}
		})
	}
	for _, tc := range []struct{ model, reason string }{{"judge-invalid", "REPORT_UNPARSEABLE"}, {"judge-partial", "AGENT_RUNTIME_INCOMPLETE"}, {"judge-unverified", "AGENT_REPORTED"}, {"judge-major", "AGENT_REPORTED"}} {
		t.Run(tc.model, func(t *testing.T) {
			p := base
			p.ReviewerModel = tc.model
			o, err := RunVerification(t.Context(), p)
			if err != nil || o.Reason != tc.reason {
				t.Fatalf("actual report outcome: %+v %v", o, err)
			}
			if tc.model == "judge-unverified" && len(o.AgentReport.Unverified) != 1 {
				t.Fatal("unverified items erased")
			}
			if tc.model == "judge-major" && len(o.AgentReport.Findings) != 1 {
				t.Fatal("findings erased")
			}
		})
	}
}
