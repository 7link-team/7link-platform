package governance

import (
	"encoding/json"
	"strings"
)

// RoleManifestVersion changes when the declared default grants change.
const RoleManifestVersion = 3

// defaultRoleManifest expands spec08 §3.3 and its D112/D119/D120 additions.
// Lists are explicit: registry growth alone never grants an unknown permission.
// Fresh slices/maps prevent a consumer from mutating subsequent seed runs.
// Display names are stable IAM seed names; UI localization uses the role key.
func defaultRoleManifest() []roleManifestEntry {
	return []roleManifestEntry{
		{Key: "tester", Name: "Tester", Codes: strings.Fields(`project_read requirement_read work_item_read work_item_write decision_approve decision_request_changes verification_run evidence_submit test_run_execute artifact_comment_write`)},
		{Key: "team_owner", Name: "Team owner", Codes: strings.Fields(`
   intake_read intake_triage intake_convert intake_decline intake_channel_manage intake_submit
   project_read project_manage project_stage_change project_mode_change requirement_read requirement_write
   work_item_read work_item_write work_item_assign work_item_execute work_item_cancel
   decision_approve decision_reject decision_request_changes decision_pause decision_force_valid scope_change
   delivery_read delivery_compose delivery_send decomposition_template_manage
   execution_read execution_start execution_stop interaction_respond evidence_submit verification_run code_merge verifier_manage
   machine_read machine_manage machine_revoke gateway_manage agent_read agent_manage agent_retire
   channel_read channel_post channel_manage
   goal_read goal_manage goal_member_manage plan_submit plan_confirm report_read report_submit report_confirm planning_settings_manage
   knowledge_read knowledge_write memory_approve knowledge_manage
   policy_read policy_manage delegation_grant takeover project_takeover audit_read spawn_grant_manage
   andon_pull andon_clear exception_decide plan_approve acceptance_suite_manage template_manage integration_manage
   terminal_interact workspace_file_edit preview_share_approve machine_access_manage
   agent_workspace_read artifact_comment_write interaction_request tool_catalog_manage
   workflow_manage review_initiate review_participate test_case_manage test_plan_manage test_run_execute worklog_submit worklog_approve
   audit_event_view audit_event_export access_request_view access_request_approve role_view
  `)},
		{Key: "delivery_lead", Name: "Delivery lead", Codes: strings.Fields(`
   intake_read intake_triage intake_convert intake_decline intake_channel_manage intake_submit
   project_read project_manage project_stage_change requirement_read requirement_write
   work_item_read work_item_write work_item_assign work_item_execute work_item_cancel
   decision_approve decision_reject decision_request_changes decision_pause decision_force_valid scope_change
   delivery_read delivery_compose delivery_send decomposition_template_manage
   execution_read execution_start execution_stop interaction_respond evidence_submit verification_run code_merge verifier_manage
   machine_read machine_manage agent_read agent_manage agent_retire
   channel_read channel_post channel_manage
   goal_read goal_manage goal_member_manage plan_submit plan_confirm report_read report_submit report_confirm
   knowledge_read knowledge_write memory_approve knowledge_manage policy_read audit_read
   andon_pull andon_clear exception_decide plan_approve acceptance_suite_manage template_manage
   terminal_interact workspace_file_edit preview_share_approve artifact_comment_write interaction_request
   workflow_manage review_initiate review_participate test_case_manage test_plan_manage test_run_execute worklog_submit worklog_approve
   audit_event_view audit_event_export access_request_view
  `)},
		{Key: "engineer", Name: "Engineer", Codes: strings.Fields(`
   intake_read project_read requirement_read work_item_read delivery_read execution_read machine_read agent_read
   channel_read knowledge_read policy_read audit_read goal_read report_read
   work_item_execute execution_start execution_stop interaction_respond evidence_submit verification_run
   decision_request_changes channel_post knowledge_write goal_member_manage plan_submit report_submit
   andon_pull plan_approve workspace_file_edit artifact_comment_write interaction_request
  `), Conditions: map[string]json.RawMessage{"plan_approve": json.RawMessage(`{"version":1,"in":[{"context":"resource.attr.risk"},{"value":["LOW","MEDIUM"]}]}`)}},
		{Key: "agent_executor", Name: "Agent executor", Codes: strings.Fields(`
   project_read requirement_read work_item_read delivery_read execution_read channel_read knowledge_read
   work_item_execute execution_start execution_stop evidence_submit verification_run channel_post
   goal_read plan_submit report_submit andon_pull artifact_comment_write interaction_request
  `)},
		{Key: "agent_reviewer", Name: "Agent reviewer", Codes: strings.Fields(`
   project_read requirement_read work_item_read delivery_read execution_read channel_read knowledge_read
   agent_read verification_run channel_post goal_read plan_submit report_submit andon_pull artifact_comment_write interaction_request
  `)},
		{Key: "agent_coordinator", Name: "Agent coordinator", Codes: strings.Fields(`
   intake_read intake_triage project_read requirement_read work_item_read delivery_read execution_read channel_read knowledge_read
   channel_post goal_read plan_submit report_submit andon_pull artifact_comment_write interaction_request
  `)},
		{Key: "agent_lead", Name: "Agent lead", Codes: strings.Fields(`
   project_read requirement_read work_item_read delivery_read execution_read channel_read knowledge_read
   agent_read channel_post goal_read plan_submit report_submit andon_pull artifact_comment_write interaction_request
  `)},
		{Key: "intake_api_client", Name: "Intake API client", Codes: []string{"intake_submit"}},
		{Key: "production_operator", Name: "Production operator", Codes: []string{"production_access"}},
		{Key: "delegated_decision_approve", Name: "Delegated decision approval", Codes: []string{"decision_approve"}},
		{Key: "delegated_decision_reject", Name: "Delegated decision rejection", Codes: []string{"decision_reject"}},
		{Key: "delegated_decision_request_changes", Name: "Delegated change request", Codes: []string{"decision_request_changes"}},
		{Key: "delegated_code_merge", Name: "Delegated code merge", Codes: []string{"code_merge"}},
		{Key: "delegated_intake_convert", Name: "Delegated intake conversion", Codes: []string{"intake_convert"}},
	}
}
