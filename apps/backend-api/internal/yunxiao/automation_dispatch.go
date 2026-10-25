package yunxiao

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AutomationNotice is source-resolved, never accepted as an HTTP command.
// NotifyOn must persist the canonical notification in this same transaction.
type AutomationNotice struct {
	TenantID, ProjectID, RuleID, RunID, WorkItemID, RecipientID guid.ID
	RuleName, RunAs                                             string
}
type AutomationNotifierConnector struct {
	Bind func(func(context.Context, *gorm.DB, AutomationNotice) error) error
}

// AutomationReportEntry is resolved from the locked rule and durable trigger.
// The report owner rechecks current authority and actual work facts on this tx.
type AutomationReportEntry struct {
	TenantID, ProjectID, RuleID, RunID, WorkItemID, CreatorID, PrincipalID guid.ID
	Section, RunAs                                                         string
	OccurredAt                                                             time.Time
}
type AutomationReportWriterConnector struct {
	Bind func(func(context.Context, *gorm.DB, AutomationReportEntry) error) error
}

// AutomationPlanningChange is dispatcher-resolved authority, never HTTP input.
type AutomationPlanningChange struct {
	TenantID, ProjectID, WorkItemID, CreatorID, RuleID, RunID guid.ID
	RunAs                                                     string
}

func automationAssignmentTarget(action AutomationAction) (guid.ID, bool) {
	if action.Kind != "ASSIGN" || len(action.Parameters) != 1 {
		return 0, false
	}
	id, err := guid.RequireID(action.Parameters["principal_id"])
	return id, err == nil
}

func automationAgentDispatch(action AutomationAction) bool {
	return action.Kind == "DISPATCH_TO_AGENT" && len(action.Parameters) == 1 && action.Parameters["role"] == "EXECUTOR"
}

func automationLinkedDraft(action AutomationAction) bool {
	return action.Kind == "CREATE_LINKED_ITEM" && len(action.Parameters) == 2 && action.Parameters["type"] != "" && action.Parameters["title_template"] != ""
}

func (m *Module) registerAutomationSystemActions(s *module.Services) error {
	registry, err := module.Resolve[governance.SystemActions](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	} // Minimal read-only composition.
	if err != nil {
		return err
	}
	if m.automationSystemAllowed, err = module.Resolve[governance.SystemActionAllowed](s); err != nil {
		return err
	}
	for _, code := range []string{"work_item_write", "work_item_assign", "verification_run", "workflow_manage"} {
		if err = registry.Register("automation", code); err != nil {
			return err
		}
	}
	return nil
}

func automationActionPermission(action AutomationAction) string {
	switch action.Kind {
	case "ASSIGN", "DISPATCH_TO_AGENT":
		return "work_item_assign"
	case "REQUEST_VERIFICATION":
		return "verification_run"
	case "SET_FIELD", "TRANSITION", "CREATE_LINKED_ITEM":
		return "work_item_write"
	default:
		return "workflow_manage"
	}
}

func automationReadinessTransition(action AutomationAction) bool {
	return action.Kind == "TRANSITION" && len(action.Parameters) == 1 && (stateKey.MatchString(action.Parameters["to_state_key"]) || slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, action.Parameters["to_state_key"]))
}

func automationVerificationRequest(action AutomationAction) bool {
	return action.Kind == "REQUEST_VERIFICATION" && len(action.Parameters) == 1 && slices.Contains([]string{"AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"}, action.Parameters["stage"])
}

func automationPreparationField(action AutomationAction) bool {
	_, valuePresent := action.Parameters["value"]
	return action.Kind == "SET_FIELD" && len(action.Parameters) == 2 && valuePresent && slices.Contains([]string{"risk", "autonomy_level", "estimate_minutes"}, action.Parameters["field"])
}

func (m *Module) automationTransitionOn(ctx, creator context.Context, tx *gorm.DB, rule *model.AutomationRule, work delivery.PlanningWorkIdentity, workID, runID guid.ID, lockedPrincipals []guid.ID, action AutomationAction) error {
	target := action.Parameters["to_state_key"]
	if stateKey.MatchString(target) {
		workflow, err := m.effectiveWorkflowOn(creator, tx, rule.TenantID, rule.ProjectID, work.Type)
		if err != nil {
			return err
		}
		mapped := ""
		for _, state := range workflow.States {
			if state.Key == target {
				mapped = state.MapsTo
				break
			}
		}
		if mapped == "" {
			return ErrInvalid
		}
		target = mapped
	}
	return m.automationTransition(ctx, tx, delivery.AutomationTransition{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: work.PrincipalID, RuleID: rule.ID, RunID: runID, RunAs: "SYSTEM:automation:" + rule.ID.String(), ToState: target, LockedPrincipals: lockedPrincipals})
}

