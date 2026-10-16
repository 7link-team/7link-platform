package governance

// ActionRules describes governance constraints, not permission grants. The IAM
// registry must independently recognize a code before an action can be allowed.
// Values are returned by copy so callers cannot change the frozen rules.
type ActionRules struct {
	NonDelegable bool `json:"non_delegable"`
	Delegable    bool `json:"delegable"`
	HumanOnly    bool `json:"human_only"`
	PolicyOnly   bool `json:"policy_only"`
}

// RulesForAction is the single classification of spec08 §3.2 and D111/D112/
// D119/D120 additions. plan_approve is policy-only specifically for Agents.
func RulesForAction(code, actorKind string) ActionRules {
	var rules ActionRules
	switch code {
	case "scope_change", "delivery_send", "project_mode_change", "policy_manage",
		"delegation_grant", "takeover", "machine_revoke", "production_access",
		"intake_decline", "memory_approve", "project_create", "spawn_grant_manage",
		"exception_decide", "acceptance_suite_manage", "integration_manage",
		"terminal_interact", "preview_share_approve", "machine_access_manage", "tool_catalog_manage":
		rules.NonDelegable = true
		rules.HumanOnly = true
	case "decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert":
		rules.Delegable = true
	case "agent_spawn":
		rules.PolicyOnly = true
	case "plan_approve":
		rules.PolicyOnly = actorKind == "AGENT"
	case "interaction_respond", "plan_confirm", "report_confirm", "andon_clear",
		"template_manage", "workspace_file_edit", "agent_workspace_read":
		rules.HumanOnly = true
	}
	return rules
}
