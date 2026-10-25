package governance

import (
	"strings"
	"testing"

	"7link.com/platform/pkg/extension/policyx"
	"github.com/stretchr/testify/require"
)

func TestManifestPrivilegedPermissionsStaySeparated(t *testing.T) {
	roles := map[string]roleManifestEntry{}
	for _, role := range defaultRoleManifest() {
		require.NotEmpty(t, role.Name)
		require.NotContains(t, roles, role.Key)
		roles[role.Key] = role
		seen := map[string]bool{}
		for _, code := range role.Codes {
			require.NotEmpty(t, code)
			require.NotContains(t, code, "*")
			require.False(t, seen[code], "duplicate %s in %s", code, role.Key)
			seen[code] = true
			require.NotEqual(t, "agent_spawn", code, "spawn is policy-only")
			if code == "production_access" {
				require.Equal(t, "production_operator", role.Key)
			}
		}
		if strings.HasPrefix(role.Key, "delegated_") {
			require.Equal(t, []string{strings.TrimPrefix(role.Key, "delegated_")}, role.Codes)
		}
	}
	require.Len(t, roles, 15, "spec08 roles plus the spec12 Human tester role")
	require.Equal(t, []string{"production_access"}, roles["production_operator"].Codes)
	for _, code := range []string{"machine_revoke", "gateway_manage", "policy_manage", "takeover", "project_mode_change", "delegation_grant", "spawn_grant_manage", "planning_settings_manage", "integration_manage", "machine_access_manage", "agent_workspace_read", "tool_catalog_manage"} {
		require.Contains(t, roles["team_owner"].Codes, code)
		require.NotContains(t, roles["delivery_lead"].Codes, code)
	}
	require.Contains(t, roles["team_owner"].Codes, "access_request_approve")
	require.Contains(t, roles["team_owner"].Codes, "project_takeover")
	require.NotContains(t, roles["delivery_lead"].Codes, "project_takeover")
	require.True(t, RulesForAction("project_takeover", "AGENT").HumanOnly)
	require.True(t, RulesForAction("project_takeover", "HUMAN").NonDelegable)
	require.NotContains(t, roles["delivery_lead"].Codes, "access_request_approve")
	require.NotContains(t, roles["engineer"].Codes, "decision_approve")
	require.NotContains(t, roles["engineer"].Codes, "decision_force_valid")
}

func TestManifestAgentsCannotAcquireHumanAuthority(t *testing.T) {
	for _, role := range defaultRoleManifest() {
		if !strings.HasPrefix(role.Key, "agent_") {
			continue
		}
		for _, code := range []string{"production_access", "agent_spawn", "decision_approve", "decision_reject", "decision_request_changes", "decision_force_valid", "code_merge", "scope_change", "delivery_send", "project_mode_change", "policy_manage", "delegation_grant", "takeover", "machine_revoke", "intake_decline", "memory_approve", "plan_confirm", "report_confirm", "plan_approve", "terminal_interact", "workspace_file_edit", "preview_share_approve", "machine_access_manage", "agent_workspace_read", "tool_catalog_manage", "interaction_respond", "work_item_assign"} {
			require.NotContains(t, role.Codes, code, role.Key)
		}
		for _, code := range []string{"andon_pull", "artifact_comment_write", "interaction_request", "goal_read", "plan_submit", "report_submit"} {
			require.Contains(t, role.Codes, code, role.Key)
		}
		if role.Key != "agent_executor" {
			require.NotContains(t, role.Codes, "execution_start")
			require.NotContains(t, role.Codes, "work_item_execute")
		}
		require.NotContains(t, role.Codes, "policy_read")
		require.NotContains(t, role.Codes, "audit_read")
	}
}

func TestManifestEngineerRiskConditionUsesCanonicalBusinessFact(t *testing.T) {
	var engineer roleManifestEntry
	for _, role := range defaultRoleManifest() {
		if role.Key == "engineer" {
			engineer = role
		}
	}
	require.Contains(t, engineer.Codes, "plan_approve")
	require.Contains(t, engineer.Codes, "policy_read")
	require.Contains(t, engineer.Codes, "audit_read")
	require.Contains(t, engineer.Codes, "workspace_file_edit")
	require.NotContains(t, engineer.Codes, "terminal_interact")
	require.NotContains(t, engineer.Codes, "preview_share_approve")
	condition := engineer.Conditions["plan_approve"]
	require.NotEmpty(t, condition)
	for _, risk := range []string{"LOW", "MEDIUM", "HIGH", "", "unknown"} {
		allowed, err := policyx.EvaluateCondition(condition, policyx.TrustedContext{Resource: policyx.ResourceContext{Attrs: map[string]string{"risk": risk}}})
		require.NoError(t, err)
		require.Equal(t, risk == "LOW" || risk == "MEDIUM", allowed, risk)
	}
}

func TestManifestReturnsIsolatedCopies(t *testing.T) {
	first := defaultRoleManifest()
	first[0].Codes[0] = "unexpected_permission"
	for _, entry := range first {
		for _, condition := range entry.Conditions {
			condition[0] = '!'
		}
	}
	for _, entry := range defaultRoleManifest() {
		require.NotContains(t, entry.Codes, "unexpected_permission")
		for _, condition := range entry.Conditions {
			_, err := policyx.CanonicalCondition(condition)
			require.NoError(t, err)
		}
	}
}
