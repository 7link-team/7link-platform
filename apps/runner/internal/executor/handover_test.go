package executor

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestHandoverRunsRealRuntimeSubprocessWithoutGitAndDeletesOwnWorkspace(t *testing.T) {
	p := runtimeFixture(t)
	p.Model = "quality-feedback"
	p.Workdir = ""
	p.BaseCommit = ""
	p.Objective = "Actual handover source facts and requested document"
	p.ExecutorPrincipalID = "21"
	p.Handover = &HandoverPlan{RetirementID: "31", AgentPrincipalID: "21"}
	for _, runtime := range []string{"CODEX", "CLAUDE_CODE"} {
		p.Runtime = runtime
		_, args, err := Command(p)
		if err != nil {
			t.Fatal(err)
		}
		if runtime == "CODEX" && !slices.Contains(args, "--skip-git-repo-check") {
			t.Fatal("source-independent purpose still requires Git")
		}
		var workspace string
		result, err := Run(t.Context(), p, func(o Observation) error {
			if o.Kind == "WORKSPACE_READY" {
				workspace = o.Content
			}
			return nil
		})
		if err != nil || !result.RuntimeCompleted || result.HandoverDocument != p.Objective || result.HeadCommit != "" || result.Workspace != "" {
			t.Fatalf("actual runtime document mismatch: runtime=%s result=%+v err=%v", runtime, result, err)
		}
		if workspace == "" {
			t.Fatal("no private scratch workspace observed")
		}
		if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("handover scratch retained: %v", err)
		}
	}
}

func TestHandoverRejectsBorrowedPurposeAndWriteCapabilities(t *testing.T) {
	base := Plan{Runtime: "CODEX", Model: "observed", Objective: "Write handover", AuthorizationMode: "READ_ONLY", ExecutorPrincipalID: "21", Handover: &HandoverPlan{RetirementID: "31", AgentPrincipalID: "21"}}
	for _, mutate := range []func(*Plan){func(p *Plan) { p.AuthorizationMode = "WORKSPACE_WRITE" }, func(p *Plan) { p.BaseCommit = "invented" }, func(p *Plan) { p.ExecutorPrincipalID = "22" }, func(p *Plan) { p.Distillation = &DistillationPlan{} }, func(p *Plan) { p.Handover = &HandoverPlan{RetirementID: "0", AgentPrincipalID: "21"} }} {
		p := base
		mutate(&p)
		if _, _, err := Command(p); !errors.Is(err, ErrUnsupported) {
			t.Fatal("invalid purpose accepted", err)
		}
	}
}
