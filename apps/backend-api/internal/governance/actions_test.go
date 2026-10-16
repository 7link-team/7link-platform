package governance

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFrozenGovernanceActionConstraints(t *testing.T) {
	// These are the normative nondelegable additions through D120, including
	// the project_create pseudo-action that must never become an IAM grant.
	for _, code := range []string{"scope_change", "delivery_send", "project_mode_change", "policy_manage", "delegation_grant", "takeover", "machine_revoke", "production_access", "intake_decline", "memory_approve", "project_create", "spawn_grant_manage", "exception_decide", "acceptance_suite_manage", "integration_manage", "terminal_interact", "preview_share_approve", "machine_access_manage", "tool_catalog_manage"} {
		rules := RulesForAction(code, "AGENT")
		require.True(t, rules.NonDelegable, code)
		require.True(t, rules.HumanOnly, code)
		require.False(t, rules.Delegable, code)
		require.False(t, rules.PolicyOnly, code)
	}
	for _, code := range []string{"interaction_respond", "plan_confirm", "report_confirm", "andon_clear", "template_manage", "workspace_file_edit", "agent_workspace_read"} {
		require.Equal(t, ActionRules{HumanOnly: true}, RulesForAction(code, "SYSTEM"), code)
	}
	for _, code := range []string{"decision_approve", "decision_reject", "decision_request_changes", "code_merge", "intake_convert"} {
		require.Equal(t, ActionRules{Delegable: true}, RulesForAction(code, "AGENT"), code)
	}
	require.Equal(t, ActionRules{PolicyOnly: true}, RulesForAction("agent_spawn", "AGENT"))
	require.Equal(t, ActionRules{PolicyOnly: true}, RulesForAction("plan_approve", "AGENT"))
	require.Equal(t, ActionRules{}, RulesForAction("plan_approve", "HUMAN"))
	for _, code := range []string{"", "unknown", "andon_pull", "artifact_comment_write", "interaction_request"} {
		require.Equal(t, ActionRules{}, RulesForAction(code, "AGENT"), code)
	}
	snapshot := RulesForAction("project_create", "HUMAN")
	snapshot.HumanOnly = false
	require.True(t, RulesForAction("project_create", "HUMAN").HumanOnly)
	require.False(t, snapshot.HumanOnly)
}
