package notification

import (
	"context"
	"encoding/json"
	"errors"

	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"gorm.io/gorm"
)

// sourceAudienceOn narrows read permission to the frozen business audience.
// The same check runs during projection, inbox reads, and SMTP delivery.
func (m *Module) sourceAudienceOn(ctx context.Context, tx *gorm.DB, row *model.Notification, principal guid.ID) (bool, error) {
	if row.Type == "delivery.acceptance_reminder" || row.Type == "delivery.acceptance_overdue" {
		return m.acceptanceAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "delivery.confirmation_requested" {
		return m.deliveryConfirmationAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "project.mode_changed" || row.Type == "project.taken_over" || row.Type == "project.andon_pulled" || row.Type == "project.autonomy_lost" || row.Type == "project.autonomy_restored" {
		return m.projectControlAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "ensemble.approval_requested" || row.Type == "ensemble.selection_requested" {
		return m.ensembleAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "planning.report_overdue" {
		return m.reportOverdueAudienceOn(ctx, tx, row, principal)
	}
	switch row.Type {
	case "verifier.calibration_drifted", "provider.unavailable", "eval.below_target", "rubric.divergence_detected", "verifier.tool_unavailable":
		return m.templateLifecycleAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == executionAttentionType {
		return m.attentionAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "automation.run_failed" {
		return m.automationAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == acceptedUndeliveredType {
		return m.acceptedAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "planning.report_reminder" {
		return m.reportReminderAudienceOn(ctx, tx, row, principal)
	}
	if (row.Type == "machine.offline" && row.Permission == "") || row.Type == "machine.recovered" {
		return m.machineAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "andon.pulled" {
		return m.andonAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "machine.enrolled" && row.ID > 0 {
		return m.sourceGroupAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "machine.enrolled" || row.Type == "machine.never_online" {
		return m.enrollmentAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "project.stage_suggestion" {
		return m.stageAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == dependencyCanceledType {
		return m.dependencyAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "planning.report_checked_blocked" {
		return m.reportCheckedAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "planning.loop_escalated" || row.Type == "planning.loop_paused" {
		if m.planningFacts.LoopEligibleOn == nil {
			return false, ErrUnavailable
		}
		var p map[string]string
		if json.Unmarshal([]byte(row.Params), &p) != nil {
			return false, ErrInvalid
		}
		id, err := guid.RequireID(p["run_id"])
		if err != nil {
			return false, ErrInvalid
		}
		return m.planningFacts.LoopEligibleOn(ctx, tx, row.TenantID, id, principal, row.Type)
	}
	if row.Type == "governance.spawn_grant_threshold" || row.Type == "governance.spawn_grant_expiring" || row.Type == "agent.spawn_requested" {
		return m.spawnAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "review_sheet.opened" || row.Type == "review_sheet.rejected" || row.Type == "review_sheet.due" {
		return m.reviewSheetAudienceOn(ctx, tx, row, principal)
	}
	if row.Type == "execution.cost_paused" {
		if m.executionFacts == nil {
			return false, ErrUnavailable
		}
		var params map[string]string
		if json.Unmarshal([]byte(row.Params), &params) != nil {
			return false, ErrInvalid
		}
		id, err := guid.RequireID(params["subject_id"])
		if err != nil {
			return false, ErrInvalid
		}
		fact, err := m.executionFacts(ctx, tx, row.TenantID, id)
		if errors.Is(err, execution.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if fact.ProjectID != row.ProjectID || (fact.State != "COST_PAUSED" && !(fact.State == "PAUSED" && (fact.StateReason == "COST_CAP" || fact.StateReason == "BUDGET_PAUSE"))) {
			return false, nil
		}
	}
	if row.ID > 0 && (row.Type == "interaction.requested" || row.Type == "intake.clarification_answered" || row.Type == "dispatch.applied") {
		return m.sourceGroupAudienceOn(ctx, tx, row, principal)
	}
	subject := func() (guid.ID, error) {
		var p map[string]string
		if err := json.Unmarshal([]byte(row.Params), &p); err != nil {
			return 0, err
		}
		id, err := guid.RequireID(p["subject_id"])
		if err != nil {
			return 0, ErrInvalid
		}
		return id, nil
	}
	switch row.Type {
	case "project.constitution_submitted":
		return m.decisionAudienceOn(ctx, tx, row, principal)
	case "work_item.lease_expired":
		if m.decisionFacts.LeaseExpiredOn == nil {
			return false, ErrUnavailable
		}
		var params map[string]string
		if json.Unmarshal([]byte(row.Params), &params) != nil {
			return false, ErrInvalid
		}
		assignment, err := guid.RequireID(params["assignment_id"])
		if err != nil {
			return false, ErrInvalid
		}
		fact, found, err := m.decisionFacts.LeaseExpiredOn(ctx, tx, row.TenantID, assignment)
		if err != nil || !found || fact.ProjectID != row.ProjectID || fact.WorkItemID.String() != params["subject_id"] {
			return false, err
		}
		if fact.AssigneeID == principal {
			return true, nil
		}
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	case "work_item.review_overdue", "project.blocked_long":
		return m.deadlineAudienceOn(ctx, tx, row, principal)
	case "intake.triage_overdue", "interaction.expiring", "governance.escalation_reminder":
		return m.sourceReminderAudienceOn(ctx, tx, row, principal)
	case "verification.human_requested":
		if m.decisionFacts.HumanVerificationOn == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		_, project, yes, err := m.decisionFacts.HumanVerificationOn(ctx, tx, row.TenantID, id, principal)
		return yes && project == row.ProjectID, err
	case "intake.clarification_answered", "intake.client_silent":
		return m.clarificationAudienceOn(ctx, tx, row, principal)
	case "memory.reviewed", "knowledge.source_failed":
		return m.knowledgeAudienceOn(ctx, tx, row, principal)
	case "template.version_published":
		return m.templatePublicationAudienceOn(ctx, tx, row, principal)
	case "interaction.requested":
		return m.interactionAudienceOn(ctx, tx, row, principal)
	case "intake.received", "intake.triaged":
		return m.intakeAudienceOn(ctx, tx, row, principal)
	case "preview.share_requested", "preview.share_decided", "project.tolerance_at_risk", "delivery.receipt_received", "delivery.client_rejected", "dispatch.confirmation_required", "dispatch.applied":
		return m.collaborationAudienceOn(ctx, tx, row, principal)
	case "gateway.key_rotated":
		if row.ProjectID != 0 {
			return false, nil
		}
		if m.gatewayRotations == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		return m.gatewayRotations(ctx, tx, row.TenantID, id)
	case "planning.report_needs_recheck", "planning.goal_at_risk", "planning.plan_unconfirmed", "planning.goal_close_overdue", "planning.report_generated", "planning.plan_submitted", "planning.plan_confirmed", "planning.plan_returned", "planning.report_submitted", "planning.report_confirmed", "planning.report_returned", "planning.report_blocked":
		return m.planningAudienceOn(ctx, tx, row, principal)
	case "exception.raised":
		return m.exceptionAudienceOn(ctx, tx, row, principal)
	case "plan.proposed", "plan.approved", "plan.rejected", "work_item.escalated":
		return m.decisionAudienceOn(ctx, tx, row, principal)
	case "governance.delegation_expiring":
		if m.delegationFacts == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		fact, exists, err := m.delegationFacts(ctx, tx, row.TenantID, id)
		if err != nil || !exists || fact.ProjectID != row.ProjectID || fact.State != "ACTIVE" || !fact.EndsAt.After(m.now()) {
			return false, err
		}
		if fact.GrantorID == principal {
			return true, nil
		}
		return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
	case "governance.delegation_granted":
		if m.delegationFacts == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		fact, exists, err := m.delegationFacts(ctx, tx, row.TenantID, id)
		if err != nil || !exists || fact.ProjectID != row.ProjectID || fact.GrantorID == principal {
			return false, err
		}
		return m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
	case "governance.delegation_ended":
		if m.delegationFacts == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		fact, exists, err := m.delegationFacts(ctx, tx, row.TenantID, id)
		return exists && fact.ProjectID == row.ProjectID && fact.GrantorID == principal && fact.EndReason != "" && fact.EndReason != "MANUAL" && (fact.State == "REVOKED" || fact.State == "EXPIRED"), err
	case "memory.rule_pending":
		if m.ruleEligible == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		return m.ruleEligible(ctx, tx, row.TenantID, id, principal)
	case "work_item.review_ready":
		if m.workFacts.ReviewEligibleOn == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		return m.workFacts.ReviewEligibleOn(ctx, tx, row.TenantID, id, principal)
	case "work_item.rework", "work_item.accepted":
		if m.workFacts.FactsOn == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		facts, exists, err := m.workFacts.FactsOn(ctx, tx, row.TenantID, id)
		if err != nil || !exists {
			return false, err
		}
		if facts.ExecutorID == principal {
			return true, nil
		}
		if row.Type == "work_item.accepted" {
			return facts.ExecutorID == 0 && facts.AssignerID == principal, nil
		}
		return m.leadOn(ctx, tx, row.TenantID, facts.ProjectID, principal)
	case "requirement.versioned":
		lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
		if err != nil || lead {
			return lead, err
		}
		if m.workFacts.RequirementExecutorOn == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		return m.workFacts.RequirementExecutorOn(ctx, tx, row.TenantID, id, principal)
	case "execution.interrupted", "execution.failed_exhausted":
		if m.executionFacts == nil || m.workFacts.FactsOn == nil || m.workFacts.ProjectModeOn == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		fact, err := m.executionFacts(ctx, tx, row.TenantID, id)
		if errors.Is(err, execution.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if row.Type == "execution.interrupted" && fact.InitiatorID == principal {
			return true, nil
		}
		work, exists, err := m.workFacts.FactsOn(ctx, tx, row.TenantID, fact.WorkItemID)
		if err != nil {
			return false, err
		}
		if exists && work.ExecutorID == principal {
			return true, nil
		}
		if row.Type == "execution.interrupted" {
			mode, err := m.workFacts.ProjectModeOn(ctx, tx, row.TenantID, fact.ProjectID)
			if err != nil {
				return false, err
			}
			if mode != "AUTONOMOUS" {
				return false, nil
			}
		}
		return m.leadOn(ctx, tx, row.TenantID, fact.ProjectID, principal)
	case "governance.escalation_triggered":
		if m.incidentFacts == nil {
			return false, ErrUnavailable
		}
		id, err := subject()
		if err != nil {
			return false, err
		}
		fact, found, err := m.incidentFacts(ctx, tx, row.TenantID, id)
		if err != nil || !found || fact.ProjectID != row.ProjectID || fact.State != "OPEN" {
			return false, err
		}
		owner, err := m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
		if err != nil || owner {
			return owner, err
		}
		return m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
	case "governance.quality_escape", "budget.threshold", "execution.cost_paused":
		owner, err := m.projectPermissionOn(ctx, tx, row.TenantID, row.ProjectID, principal, "project_takeover")
		if err != nil || owner {
			return owner, err
		}
		lead, err := m.leadOn(ctx, tx, row.TenantID, row.ProjectID, principal)
		if err != nil || lead {
			return lead, err
		}
		if row.Type == "budget.threshold" {
			var params map[string]string
			if err := json.Unmarshal([]byte(row.Params), &params); err != nil {
				return false, err
			}
			if params["threshold_pct"] == "100" {
				if m.workFacts.ActiveEngineerOn == nil {
					return false, ErrUnavailable
				}
				return m.workFacts.ActiveEngineerOn(ctx, tx, row.TenantID, row.ProjectID, principal)
			}
		}
		return false, nil
	case "project.stage_changed", "project.joined":
		if m.explain == nil || row.ProjectID <= 0 {
			return false, ErrUnavailable
		}
		explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: row.TenantID, EntityID: row.ProjectID, PrincipalID: principal, Permission: "project_read"})
		if err != nil || !explanation.Allowed {
			return false, err
		}
		for _, match := range explanation.Matches {
			if match.Effect == "allow" && match.SourceType == "entity_binding" && match.ScopeID == row.ProjectID.String() {
				return true, nil
			}
		}
		return false, nil
	}
	return true, nil
}

func (m *Module) leadOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	if m.roles.RoleIDOn == nil || m.explain == nil || project <= 0 {
		return false, ErrUnavailable
	}
	role, err := m.roles.RoleIDOn(ctx, tx, tenant, "delivery_lead")
	if errors.Is(err, governance.ErrRoleNotSeeded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	explanation, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: "project_read"})
	if err != nil || !explanation.Allowed {
		return false, err
	}
	for _, match := range explanation.Matches {
		if match.Effect == "allow" && match.RoleID == role.String() {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) projectPermissionOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID, permission string) (bool, error) {
	if m.explain == nil {
		return false, ErrUnavailable
	}
	e, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: principal, Permission: permission})
	return e.Allowed, err
}
