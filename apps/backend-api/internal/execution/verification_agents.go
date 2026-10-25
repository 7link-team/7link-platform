package execution

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// verificationReviewersOn ranks stored cross-family candidates before same-family
// candidates. It
// grants no authority: delivery prelocks the returned principals and rechecks
// membership/project policy; pinReviewerOn revalidates the selected identity.
func (m *Module) verificationReviewersOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, stage string) ([]guid.ID, error) {
	role := ""
	switch stage {
	case "AGENT_TEST":
		role = "TESTER"
	case "AGENT_ACCEPTANCE":
		role = "ACCEPTOR"
	default:
		return nil, ErrInvalidArgument
	}
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	v, err := view(r)
	if err != nil {
		return nil, err
	}
	out := []guid.ID{}
	fallback := []guid.ID{}
	if v.Plan.ExecutorPrincipalID <= 0 || v.Plan.ModelFamily == "" || v.Plan.Model == "" {
		return out, nil
	}
	if m.agents.ActiveByRoleOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	candidates, err := m.agents.ActiveByRoleOn(ctx, tx, tenant, role)
	if err != nil {
		return nil, err
	}
	for _, a := range candidates {
		if a.TenantID != tenant || a.PrincipalID <= 0 || a.PrincipalID == v.Plan.ExecutorPrincipalID || a.State != "ACTIVE" || !slices.Contains(a.Roles, role) || a.ModelFamily == "" || a.ModelID == "" || a.ModelID == v.Plan.Model || !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, a.RuntimeKind) {
			continue
		}
		if a.ModelFamily == v.Plan.ModelFamily {
			fallback = append(fallback, a.PrincipalID)
		} else {
			out = append(out, a.PrincipalID)
		}
	}
	slices.Sort(out)
	slices.Sort(fallback)
	return append(slices.Compact(out), slices.Compact(fallback)...), nil
}

// pinExecutionIdentityOn ignores posted identity/family fields. Only the actual
// assigned Agent's matching runtime/model can supply a frozen source family.
func (m *Module) pinExecutionIdentityOn(ctx context.Context, tx *gorm.DB, l WorkLink, p *ExecutionPlan) error {
	p.ExecutorPrincipalID, p.ModelFamily, p.AgentSystemPrompt = 0, "", ""
	if m.work.AssignedAgentOn == nil || m.agents.ByPrincipalOn == nil {
		return nil
	}
	id, err := m.work.AssignedAgentOn(ctx, tx, l)
	if err != nil {
		return err
	}
	if id <= 0 {
		return nil
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, id)
	if err != nil {
		return err
	}
	if found && agent.State == "ACTIVE" && agent.PrincipalID == id && agent.ModelFamily != "" && agent.RuntimeKind == p.Runtime && agent.ModelID == p.Model && slices.Contains(agent.Roles, "EXECUTOR") {
		if !utf8.ValidString(agent.SystemPrompt) || len(agent.SystemPrompt) > 20000 {
			return ErrInvalidArgument
		}
		p.ExecutorPrincipalID, p.ModelFamily, p.AgentSystemPrompt = id, agent.ModelFamily, agent.SystemPrompt
	}
	return nil
}

func agentVerificationVerdict(p VerificationPlan, o VerificationObservation) (string, string) {
	if o.Reason != "AGENT_REPORTED" || o.AgentReport == nil {
		return "INCONCLUSIVE", o.Reason
	}
	if o.HeadCommit != p.HeadCommit || o.AgentReport.HeadCommit != p.HeadCommit || o.AgentReport.CriterionID != p.CriterionID {
		return "INCONCLUSIVE", "AGENT_REPORT_BINDING_MISMATCH"
	}
	if digest, err := hex.DecodeString(o.ReportSHA256); err != nil || len(digest) != 32 {
		return "INCONCLUSIVE", "REPORT_UNPARSEABLE"
	}
	r := o.AgentReport
	if r.Findings == nil || r.Unverified == nil || len(r.Findings) > 100 || len(r.Unverified) > 100 || !slices.Contains([]string{"PASSED", "FAILED", "INCONCLUSIVE"}, r.Verdict) {
		return "INCONCLUSIVE", "REPORT_UNPARSEABLE"
	}
	major := false
	for _, finding := range r.Findings {
		if !slices.Contains([]string{"INFO", "MINOR", "MAJOR", "BLOCKER"}, finding.Severity) || !bounded(finding.Summary, 2000) {
			return "INCONCLUSIVE", "REPORT_UNPARSEABLE"
		}
		if finding.Severity == "MAJOR" || finding.Severity == "BLOCKER" {
			major = true
		}
	}
	if major || r.Verdict == "FAILED" {
		return "FAILED", "AGENT_FINDINGS"
	}
	if len(r.Unverified) > 0 || r.Verdict == "INCONCLUSIVE" {
		return "INCONCLUSIVE", "AGENT_UNVERIFIED"
	}
	return "PASSED", "AGENT_REPORTED"
}

// Reviewer identity comes from fleet's owned records, never from posted model
// family text. The delivery command locks participants before its project lock.
func (m *Module) pinReviewerOn(ctx context.Context, tx *gorm.DB, in VerificationRequest, source ExecutionPlan, p *VerificationPlan) error {
	if in.ReviewerPrincipalID <= 0 || source.ExecutorPrincipalID <= 0 || source.ModelFamily == "" || m.agents.ByPrincipalOn == nil {
		return ErrCapabilityUnavailable
	}
	if in.ReviewerPrincipalID == source.ExecutorPrincipalID {
		return ErrForbidden
	}
	role := ""
	switch in.Stage {
	case "AGENT_TEST":
		role = "TESTER"
	case "AGENT_ACCEPTANCE":
		role = "ACCEPTOR"
	default:
		return ErrInvalidArgument
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, in.TenantID, in.ReviewerPrincipalID)
	if err != nil {
		return err
	}
	if !found || agent.PrincipalID != in.ReviewerPrincipalID || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, role) {
		return ErrForbidden
	}
	if agent.ModelFamily == "" || agent.ModelID == "" || agent.ModelID == source.Model || !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, agent.RuntimeKind) {
		return ErrCapabilityUnavailable
	}
	if agent.ModelFamily == source.ModelFamily && !in.AllowFamilyFallback {
		return ErrCapabilityUnavailable
	}
	if !utf8.ValidString(in.CriterionText) || strings.TrimSpace(in.CriterionText) == "" || len(in.CriterionText) > 10000 {
		return ErrInvalidArgument
	}
	p.ReviewerID, p.ReviewerRuntime, p.ReviewerModel, p.ReviewerFamily, p.Stage, p.CriterionText = in.ReviewerPrincipalID.String(), agent.RuntimeKind, agent.ModelID, agent.ModelFamily, in.Stage, in.CriterionText
	p.FamilyFallback = agent.ModelFamily == source.ModelFamily
	return nil
}
