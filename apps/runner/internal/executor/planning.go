package executor

import (
	"context"
	"encoding/json"
	"strings"
)

type PlanningPlan struct {
	Binding       json.RawMessage `json:"binding"`
	Criteria      json.RawMessage `json:"criteria"`
	DefaultBranch string          `json:"default_branch"`
	Review        json.RawMessage `json:"review,omitempty"`
	Feedback      string          `json:"feedback,omitempty"`
}

// Structured frozen inputs are separate from the bounded work objective. They
// are sent intact through stdin, never truncated to fit the Objective envelope.
func planningPrompt(p Plan) (string, error) {
	prompt := p.Objective
	if p.planningInput != nil {
		instructions := "Produce a JSON execution plan only; do not implement or edit any files. Return exactly {\"files\":[...],\"criteria_coverage\":[{\"criterion_id\":\"...\",\"approach\":\"...\",\"verification\":\"...\"}],\"risks\":[...],\"out_of_scope_confirmed\":true,\"binding\":...}. Cover every frozen criterion, preserve the supplied binding exactly."
		if len(p.planningInput.Review) > 0 {
			instructions = "Independently review this frozen execution plan against every criterion. Do not implement or edit files. Return exactly JSON {\"action\":\"APPROVE\" or \"REJECT\",\"feedback\":\"specific review findings; required on rejection\"}."
		}
		frozen, err := json.Marshal(p.planningInput)
		if err != nil {
			return "", ErrUnsupported
		}
		prompt += "\n\nPlanning task instructions:\n" + instructions + "\nFrozen planning inputs: " + string(frozen)
	}
	if len(p.ApprovedPlan) > 0 {
		if !json.Valid(p.ApprovedPlan) {
			return "", ErrUnsupported
		}
		prompt += "\n\nImplement the approved frozen execution plan exactly:\n" + string(p.ApprovedPlan)
	}
	return prompt, nil
}

// runPlanning retains only actual final model text. It observes the repository
// head and complete working tree before and after the READ_ONLY runtime; any
// mutation fails instead of committing or reporting a delivery Claim.
func validPlanning(p Plan) bool {
	return p.Planning != nil && p.AuthorizationMode == "READ_ONLY" && p.Handover == nil && p.Distillation == nil && len(p.Artifacts) == 0 && len(p.Planning.Binding) > 0 && len(p.Planning.Criteria) > 0 && p.Planning.DefaultBranch != "" && !strings.HasPrefix(p.Planning.DefaultBranch, "-")
}

func runPlanning(ctx context.Context, p Plan, emit func(Observation) error) (Result, error) {
	unknown := Result{ResultState: "UNKNOWN"}
	if !validPlanning(p) {
		return unknown, ErrUnsupported
	}

	head, e := workspaceGit(ctx, p.Workdir, "rev-parse", "--verify", "HEAD^{commit}")
	if e != nil {
		return unknown, e
	}
	branch, e := workspaceGit(ctx, p.Workdir, "rev-parse", "--verify", "refs/heads/"+p.Planning.DefaultBranch+"^{commit}")
	if e != nil || strings.TrimSpace(string(head)) != strings.TrimSpace(string(branch)) {
		return unknown, ErrUnsupported
	}
	before, e := workspaceGit(ctx, p.Workdir, "status", "--porcelain", "--untracked-files=all")
	if e != nil || len(before) != 0 {
		return unknown, ErrUnsupported
	}
	if p.BaseCommit != "" && p.BaseCommit != strings.TrimSpace(string(head)) {
		return unknown, ErrUnsupported
	}
	p.BaseCommit = strings.TrimSpace(string(head))
	p.planningInput = p.Planning
	p.Planning = nil
	var output string
	result, e := Run(ctx, p, func(o Observation) error {
		if text := modelOutput(p, o); strings.TrimSpace(text) != "" {
			output = text
		}
		if emit != nil {
			return emit(o)
		}
		return nil
	})
	if e != nil {
		return result, e
	}
	after, e := workspaceGit(ctx, p.Workdir, "status", "--porcelain", "--untracked-files=all")
	if e != nil || len(after) != 0 || result.HeadCommit != p.BaseCommit {
		return unknown, ErrRuntimeFailed
	}
	var document map[string]json.RawMessage
	if strings.TrimSpace(output) == "" || len(output) > 65536 || json.Unmarshal([]byte(output), &document) != nil || document == nil {
		return unknown, ErrRuntimeFailed
	}
	result.PlanningDocument = output
	result.Workspace = ""
	result.Diff = nil
	result.Artifacts = nil
	result.Progress = nil
	return result, nil
}
