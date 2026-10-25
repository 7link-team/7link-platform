package notification

import (
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"slices"
	"strconv"
	"strings"
	"time"
)

type route struct {
	typ, permission string
	severity        Severity
	holders         bool
	recipientKey    string
}

// Only declared notification conditions are routed. Raw outbox data, names and
// exception text are never copied into notification parameters.
func eventRoute(e events.RecordedEvent, d map[string]json.RawMessage) (route, bool) {
	if r, ok := decisionRoute(e, d); ok {
		return r, true
	}
	text := func(k string) string { var s string; _ = json.Unmarshal(d[k], &s); return s }
	switch e.Type {
	case "gateway.key_rotated":
		if e.ProjectID == 0 && e.SubjectKind == "gateway_key" && e.SubjectID > 0 && rawID(d["new_key_id"]) == e.SubjectID {
			return route{"gateway.key_rotated", "gateway_manage", Info, true, ""}, true
		}
	case "governance.incident_changed":
		if text("state") == "OPEN" {
			return route{"governance.escalation_triggered", "project_read", Action, true, ""}, true
		}
	case "clientwork.exception_raised":
		return route{"exception.raised", "project_read", Action, true, ""}, true
	case "delegation.granted":
		return route{"governance.delegation_granted", "project_read", Info, true, ""}, true
	case "collab.message_posted":
		return route{"channel.mentioned", "channel_read", Info, false, ""}, true
	case "job.dead_lettered":
		return route{"jobs.dead_lettered", "system_job_manage", Action, true, ""}, true
	case "events.subscriber_failed":
		return route{"events.parked", "system_event_manage", Action, true, ""}, true
	case "machine.credential_changed":
		if text("state") == "FAILED" || text("reason_code") == "ROTATION_GRACE_EXPIRING" {
			return route{"machine.rotation_failed", "machine_manage", Action, true, "requested_by"}, true
		}
	case "machine.state_changed":
		switch text("state") {
		case "OFFLINE":
			return route{"machine.offline", "machine_manage", Action, true, ""}, true
		case "REVOKED":
			return route{"machine.revoked", "project_takeover", Action, true, ""}, true
		}
	case "work_item.assigned":
		return route{"work_item.assigned", "work_item_read", Info, false, "principal_id"}, true
	case "work_item.state_changed":
		if text("reason_code") == "LEASE_EXPIRED" && rawID(d["assignment_id"]) > 0 {
			return route{"work_item.lease_expired", "work_item_read", Info, true, ""}, true
		}
		switch text("to") {
		case "NEEDS_HUMAN_REVIEW":
			return route{"work_item.review_ready", "decision_approve", Action, true, ""}, true
		case "REWORK":
			return route{"work_item.rework", "work_item_read", Info, true, ""}, true
		case "ACCEPTED":
			return route{"work_item.accepted", "work_item_read", Info, true, ""}, true
		}
	case "execution.state_changed":
		if text("state") == "PAUSED" && (text("reason") == "COST_CAP" || text("reason") == "BUDGET_PAUSE" || text("state_reason") == "COST_CAP" || text("state_reason") == "BUDGET_PAUSE") {
			return route{"execution.cost_paused", "project_read", Action, true, ""}, true
		}
		switch text("state") {
		case "INTERRUPTED":
			return route{"execution.interrupted", "execution_read", Action, true, ""}, true
		case "COST_PAUSED":
			return route{"execution.cost_paused", "project_read", Action, true, ""}, true
		}
	case "project.stage_changed":
		return route{"project.stage_changed", "project_read", Info, true, ""}, true
	case "project.created":
		return route{"project.joined", "project_read", Info, false, ""}, true
	case "quality.escape_recorded":
		return route{"governance.quality_escape", "project_read", Info, true, ""}, true
	case "requirement.versioned":
		return route{"requirement.versioned", "requirement_read", Info, true, ""}, true
	case "knowledge.entry_written":
		if text("state") == "PENDING_REVIEW" {
			return route{"memory.rule_pending", "memory_approve", Info, true, ""}, true
		}
	case "budget.threshold_reached":
		severity := Action
		if rawID(d["threshold_pct"]) >= 100 {
			severity = Urgent
		}
		return route{"budget.threshold", "project_read", severity, true, ""}, true
	}
	return route{}, false
}
func rawID(v json.RawMessage) guid.ID {
	var s string
	if json.Unmarshal(v, &s) == nil {
		n, _ := strconv.ParseInt(s, 10, 64)
		return guid.ID(n)
	}
	var n int64
	_ = json.Unmarshal(v, &n)
	return guid.ID(n)
}