func automationPlanningField(action AutomationAction) bool {
	if action.Kind != "SET_FIELD" || len(action.Parameters) != 2 || !slices.Contains([]string{"iteration", "version", "milestone"}, action.Parameters["field"]) {
		return false
	}
	value, present := action.Parameters["value"]
	if !present {
		return false
	}
	if value == "" {
		return true // explicit clear
	}
	_, err := guid.RequireID(value)
	return err == nil
}

func (m *Module) automationPlanningOn(ctx, creator context.Context, tx *gorm.DB, change AutomationPlanningChange, action AutomationAction) error {
	if authn.PrincipalIDFromContext(ctx) != 0 || !automationPlanningField(action) || change.RunID <= 0 || change.RuleID <= 0 || change.RunAs != "SYSTEM:automation:"+change.RuleID.String() || authn.PrincipalIDFromContext(creator) != change.CreatorID {
		return ErrInvalid
	}
	if err := m.projectAuthorityOn(creator, tx, change.TenantID, change.ProjectID, "work_item_write"); err != nil {
		return err
	}
	if m.planningWork == nil {
		return ErrUnavailable
	}
	if _, err := m.planningWork(creator, tx, change.TenantID, change.ProjectID, change.WorkItemID); err != nil {
		return err
	}
	// Lock/read the existing assignment and preserve the two unrelated fields.
	row, err := m.assignmentOn(ctx, tx, change.TenantID, change.ProjectID, change.WorkItemID)
	if err != nil {
		return err
	}
	in, version := PlanningAssignmentInput{}, 0
	if row != nil {
		in, version = assignmentView(row).PlanningAssignmentInput, row.Version
	}
	var target *guid.ID
	if action.Parameters["value"] != "" {
		id, err := guid.RequireID(action.Parameters["value"])
		if err != nil {
			return ErrInvalid
		}
		target = &id
	}
	switch action.Parameters["field"] {
	case "iteration":
		in.IterationID = target
	case "version":
		in.VersionID = target
	case "milestone":
		in.MilestoneID = target
	}
	_, err = m.assignPlanningActorOn(ctx, creator, tx, change.TenantID, change.ProjectID, change.WorkItemID, version, in, &change)
	return err
}

func (m *Module) recordAutomationPlanningOn(ctx context.Context, tx *gorm.DB, id guid.ID, version int, change AutomationPlanningChange) error {
	if m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	detail := map[string]any{"actor_kind": "SYSTEM", "authority_source": change.RunAs, "rule_id": change.RuleID, "run_id": change.RunID, "creator_id": change.CreatorID, "work_item_id": change.WorkItemID}
	if err := m.audit(ctx, tx, audit.DomainEvent{TenantID: change.TenantID, EventType: "planning.assignment_changed", TargetType: "planning_assignment", TargetID: id, Detail: detail}); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: change.TenantID, ProjectID: change.ProjectID, Type: "planning.assignment_changed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: change.RunAs}, Subject: events.Subject{Kind: "planning_assignment", ID: id}, SubjectVersion: int64(version), Data: detail})
	return err
}

type AutomationActionResult struct {
	Action    string `json:"action"`
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code,omitempty"`
}

