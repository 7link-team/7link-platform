package governance

import "slices"

// DelegationConditions is evaluated against facts loaded by the business owner
// in the decision transaction. Unknown or absent facts never imply a pass.
type DelegationConditions struct {
	Actions       []string `json:"actions"`
	WorkItemTypes []string `json:"work_item_types"`
	MaxRisk       string   `json:"max_risk"`
	CostCapMicros int64    `json:"cost_cap_micros"`
	MaxRework     int      `json:"max_rework"`
}

// DelegationGuards are current project/escalation facts, not request JSON.
type DelegationGuards struct {
	Hold           bool
	ProjectAllowed bool
	Autonomous     bool
	ClaimEvaluated bool
}

func validDelegationConditions(c DelegationConditions) bool {
	if len(c.Actions) < 1 || len(c.Actions) > 5 || (c.MaxRisk != "LOW" && c.MaxRisk != "MEDIUM") || c.CostCapMicros <= 0 || c.MaxRework < 1 || c.MaxRework > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, action := range c.Actions {
		if seen[action] || !slices.Contains([]string{"decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert"}, action) || (action == "code_merge" && c.MaxRisk != "LOW") {
			return false
		}
		seen[action] = true
	}
	seen = map[string]bool{}
	for _, kind := range c.WorkItemTypes {
		if seen[kind] || !slices.Contains([]string{"FEATURE", "BUG", "CHORE", "DOC", "RESEARCH", "DESIGN", "TEST", "TASK", "RISK", "INCIDENT", "CONTENT"}, kind) {
			return false
		}
		seen[kind] = true
	}
	return true
}

func delegationConditionReason(c DelegationConditions, code string, f *WorkItemFacts, g DelegationGuards) string {
	if !validDelegationConditions(c) || !slices.Contains(c.Actions, code) || f == nil {
		return "NO_DELEGATION"
	}
	if code != "intake_convert" {
		if len(c.WorkItemTypes) > 0 && !slices.Contains(c.WorkItemTypes, f.Type) {
			return "DELEGATION_TYPE"
		}
		if f.Risk != "LOW" && (f.Risk != "MEDIUM" || c.MaxRisk != "MEDIUM") {
			return "DELEGATION_RISK"
		}
		if f.CostMicros < 0 || f.CostMicros > c.CostCapMicros {
			return "DELEGATION_COST"
		}
		if !f.RequiredVerificationsPassed {
			return "DELEGATION_VERIFICATION"
		}
		if f.QualityReworkCount < 0 || f.QualityReworkCount >= c.MaxRework {
			return "DELEGATION_REWORK"
		}
		if f.RequireHumanDecision {
			return "DELEGATION_HUMAN_REQUIRED"
		}
	}
	if g.Hold {
		return "ESCALATION_HOLD"
	}
	if !g.ProjectAllowed {
		return "PROJECT_GATE"
	}
	if code == "code_merge" && f.Risk != "LOW" {
		return "DELEGATION_MERGE_RISK"
	}
	if code == "intake_convert" && (!g.Autonomous || (f.IntakeConvertMode != "NEW_REQUIREMENT" && f.IntakeConvertMode != "MERGE")) {
		return "DELEGATION_MODE"
	}
	if code != "intake_convert" && !g.ClaimEvaluated {
		return "ESCALATION_PENDING"
	}
	return ""
}
