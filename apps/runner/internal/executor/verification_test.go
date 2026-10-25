package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationTemplatePinSurvivesTransportAndActualCommandHash(t *testing.T) {
	p := runtimeFixture(t)
	plan := VerificationPlan{TemplateVerifier: json.RawMessage(`{"template_version_id":"77","definition_hash":"frozen","verifier":{"verifier_key":"command","params":{"threshold":70}}}`), ID: "1", Nonce: "nonce", ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "COMMAND", SourceWorkdir: p.Workdir, HeadCommit: p.BaseCommit, Argv: []string{"git", "status", "--porcelain"}, TimeoutSec: 10}
	wire, e := json.Marshal(plan)
	if e != nil {
		t.Fatal(e)
	}
	var transported VerificationPlan
	if e = json.Unmarshal(wire, &transported); e != nil {
		t.Fatal(e)
	}
	o, e := RunVerification(t.Context(), transported)
	if e != nil || o.ExitCode == nil || *o.ExitCode != 0 {
		t.Fatal(o, e)
	}
	digest := sha256.Sum256(wire)
	if o.PlanHash != hex.EncodeToString(digest[:]) {
		t.Fatal("template pin disappeared from the platform command receipt hash")
	}
}

func TestVerificationRunsExactDetachedCommitAndPreservesDirtySource(t *testing.T) {
	p := runtimeFixture(t)
	if err := os.WriteFile(filepath.Join(p.Workdir, "dirty.txt"), []byte("source only"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := VerificationPlan{ID: "1", Nonce: strings.Repeat("a", 64), ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "COMMAND", SourceWorkdir: p.Workdir, HeadCommit: p.BaseCommit, Argv: []string{"git", "status", "--porcelain"}, TimeoutSec: 10}
	o, err := RunVerification(context.Background(), plan)
	if err != nil || o.ExitCode == nil || *o.ExitCode != 0 || o.Reason != "PROCESS_EXITED" || o.HeadCommit != p.BaseCommit {
		t.Fatalf("observation=%+v err=%v", o, err)
	}
	if o.OutputSHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("dirty source leaked into copy: %+v", o)
	}
	if b, err := os.ReadFile(filepath.Join(p.Workdir, "dirty.txt")); err != nil || string(b) != "source only" {
		t.Fatal("source modified", err)
	}
	plan.Argv = []string{"git", "rev-parse", "--verify", "refs/heads/nonexistent"}
	o, err = RunVerification(t.Context(), plan)
	if err != nil || o.ExitCode == nil || *o.ExitCode == 0 {
		t.Fatalf("nonzero exit lost: %+v %v", o, err)
	}
	plan.Argv = []string{"missing-verification-command"}
	o, err = RunVerification(t.Context(), plan)
	if err != nil || o.ExitCode != nil || o.Reason != "COMMAND_NOT_RUNNABLE" {
		t.Fatalf("unrunnable fabricated exit: %+v %v", o, err)
	}
	plan.Method = "INTERACTION"
	if _, err = RunVerification(t.Context(), plan); err != ErrUnsupported {
		t.Fatal("unsupported interaction accepted", err)
	}
}

func TestVerificationTestReportRequiresActualReport(t *testing.T) {
	p := runtimeFixture(t)
	plan := VerificationPlan{ID: "1", Nonce: "nonce", ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "TEST", SourceWorkdir: p.Workdir, HeadCommit: p.BaseCommit, Argv: []string{"git", "--version"}, TimeoutSec: 10, ReportPath: "report.json"}
	o, err := RunVerification(t.Context(), plan)
	if err != nil || o.Reason != "REPORT_UNPARSEABLE" || o.Passed != nil {
		t.Fatalf("missing report passed: %+v %v", o, err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan.Argv = []string{self, "fixture-verification-report"}
	t.Setenv("OPENAI_API_KEY", "secret-excluded-from-child")
	t.Setenv("MACHINE_SECRET", "machine-secret-excluded-from-child")
	o, err = RunVerification(t.Context(), plan)
	if err != nil || o.Reason != "PROCESS_EXITED" || o.Passed == nil || *o.Passed != 2 {
		t.Fatalf("report not observed: %+v %v", o, err)
	}
	plan.Argv = []string{self, "fixture-verification-report", "bad"}
	o, err = RunVerification(t.Context(), plan)
	if err != nil || o.Reason != "REPORT_UNPARSEABLE" {
		t.Fatalf("trailing garbage accepted: %+v %v", o, err)
	}
	plan.Method = "COMMAND"
	plan.Argv = []string{self, "fixture-verification-timeout"}
	plan.TimeoutSec = 1
	o, err = RunVerification(t.Context(), plan)
	if err != nil || o.Reason != "TIMEOUT_OR_CANCELLED" || o.ExitCode != nil {
		t.Fatalf("timeout verdict fabricated: %+v %v", o, err)
	}
}

func TestVerificationPlanRejectsUnboundUnsupportedAndUnsafeReportPaths(t *testing.T) {
	p := VerificationPlan{ID: "1", Nonce: "nonce", ResultID: "2", ResultVersion: 1, CriterionID: "3", Method: "COMMAND", SourceWorkdir: t.TempDir(), HeadCommit: strings.Repeat("a", 40), Argv: []string{"git", "--version"}, TimeoutSec: 1}
	for _, mutate := range []func(*VerificationPlan){func(p *VerificationPlan) { p.ID = "" }, func(p *VerificationPlan) { p.Nonce = "" }, func(p *VerificationPlan) { p.Method = "AGENT_JUDGE" }, func(p *VerificationPlan) { p.HeadCommit = "invalid" }, func(p *VerificationPlan) { p.Argv = []string{"-option"} }, func(p *VerificationPlan) { p.Argv = []string{"git", "bad\x00"} }, func(p *VerificationPlan) { p.Method = "TEST"; p.ReportPath = "../report.json" }, func(p *VerificationPlan) { p.TimeoutSec = 3601 }} {
		bad := p
		mutate(&bad)
		if _, err := RunVerification(t.Context(), bad); err != ErrUnsupported {
			t.Fatal("invalid platform plan launched", bad, err)
		}
	}
}