// subscriber uses the shared transactional cursor/receipt dispatcher. Hints
// are registered on the same attempt and emitted only after its commit.
func (m *Module) subscriber() events.Subscriber {
	return events.Subscriber{Name: Name + ".projector", EventTypes: []string{"project.autonomy_lost", "project.autonomy_restored", "delivery.confirmation_requested", "delivery.withdrawn", "project.mode_changed", "project.taken_over", "project.andon_pulled", "project.andon_cleared", "ensemble.changed", "andon.pulled", "andon.cleared", "automation.run_failed", "project.stage_ready", "work_item.created", "work_item.ready_checked", "work_item.dependency_changed", "verification.completed", "project.constitution_activated", "machine.enrollment_changed", "project.constitution_submitted", "clientwork.preview_share_requested", "clientwork.preview_share_decided", "clientwork.tolerance_forecasted", "clientwork.tolerance_changed", "planning.report_checked", "review_sheet.opened", "review_sheet.closed", "project.created", "clientwork.clarification_answered", "clientwork.clarification_expired", "intake.triaged", "knowledge.memory_reviewed", "knowledge.source_failed", "template.published", "governance.incident_changed", "intake.received", "clientwork.package_decided", "clientwork.dispatch_proposed", "clientwork.dispatch_decided", "gateway.key_rotated", "planning.changed", "planning.report_generated", "clientwork.exception_raised", "plan.proposed", "plan.approved", "plan.rejected", "work_item.escalated", "quality.escape_recorded", "delegation.granted", "delegation.revoked", "delegation.expired", "machine.credential_changed", "machine.state_changed", "work_item.assigned", "work_item.state_changed", "execution.state_changed", "project.stage_changed", "requirement.versioned", "agents.changed", "knowledge.entry_written", "budget.threshold_reached", "job.dead_lettered", "collab.message_posted"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: func(ctx context.Context, tx *gorm.DB, envelope events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		e := events.RecordedEvent{ID: envelope.EventID, TenantID: envelope.TenantID, ProjectID: envelope.ProjectID, SubjectID: envelope.Subject.ID, ActorID: envelope.Actor.ID, Type: envelope.EventType, SubjectKind: envelope.Subject.Kind, Data: envelope.Data}
		if err := m.projectEventOn(eventCtx, tx, e); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}}
}
func (m *Module) projectEventOn(ctx context.Context, tx *gorm.DB, e events.RecordedEvent) error {
	if e.Type == "delivery.confirmation_requested" || e.Type == "delivery.withdrawn" {
		return m.projectDeliveryConfirmationOn(ctx, tx, e)
	}
	if e.Type == "project.mode_changed" || e.Type == "project.taken_over" || e.Type == "project.andon_pulled" || e.Type == "project.andon_cleared" || e.Type == "project.autonomy_lost" || e.Type == "project.autonomy_restored" {
		return m.projectControlEventOn(ctx, tx, e)
	}
	if e.Type == "ensemble.changed" {
		return m.projectEnsembleOn(ctx, tx, e)
	}
	if e.Type == "automation.run_failed" {
		return m.projectAutomationFailureOn(ctx, tx, e)
	}
	if e.Type == "machine.enrollment_changed" {
		return m.projectEnrollmentOn(ctx, tx, e)
	}
	if !validTransaction(tx) {
		return ErrInvalid
	}
	if slices.Contains(stageHealthEventTypes, e.Type) {
		if err := m.stageHealthEventOn(ctx, tx, e); err != nil {
			return err
		}
	}
	if e.Type == "project.stage_ready" {
		var data struct {
			Target string `json:"target"`
		}
		if json.Unmarshal(e.Data, &data) != nil || data.Target == "" || e.ProjectID != e.SubjectID {
			return ErrInvalid
		}
		if m.decisionFacts.StageSuggestionOn == nil {
			return ErrUnavailable
		}
		target, err := m.decisionFacts.StageSuggestionOn(ctx, tx, e.TenantID, e.ProjectID)
		if err != nil || target != data.Target {
			return err
		}
		return m.notifyStageSuggestionOn(ctx, tx, e.TenantID, e.ProjectID)
	}
	if e.Type == "project.constitution_submitted" {
		return m.projectConstitutionOn(ctx, tx, e)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return err
	}
	if e.TenantID < 0 {
		return nil
	}
	if e.Type == "machine.state_changed" {
		var state string
		_ = json.Unmarshal(data["state"], &state)
		if state == "OFFLINE" || state == "ONLINE" || state == "DRAINING" {
			return m.projectMachineOn(ctx, tx, e, state)
		}
	}
	if e.Type == "andon.pulled" {
		return m.projectAndonOn(ctx, tx, e)
	}
	if e.Type == "andon.cleared" {
		return nil
	} // Current-source checks suppress the closed episode.
	if e.Type == "work_item.state_changed" {
		var reason string
		_ = json.Unmarshal(data["reason_code"], &reason)
		if reason == "ANDON_PULLED" {
			return m.projectAndonOn(ctx, tx, e)
		}
	}
	if e.Type == "planning.report_checked" {
		return m.projectReportCheckedOn(ctx, tx, e, data)
	}
	if e.Type == "review_sheet.opened" || e.Type == "review_sheet.closed" {
		return m.projectReviewSheetOn(ctx, tx, e)
	}
	if e.Type == "clientwork.clarification_answered" || e.Type == "clientwork.clarification_expired" {
		return m.projectClarificationOn(ctx, tx, e)
	}
	if e.Type == "knowledge.memory_reviewed" || e.Type == "knowledge.source_failed" {
		return m.projectKnowledgeEventOn(ctx, tx, e, data)
	}
	if e.Type == "template.published" {
		return m.projectTemplatePublicationOn(ctx, tx, e)
	}
	if m.decisionFacts.AttentionOn != nil {
		if e.Type == "execution.state_changed" {
			var state string
			_ = json.Unmarshal(data["state"], &state)
			if state == "INTERRUPTED" {
				return m.projectAttentionOn(ctx, tx, e, "INTERRUPTED")
			}
		}
		if e.Type == "work_item.state_changed" {
			var state string
			_ = json.Unmarshal(data["to"], &state)
			if state == "EXECUTION_FAILED" {
				return m.projectAttentionOn(ctx, tx, e, "EXECUTION_FAILED")
			}
		}
	}
	if e.Type == "execution.state_changed" && rawID(data["approval_id"]) > 0 {
		return m.projectInteractionOn(ctx, tx, e, data)
	}
	if e.Type == "intake.received" {
		return m.projectIntakeEventOn(ctx, tx, e)
	}
	if e.Type == "intake.triaged" {
		return m.projectIntakeTriagedOn(ctx, tx, e, data)
	}
	if e.Type == "planning.changed" || e.Type == "planning.report_generated" {
		return m.projectPlanningEventOn(ctx, tx, e, data)
	}
	if e.Type == "clientwork.preview_share_requested" || e.Type == "clientwork.preview_share_decided" || e.Type == "clientwork.tolerance_forecasted" || e.Type == "clientwork.tolerance_changed" || e.Type == "clientwork.package_decided" || e.Type == "clientwork.dispatch_proposed" || e.Type == "clientwork.dispatch_decided" {
		return m.projectCollaborationEventOn(ctx, tx, e)
	}
	if e.Type == "delegation.revoked" || e.Type == "delegation.expired" {
		return m.projectDelegationEndOn(ctx, tx, e, data)
	}
	if e.Type == "work_item.state_changed" && m.decisionFacts.CancelledDependentsOn != nil {
		if err := m.projectCancelledDependentsOn(ctx, tx, e); err != nil {
			return err
		}
	}
	if m.decisionFacts.DeadlineOn != nil && (e.Type == "work_item.state_changed" || e.Type == "project.stage_changed") {
		kind := "review_age"
		if e.Type == "project.stage_changed" {
			kind = "project_blocked"
		}
		if err := m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: kind, SourceID: e.SubjectID, Ordinal: 1}); err != nil {
			return err
		}
	}
	if e.Type == "governance.incident_changed" && m.incidentFacts != nil {
		if err := m.scheduleSourceReminderOn(ctx, tx, e.TenantID, sourceReminder{Kind: "incident", SourceID: e.SubjectID, Ordinal: 1}); err != nil {
			return err
		}
	}
	if e.Type == "delegation.granted" {
		if err := m.scheduleDelegationWarningOn(ctx, tx, e); err != nil {
			return err
		}
	}
	if e.Type == "machine.credential_changed" {
		var state, reason string
		_ = json.Unmarshal(data["state"], &state)
		_ = json.Unmarshal(data["reason_code"], &reason)
		if state == "PENDING_DELIVERY" && reason != "ROTATION_GRACE_EXPIRING" {
			return m.scheduleRotationWarningOn(ctx, tx, e, data)
		}
		if state == "FAILED" || reason == "ROTATION_GRACE_EXPIRING" {
			condition := "FAILED"
			if reason == "ROTATION_GRACE_EXPIRING" {
				condition = "GRACE"
			}
			claimed, err := m.claimRotationAlertOn(ctx, tx, e.TenantID, rawID(data["rotation_id"]), condition)
			if err != nil || !claimed {
				return err
			}
		}
	}
	rule, ok := eventRoute(e, data)
	if !ok || e.TenantID < 0 {
		return nil
	}
	var interruptedMachine guid.ID
	if rule.typ == "execution.interrupted" {
		if m.executionFacts == nil {
			return ErrUnavailable
		}
		fact, err := m.executionFacts(ctx, tx, e.TenantID, e.SubjectID)
		if errors.Is(err, execution.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		interruptedMachine = fact.MachineID
		if interruptedMachine <= 0 {
			return ErrInvalid
		}
	}
	if e.Type == "job.dead_lettered" || e.Type == "events.subscriber_failed" {
		e.TenantID = 0
		e.ProjectID = 0
	}
	recipients := []guid.ID{}
	if e.Type == "project.created" {
		if err := json.Unmarshal(data["member_ids"], &recipients); err != nil {
			return ErrInvalid
		}
	}
	if e.Type == "collab.message_posted" {
		if m.mentions.RecipientsOn == nil {
			return ErrUnavailable
		}
		var err error
		recipients, err = m.mentions.RecipientsOn(ctx, tx, e.TenantID, e.SubjectID)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}
	}
	if e.Type == "machine.credential_changed" && m.rotationRequester != nil {
		requester, err := m.rotationRequester(ctx, tx, e.TenantID, e.SubjectID, rawID(data["rotation_id"]))
		if err != nil {
			return err
		}
		if requester > 0 {
			recipients = append(recipients, requester)
		}
	}
	if id := rawID(data[rule.recipientKey]); id > 0 {
		recipients = append(recipients, id)
	}
	if e.Type == "work_item.assigned" {
		var assignee map[string]json.RawMessage
		if json.Unmarshal(data["assignee"], &assignee) == nil {
			if id := rawID(assignee["principal_id"]); id > 0 {
				recipients = append(recipients, id)
			}
		}
		if len(recipients) == 0 || rawID(data["assignment_id"]) <= 0 {
			return ErrInvalid
		}
	}
	if rule.holders {
		var cursor guid.ID
		for {
			ids, err := m.candidates(ctx, tx, cursor, 100)
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				break
			}
			for _, id := range ids {
				cursor = id
				var reasonCode string
				_ = json.Unmarshal(data["reason_code"], &reasonCode)
				params, _ := json.Marshal(map[string]string{"subject_id": e.SubjectID.String(), "threshold_pct": rawID(data["threshold_pct"]).String(), "assignment_id": rawID(data["assignment_id"]).String(), "reason_code": reasonCode, "source_version": rawID(data["source_version"]).String()})
				r := &model.Notification{Type: rule.typ, Params: string(params), TenantID: e.TenantID, ProjectID: e.ProjectID, RecipientPrincipalID: id, Permission: rule.permission}
				yes, err := m.visibleOn(ctx, tx, r, id)
				if err != nil {
					return err
				}
				if yes {
					recipients = append(recipients, id)
				}
			}
		}
	}
	for start := 0; start < len(recipients); start += 200 {
		end := min(start+200, len(recipients))
		dedupe := e.Type + ":" + e.SubjectID.String()
		if rule.typ == "execution.interrupted" {
			dedupe = "interrupted:" + interruptedMachine.String()
		}
		if e.Type == "work_item.assigned" {
			dedupe = "assigned:" + rawID(data["assignment_id"]).String()
		}
		if e.Type == "requirement.versioned" {
			dedupe += ":" + rawID(data["version_no"]).String()
		}
		if rule.typ == "work_item.review_ready" {
			dedupe = "review:" + e.ProjectID.String()
		}
		if rule.typ == "work_item.accepted" {
			dedupe = "accepted"
		}
		if rule.typ == "execution.cost_paused" {
			dedupe = "cost-paused:" + e.ProjectID.String()
		}

		if e.Type == "collab.message_posted" {
			dedupe = "mention:" + rawID(data["channel_id"]).String()
		}
		if e.Type == "events.subscriber_failed" {
			var subscriber string
			_ = json.Unmarshal(data["subscriber"], &subscriber)
			dedupe = "events.parked:" + subscriber
		}
		if e.Type == "job.dead_lettered" {
			var kind string
			_ = json.Unmarshal(data["kind"], &kind)
			dedupe = "jobs.dead:" + kind
		}
		if e.Type == "machine.credential_changed" {
			dedupe += ":" + rawID(data["rotation_id"]).String() + ":" + string(data["reason_code"])
		}
		if e.Type == "budget.threshold_reached" {
			dedupe += ":" + rawID(data["cap_version"]).String() + ":" + rawID(data["threshold_pct"]).String()
		}
		if e.Type == "work_item.escalated" {
			dedupe += ":" + e.ID.String()
		}

		if rule.typ == "work_item.lease_expired" {
			if m.decisionFacts.LeaseExpiredOn == nil {
				return ErrUnavailable
			}
			fact, found, err := m.decisionFacts.LeaseExpiredOn(ctx, tx, e.TenantID, rawID(data["assignment_id"]))
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
			dedupe = "lease-expired:" + e.SubjectID.String() + ":" + strconv.FormatInt(fact.EndedAt.Unix()/3600, 10)
		}
		link := ""
		if rule.typ == "gateway.key_rotated" {
			link = "/admin/" + e.TenantID.String() + "/gateway"
		}
		if rule.typ == "exception.raised" {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/exceptions?collection=exceptions&id=" + e.SubjectID.String()
		}
		if strings.HasPrefix(rule.typ, "plan.") {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/plan-review?plan_id=" + e.SubjectID.String()
		}
		if e.Type == "work_item.escalated" {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/work-items/" + e.SubjectID.String()
		}
		if e.Type == "collab.message_posted" {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/conversations?channel_id=" + rawID(data["channel_id"]).String()
		}
		if e.Type == "events.subscriber_failed" {
			link = "/system/domain-events"
		}
		if e.SubjectKind == "machine" && e.SubjectID > 0 {
			link = "/admin/" + e.TenantID.String() + "/machines/" + e.SubjectID.String()
		}
		if e.Type == "job.dead_lettered" {
			link = "/system/jobs?state=DEAD"
		}
		if e.Type == "governance.incident_changed" {
			link = "/admin/" + e.TenantID.String() + "/governance/incidents?incident_id=" + e.SubjectID.String()
		}

		if rule.typ == "project.joined" {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String()
		}
		if rule.typ == "work_item.lease_expired" {
			link = "/admin/" + e.TenantID.String() + "/entities/" + e.ProjectID.String() + "/work-items/" + e.SubjectID.String()
		}
		templateKey := "notification." + strings.ReplaceAll(rule.typ, ".", "_")
		params := map[string]string{"subject_id": e.SubjectID.String(), "type": rule.typ}
		if e.Type == "work_item.escalated" {
			var reasonCode string
			_ = json.Unmarshal(data["reason_code"], &reasonCode)
			params["reason_code"] = reasonCode
			params["source_version"] = rawID(data["source_version"]).String()
		}
		if rule.typ == "work_item.lease_expired" {
			params["assignment_id"] = rawID(data["assignment_id"]).String()
		}
		if e.Type == "budget.threshold_reached" {
			params["threshold_pct"] = rawID(data["threshold_pct"]).String()
		}
		if e.Type == "collab.message_posted" {
			templateKey = "notification.mentioned"
		}
		if e.Type == "events.subscriber_failed" {
			templateKey = "notification.parked"
			var subscriber string
			_ = json.Unmarshal(data["subscriber"], &subscriber)
			params["subscriber"] = subscriber
			params["count"] = rawID(data["count"]).String()
		}
		if e.Type == "job.dead_lettered" {
			templateKey = "notification.job_failed"
			var kind string
			_ = json.Unmarshal(data["kind"], &kind)
			params["kind"] = kind
			params["attempts"] = rawID(data["attempts"]).String()
		}
		if e.Type == "machine.credential_changed" {
			var retire time.Time
			if json.Unmarshal(data["retire_old_at"], &retire) == nil && !retire.IsZero() {
				templateKey = "notification.rotation"
				params["retire_at"] = retire.UTC().Format(time.RFC3339)
			}
		}
		_, err := m.Notify(ctx, tx, Notification{TenantID: e.TenantID, ProjectID: e.ProjectID, Recipients: recipients[start:end], Type: rule.typ, Severity: rule.severity, TemplateKey: templateKey, Params: params, LinkPath: link, DedupeKey: dedupe, Permission: rule.permission})
		if err != nil {
			return err
		}
	}
	return nil
}

// parkedNotificationOn is called by the shared dispatcher in the new FAILED
// park transaction. It publishes no new subscriber event and cannot recurse.
func (m *Module) parkedNotificationOn(ctx context.Context, tx *gorm.DB, notice events.ParkedNotice) error {
	data, err := json.Marshal(map[string]any{"subscriber": notice.Subscriber, "count": notice.Count})
	if err != nil {
		return err
	}
	return m.projectEventOn(ctx, tx, events.RecordedEvent{Type: "events.subscriber_failed", SubjectID: notice.EventID, Data: data})
}
