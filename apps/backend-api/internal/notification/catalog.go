package notification

import "time"

// These are the frozen defaults of the currently produced catalog. A saved
// global or per-type preference overrides email defaults; URGENT stays immediate.
func defaultEmail(typ string, severity Severity) string {
	switch typ {
	case "verifier.calibration_drifted":
		return "DAILY_DIGEST"
	case "provider.unavailable", "eval.below_target", "rubric.divergence_detected", "verifier.tool_unavailable":
		return "OFF"
	case "automation.run_failed", "automation.notice", "planning.report_needs_recheck":
		return "OFF"
	case executionAttentionType:
		return "IMMEDIATE"
	case acceptedUndeliveredType:
		return "OFF"
	case "machine.offline", "machine.recovered":
		return "IMMEDIATE"
	case "machine.enrolled", "machine.never_online":
		return "OFF"
	case "worklog.approval_pending", "project.stage_suggestion":
		return "OFF"
	case "dispatch.applied":
		return "DAILY_DIGEST"
	case dependencyCanceledType, "project.constitution_submitted", "preview.share_requested", "preview.share_decided", "project.tolerance_at_risk", "planning.goal_at_risk", "planning.report_checked_blocked":
		return "OFF"
	case "planning.loop_escalated", "planning.loop_paused":
		return "IMMEDIATE"
	case "governance.spawn_grant_expiring":
		return "DAILY_DIGEST"
	case "agent.spawn_requested":
		return "OFF"
	case "governance.spawn_grant_threshold":
		return "IMMEDIATE"
	case "review_sheet.opened":
		return "DAILY_DIGEST"
	case "review_sheet.rejected", "review_sheet.due":
		return "OFF"
	case "project.joined", "work_item.lease_expired":
		return "OFF"
	case "interaction.expiring":
		return "OFF"
	case "project.blocked_long", "work_item.review_overdue", "intake.triage_overdue", "governance.escalation_reminder":
		return "IMMEDIATE"
	case "verification.human_requested":
		return "OFF"
	case "planning.report_returned":
		return "IMMEDIATE"
	case "intake.client_silent":
		return "IMMEDIATE"
	case "intake.received":
		return "DAILY_DIGEST"
	case "delivery.receipt_received", "delivery.client_rejected", "dispatch.confirmation_required":
		return "OFF"
	case "gateway.key_rotated":
		return "OFF"
	case "planning.plan_submitted", "planning.report_submitted", "planning.report_blocked":
		return "IMMEDIATE"
	case "planning.report_generated", "planning.plan_confirmed", "planning.plan_returned", "planning.report_confirmed":
		return "OFF"
	case "exception.raised", "plan.proposed", "work_item.escalated":
		return "IMMEDIATE"
	case "plan.approved", "plan.rejected":
		return "OFF"
	case "governance.delegation_expiring":
		return "IMMEDIATE"
	case "project.autonomy_lost", "project.autonomy_restored":
		return "IMMEDIATE"
	case "work_item.assigned", "requirement.versioned", "memory.rule_pending", "governance.quality_escape", "governance.delegation_granted":
		return "DAILY_DIGEST"
	case "machine.revoked", "machine.rotation_failed", "execution.failed_exhausted", "execution.interrupted", "execution.cost_paused", "governance.escalation_triggered", "budget.threshold", "fleet.agent_error_persisting", "jobs.dead_lettered", "events.parked":
		return "IMMEDIATE"
	case "work_item.review_ready", "work_item.rework", "work_item.accepted", "project.stage_changed", "channel.mentioned", "governance.delegation_ended", "*":
		return "OFF"
	}
	if severity == Info {
		return "OFF"
	}
	return "IMMEDIATE"
}

func mergeWindow(typ string) time.Duration {
	switch typ {
	case "verifier.calibration_drifted", "provider.unavailable", "eval.below_target", "rubric.divergence_detected", "verifier.tool_unavailable":
		return 0
	case "automation.run_failed":
		return time.Hour
	case executionAttentionType:
		return 0
	case acceptedUndeliveredType:
		return 0
	case "andon.pulled", "project.andon_pulled", "project.mode_changed", "project.taken_over", "project.autonomy_lost", "project.autonomy_restored", "delivery.confirmation_requested":
		return 0
	case "machine.enrolled":
		return time.Hour
	case "machine.never_online":
		return 0
	case "worklog.approval_pending", "project.stage_suggestion":
		return 0
	case "dispatch.applied":
		return 15 * time.Minute
	case dependencyCanceledType:
		return 0
	case "project.constitution_submitted", "preview.share_requested", "preview.share_decided", "project.tolerance_at_risk", "planning.goal_at_risk", "planning.report_checked_blocked":
		return 0
	case "planning.loop_escalated":
		return time.Hour
	case "planning.loop_paused":
		return 0
	case "agent.spawn_requested":
		return 10 * time.Minute
	case "governance.spawn_grant_expiring", "governance.spawn_grant_threshold":
		return 0
	case "review_sheet.opened", "review_sheet.rejected", "review_sheet.due":
		return 0
	case "project.joined", "work_item.lease_expired":
		return 0
	case "project.blocked_long", "work_item.review_overdue", "intake.triage_overdue", "interaction.expiring", "governance.escalation_reminder":
		return 0
	case "verification.human_requested":
		return 0
	case "planning.plan_unconfirmed", "planning.goal_close_overdue", "template.version_published", "memory.reviewed", "knowledge.source_failed", "intake.triaged":
		return 0
	case "interaction.requested":
		return time.Minute
	case "intake.clarification_answered":
		return 10 * time.Minute
	case "intake.client_silent":
		return 0
	case "intake.received":
		return 5 * time.Minute
	case "dispatch.confirmation_required":
		return 10 * time.Minute
	case "delivery.receipt_received", "delivery.client_rejected":
		return 0
	case "gateway.key_rotated":
		return 0
	case "planning.plan_submitted", "planning.plan_confirmed", "planning.plan_returned", "planning.report_submitted", "planning.report_confirmed", "planning.report_returned", "planning.report_blocked":
		return 0
	case "exception.raised", "plan.proposed", "plan.approved", "plan.rejected", "work_item.escalated":
		return 0
	case "governance.delegation_expiring":
		return 0
	case "machine.offline":
		return 5 * time.Minute
	case "machine.recovered":
		return 0
	case "memory.rule_pending", "execution.cost_paused":
		return 30 * time.Minute
	case "channel.mentioned":
		return 2 * time.Minute
	case "work_item.review_ready":
		return 5 * time.Minute
	case "machine.revoked", "work_item.assigned", "requirement.versioned", "budget.threshold", "governance.escalation_triggered", "governance.delegation_ended", "governance.quality_escape", "governance.delegation_granted":
		return 0
	default:
		return 10 * time.Minute
	}
}