func (m *Module) provideAutomationDispatcher(s *module.Services) error {
	if err := module.Provide[AutomationReportWriterConnector](s, AutomationReportWriterConnector{Bind: func(write func(context.Context, *gorm.DB, AutomationReportEntry) error) error {
		if write == nil || m.automationReport != nil {
			return ErrInvalid
		}
		m.automationReport = write
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[AutomationNotifierConnector](s, AutomationNotifierConnector{Bind: func(notify func(context.Context, *gorm.DB, AutomationNotice) error) error {
		if notify == nil || m.automationNotify != nil {
			return ErrInvalid
		}
		m.automationNotify = notify
		return nil
	}})
}

func (m *Module) consumeAutomation(ctx context.Context, tx *gorm.DB, envelope events.Envelope) error {
	if envelope.TenantID <= 0 || envelope.ProjectID <= 0 || envelope.Subject.ID <= 0 {
		return nil
	}
	// Re-read the actual durable source. Subscriber input cannot assert an outcome.
	if m.automationEvents == nil {
		return ErrUnavailable
	}
	source, err := m.automationEvents(ctx, tx, envelope.EventID-1, 1)
	if err != nil {
		return err
	}
	if len(source) != 1 || source[0].ID != envelope.EventID || source[0].TenantID != envelope.TenantID || source[0].ProjectID != envelope.ProjectID || source[0].SubjectKind != envelope.Subject.Kind || source[0].SubjectID != envelope.Subject.ID || source[0].Type != envelope.EventType {
		return ErrInvalid
	}
	workID, err := m.automationSourceWorkOn(ctx, tx, source[0])
	if err != nil {
		return err
	}
	if workID == 0 {
		return nil
	}
	q := query.BindAutomationRules(tx, m.names)
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(envelope.TenantID), q.ProjectID.Eq(envelope.ProjectID), q.ID.Gt(after), q.Enabled.Is(true), q.ArchivedAt.IsNull()).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			if !source[0].OccurredAt.IsZero() && row.CreatedAt.After(source[0].OccurredAt) {
				continue
			}
			var def AutomationDefinition
			if row.Definition == "" {
				continue
			}
			if json.Unmarshal([]byte(row.Definition), &def) != nil {
				return ErrUnavailable
			}
			if !automationTriggerMatches(def.Trigger.EventType, source[0].Type) {
				continue
			}
			key := "event:" + source[0].ID.String() + ":work:" + workID.String()
			if err = m.dispatchAutomationOn(ctx, tx, row, source[0], workID, key, nil); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (m *Module) automationSourceWorkOn(ctx context.Context, tx *gorm.DB, source events.RecordedEvent) (guid.ID, error) {
	if source.SubjectKind == "work_item" {
		return source.SubjectID, nil
	}
	if !(source.Type == "review_sheet.closed" && source.SubjectKind == "review_sheet") && !(source.Type == "clientwork.test_run_recorded" && source.SubjectKind == "clientwork") {
		return 0, nil
	}
	if m.automationTriggerWork == nil {
		return 0, ErrUnavailable
	}
	return m.automationTriggerWork(ctx, tx, source)
}

// Authorization contexts below are used only to read current creator eligibility
// and work facts. All action writes and audit retain the genuine SYSTEM context.
// The notification/report-draft executors grant no authority over lifecycle commands.
func (m *Module) dispatchAutomationOn(ctx context.Context, tx *gorm.DB, hint *model.AutomationRule, source events.RecordedEvent, workID guid.ID, key string, scheduledAt *time.Time) error {
	if tx == nil || tx.Statement == nil || hint == nil || hint.ID <= 0 || workID <= 0 || key == "" {
		return ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalid
	}
	if m.audit == nil || m.events == nil || m.planningWork == nil || m.automationOwner == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	owner, err := m.automationOwner(ctx, tx, hint.TenantID, hint.ProjectID, workID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	principals := []guid.ID{hint.CreatedBy, owner}
	var lockDefinition AutomationDefinition
	if json.Unmarshal([]byte(hint.Definition), &lockDefinition) != nil {
		return ErrUnavailable
	}
	for _, action := range lockDefinition.Actions {
		if automationAgentDispatch(action) && m.automationDispatch != nil && m.automationDispatch.Discover != nil {
			extra, err := m.automationDispatch.Discover(ctx, tx, hint.TenantID)
			if err != nil {
				return err
			}
			principals = append(principals, extra...)
		}
		if target, valid := automationAssignmentTarget(action); valid {
			principals = append(principals, target)
		}
		_, assignment := automationAssignmentTarget(action)
		if (automationLinkedDraft(action) && m.automationLinkedItem != nil) || (automationAgentDispatch(action) && m.automationDispatch != nil) || (assignment && m.automationAssignment != nil) || (automationVerificationRequest(action) && m.automationVerification != nil) || (automationReadinessTransition(action) && m.automationTransition != nil) || (automationPreparationField(action) && m.automationPreparationField != nil) {
			if m.automationWorkParticipants == nil {
				return ErrUnavailable
			}
			extra, err := m.automationWorkParticipants(ctx, tx, hint.TenantID, hint.ProjectID, workID)
			if err != nil {
				return err
			}
			principals = append(principals, extra...)
		}
	}
	slices.Sort(principals)
	principals = slices.Compact(principals)
	for _, principal := range principals {
		if err = m.principals.Lock(ctx, tx, principal); err != nil {
			return err
		}
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: hint.TenantID, PrincipalID: hint.CreatedBy, EntityID: hint.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: hint.TenantID, EntityID: hint.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	authorityErr := m.projectAuthorityOn(creator, tx, hint.TenantID, hint.ProjectID, "workflow_manage")
	if authorityErr != nil && automationAudienceError(authorityErr) != nil {
		return authorityErr
	}
	q := query.BindAutomationRules(tx, m.names)
	rule, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(hint.TenantID), q.ProjectID.Eq(hint.ProjectID), q.ID.Eq(hint.ID)).First()
	if err != nil {
		return err
	}
	if rule.Version != hint.Version {
		return nil
	} // the next event/slot uses the new definition
	runs := query.BindAutomationRuns(tx, m.names)
	var old model.AutomationRun
	err = tx.WithContext(ctx).Table(m.names.AutomationRuns.String()).Where("tenant_id = ? AND rule_id = ? AND trigger_key = ?", rule.TenantID, rule.ID, key).First(&old).Error
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var def AutomationDefinition
	if json.Unmarshal([]byte(rule.Definition), &def) != nil {
		return ErrUnavailable
	}
	matched, reason, actions, err := automationDefinitionMatches(rule, source)
	if err != nil {
		return err
	}
	now := m.now()
	if scheduledAt != nil && (rule.CreatedAt.After(*scheduledAt) || rule.UpdatedAt.After(*scheduledAt)) {
		matched, reason = false, "RULE_CHANGED_AFTER_SLOT"
	}
	if authorityErr != nil {
		matched, reason = false, "CURRENT_AUTHORITY_REVOKED"
	}
	if scheduledAt != nil {
		source.ID, err = m.events.Publish(ctx, tx, events.Event{TenantID: rule.TenantID, ProjectID: rule.ProjectID, Type: "automation.schedule_triggered", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:automation:" + rule.ID.String()}, Subject: events.Subject{Kind: "work_item", ID: workID}, SubjectVersion: int64(rule.Version), Data: map[string]any{"rule_id": rule.ID, "slot": scheduledAt.Format(time.RFC3339)}})
		if err != nil {
			return err
		}
		if source.ID <= 0 {
			return ErrUnavailable
		}
	}
	id, err := m.nextID()
	if err != nil {
		return err
	}
	receipt := &model.AutomationRun{ID: id, TenantID: rule.TenantID, ProjectID: rule.ProjectID, RuleID: rule.ID, RuleVersion: rule.Version, RuleName: rule.Name, CreatedBy: rule.CreatedBy, TriggerEventID: source.ID, WorkItemID: workID, TriggerKey: &key, RunAs: "SYSTEM:automation:" + rule.ID.String(), DefinitionSnapshot: rule.Definition, ActionsResult: "[]", Outcome: "SKIPPED", Error: reason, CreatedAt: now}
	if matched {
		var count int64
		hour := now.UTC().Truncate(time.Hour)
		if err = tx.WithContext(ctx).Table(m.names.AutomationRuns.String()).Where("tenant_id = ? AND rule_id = ? AND created_at >= ? AND created_at < ? AND outcome <> ?", rule.TenantID, rule.ID, hour, hour.Add(time.Hour), "SKIPPED").Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(def.MaxRunsPerHour) {
			matched, receipt.Error = false, "HOURLY_LIMIT"
		}
	}
	results := []AutomationActionResult{}
	if matched {
		work, e := m.planningWork(creator, tx, rule.TenantID, rule.ProjectID, workID)
		if e != nil {
			if automationAudienceError(e) != nil {
				return e
			}
			receipt.Error = "WORK_UNAVAILABLE"
		} else {
			if work.PrincipalID != owner {
				return ErrConflict
			}
			receipt.Outcome, receipt.Error = "SUCCEEDED", ""
			// Validate every adapter before writes; an unsupported action cannot
			// leave a preceding NOTIFY committed under a failed receipt.
			for _, action := range actions {
				code := ""
				if automationLinkedDraft(action) {
					if m.automationLinkedItem == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if automationAgentDispatch(action) {
					if m.automationDispatch == nil || m.automationDispatch.Run == nil || m.automationDispatch.Discover == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if _, valid := automationAssignmentTarget(action); valid {
					if m.automationAssignment == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if automationReadinessTransition(action) {
					if m.automationTransition == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if automationPreparationField(action) {
					if m.automationPreparationField == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if automationVerificationRequest(action) {
					if m.automationVerification == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if automationPlanningField(action) {
					if m.planningWork == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if action.Kind == "ADD_REPORT_ENTRY" && len(action.Parameters) == 1 && slices.Contains([]string{"completed", "uncompleted"}, action.Parameters["section"]) {
					if m.automationReport == nil {
						code = "ACTION_UNAVAILABLE"
					}
				} else if action.Kind != "NOTIFY" || action.Parameters["recipients_rule"] != "OWNER" || len(action.Parameters) != 1 {
					code = "ACTION_UNSUPPORTED"
				} else if m.automationNotify == nil {
					code = "ACTION_UNAVAILABLE"
				}
				if code == "" && (m.automationSystemAllowed == nil || !m.automationSystemAllowed("automation", automationActionPermission(action))) {
					code = "SYSTEM_ACTION_NOT_REGISTERED"
				}
				results = append(results, AutomationActionResult{Action: action.Kind, ErrorCode: code})
				if code != "" {
					receipt.Outcome, receipt.Error = "FAILED", code
				}
			}
			if receipt.Outcome == "FAILED" {
				for i := range results {
					if results[i].ErrorCode == "" {
						results[i].ErrorCode = "NOT_EXECUTED"
					}
				}
			}
			if receipt.Outcome == "SUCCEEDED" {
				if err = tx.SavePoint("automation_dispatch").Error; err != nil {
					return err
				}
				for i := range actions {
					if automationLinkedDraft(actions[i]) {
						e = m.automationLinkedItem(ctx, tx, delivery.AutomationLinkedItem{AutomationTransition: delivery.AutomationTransition{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: owner, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs, LockedPrincipals: principals}, Type: actions[i].Parameters["type"], TitleTemplate: actions[i].Parameters["title_template"]})
					} else if automationAgentDispatch(actions[i]) {
						e = m.automationDispatch.Run(ctx, tx, delivery.AutomationDispatch{AutomationTransition: delivery.AutomationTransition{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: owner, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs, LockedPrincipals: principals}, Role: actions[i].Parameters["role"]})
					} else if target, valid := automationAssignmentTarget(actions[i]); valid {
						e = m.automationAssignment(ctx, tx, delivery.AutomationAssignment{AutomationTransition: delivery.AutomationTransition{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: owner, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs, LockedPrincipals: principals}, AssigneePrincipalID: target})
					} else if automationReadinessTransition(actions[i]) {
						e = m.automationTransitionOn(ctx, creator, tx, rule, work, workID, id, principals, actions[i])
					} else if automationPreparationField(actions[i]) {
						e = m.automationPreparationField(ctx, tx, delivery.AutomationPreparationField{AutomationTransition: delivery.AutomationTransition{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: owner, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs, LockedPrincipals: principals}, Field: actions[i].Parameters["field"], Value: actions[i].Parameters["value"]})
					} else if automationVerificationRequest(actions[i]) {
						e = m.automationVerification(ctx, tx, delivery.AutomationVerification{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, OwnerID: owner, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs, Stage: actions[i].Parameters["stage"], LockedPrincipals: principals})
					} else if automationPlanningField(actions[i]) {
						e = m.automationPlanningOn(ctx, creator, tx, AutomationPlanningChange{TenantID: rule.TenantID, ProjectID: rule.ProjectID, WorkItemID: workID, CreatorID: rule.CreatedBy, RuleID: rule.ID, RunID: id, RunAs: receipt.RunAs}, actions[i])
					} else if actions[i].Kind == "ADD_REPORT_ENTRY" {
						e = m.automationReport(ctx, tx, AutomationReportEntry{TenantID: rule.TenantID, ProjectID: rule.ProjectID, RuleID: rule.ID, RunID: id, WorkItemID: workID, CreatorID: rule.CreatedBy, PrincipalID: work.PrincipalID, Section: actions[i].Parameters["section"], RunAs: receipt.RunAs, OccurredAt: source.OccurredAt})
					} else {
						e = m.automationNotify(ctx, tx, AutomationNotice{TenantID: rule.TenantID, ProjectID: rule.ProjectID, RuleID: rule.ID, RunID: id, WorkItemID: workID, RecipientID: work.PrincipalID, RuleName: rule.Name, RunAs: receipt.RunAs})
					}
					if e != nil {
						if err = tx.RollbackTo("automation_dispatch").Error; err != nil {
							return err
						}
						receipt.Outcome, receipt.Error, results[i].ErrorCode = "FAILED", "ACTION_FAILED", "ACTION_FAILED"
						break
					}
					results[i].OK = true
				}
				if receipt.Outcome == "FAILED" {
					for i := range results {
						results[i].OK = false
						if results[i].ErrorCode == "" {
							results[i].ErrorCode = "ROLLED_BACK"
						}
					}
				}
			}
		}
	}
	data, err := json.Marshal(results)
	if err != nil {
		return err
	}
	receipt.ActionsResult = string(data)
	receipt.DurationMS = max(int64(0), m.now().Sub(now).Milliseconds())
	if err = runs.WithContext(ctx).Create(receipt); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, rule.TenantID, "automation.run_recorded", "automation_run", id)
	event.Detail["run_as"] = receipt.RunAs
	if err = m.audit(ctx, tx, event); err != nil {
		return err
	}
	if receipt.Outcome == "FAILED" {
		_, err = m.events.Publish(ctx, tx, events.Event{TenantID: rule.TenantID, ProjectID: rule.ProjectID, Type: "automation.run_failed", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: receipt.RunAs}, Subject: events.Subject{Kind: "automation_run", ID: id}, SubjectVersion: 1, CausationID: source.ID, Data: map[string]any{"rule_id": rule.ID, "work_item_id": workID}})
	}
	return err
}

func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: "yunxiao.automation_schedule", Kind: "yunxiao.automation_schedule", Interval: time.Minute, Timezone: "UTC", TenantScope: jobs.ScopePerTenant, Misfire: jobs.MisfireSkip, Payload: map[string]any{}}}
}
func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{ingressHandler(m), {Kind: "yunxiao.automation_schedule", Queue: "default", Timeout: time.Minute, MaxAttempts: 3, Run: m.automationScheduleJob}}
}
func (m *Module) automationScheduleJob(ctx context.Context, job jobs.Job) error {
	if job.TenantID <= 0 {
		return ErrInvalid
	}
	if m.database() == nil {
		return ErrUnavailable
	}
	// Job ID is not a schedule occurrence: manual/retried jobs share the same
	// UTC slot key and cannot generate extra runs. Missed minutes are skipped.
	slot := m.now().UTC().Truncate(time.Minute)
	var after guid.ID
	for {
		var rows []*model.AutomationRule
		q := query.BindAutomationRules(m.database(), m.names)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Gt(after), q.Enabled.Is(true), q.ArchivedAt.IsNull()).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			after = row.ID
			var def AutomationDefinition
			if row.Definition == "" {
				continue
			}
			if json.Unmarshal([]byte(row.Definition), &def) != nil {
				return ErrUnavailable
			}
			if def.Trigger.EventType != "schedule" {
				continue
			}
			if _, err = validateAutomationDefinition(def); err != nil {
				return err
			}
			loc, err := time.LoadLocation(def.Trigger.Timezone)
			if err != nil {
				return ErrUnavailable
			}
			plan, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(def.Trigger.Cron)
			if err != nil {
				return ErrUnavailable
			}
			if !plan.Next(slot.Add(-time.Minute).In(loc)).Equal(slot) {
				continue
			}
			key := "schedule:" + strconv.FormatInt(slot.Unix(), 10)
			err = m.database().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				// A scheduled source is a genuine durable event, not an invented
				// event ID or Runner execution. Source and action commit together.
				source := events.RecordedEvent{TenantID: row.TenantID, ProjectID: row.ProjectID, SubjectKind: "work_item", SubjectID: def.Trigger.WorkItemID, Type: "schedule", Data: json.RawMessage(`{}`), OccurredAt: slot}
				return m.dispatchAutomationOn(ctx, tx, row, source, source.SubjectID, key, &slot)
			})
			if err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
