package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AutomationWorkOwnerOn resolves only the lock hint. It grants no read or write
// authority: the dispatcher must re-read via authorized PlanningWorkIdentityOn
// after taking the principal/project locks and reject ownership changes.
type AutomationWorkOwnerOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)

func (m *Module) provideAutomationWorkOwner(s *module.Services) error {
	if err := m.provideAutomationDispatch(s); err != nil {
		return err
	}
	if err := module.Provide[AutomationLinkedItemOn](s, m.automationLinkedItemOn); err != nil {
		return err
	}
	var testRun AutomationTriggerWorkOn
	if err := module.Provide[AutomationTestRunWorkConnector](s, AutomationTestRunWorkConnector{Bind: func(resolve AutomationTriggerWorkOn) error {
		if resolve == nil || testRun != nil {
			return ErrInvalidArgument
		}
		testRun = resolve
		return nil
	}}); err != nil {
		return err
	}
	if err := module.Provide[AutomationWorkParticipantsOn](s, m.automationWorkParticipantsOn); err != nil {
		return err
	}
	if err := module.Provide[AutomationVerificationOn](s, m.automationVerificationOn); err != nil {
		return err
	}
	if err := module.Provide[AutomationTriggerWorkOn](s, AutomationTriggerWorkOn(func(ctx context.Context, tx *gorm.DB, source events.RecordedEvent) (guid.ID, error) {
		if source.Type == "clientwork.test_run_recorded" && source.SubjectKind == "clientwork" {
			if testRun == nil {
				return 0, ErrUnavailable
			}
			return testRun(ctx, tx, source)
		}
		return m.automationTriggerWorkOn(ctx, tx, source)
	})); err != nil {
		return err
	}
	if err := module.Provide[AutomationTransitionOn](s, m.automationTransitionOn); err != nil {
		return err
	}
	if err := module.Provide[AutomationPreparationFieldOn](s, m.automationPreparationFieldOn); err != nil {
		return err
	}
	if err := module.Provide[AutomationAssignmentOn](s, m.automationAssignmentOn); err != nil {
		return err
	}
	return module.Provide[AutomationWorkOwnerOn](s, m.automationWorkOwnerOn)
}

// AutomationTriggerWorkOn resolves non-work event subjects from owner storage.
// Zero means the source has no work target; it must not invent one from payload.
type AutomationTriggerWorkOn func(context.Context, *gorm.DB, events.RecordedEvent) (guid.ID, error)

// The test owner binds after all services are published, avoiding a dependency
// cycle through execution. A missing owner cannot fabricate a work target.
type AutomationTestRunWorkConnector struct {
	Bind func(AutomationTriggerWorkOn) error
}

func (m *Module) automationTriggerWorkOn(ctx context.Context, tx *gorm.DB, source events.RecordedEvent) (guid.ID, error) {
	if !isWorkActivityTransaction(tx) || source.TenantID <= 0 || source.ProjectID <= 0 || source.SubjectID <= 0 {
		return 0, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return 0, ErrUnavailable
	}
	if source.Type == "review_sheet.closed" && source.SubjectKind == "review_sheet" {
		q := query.BindReviewSheets(tx, m.service.names)
		row, err := q.WithContext(ctx).Where(q.TenantID.Eq(source.TenantID), q.ProjectID.Eq(source.ProjectID), q.ID.Eq(source.SubjectID)).First()
		if err != nil {
			return 0, err
		}
		if row.WorkItemID == nil {
			return 0, nil
		}
		return *row.WorkItemID, nil
	}
	return 0, nil
}

// AutomationTransition is an in-process command, never caller-asserted evidence.
// CreatorID supplies current IAM authority; SYSTEM remains the written actor.
type AutomationTransition struct {
	TenantID, ProjectID, WorkItemID, CreatorID, OwnerID, RuleID, RunID guid.ID
	ToState, RunAs                                                     string
	LockedPrincipals                                                   []guid.ID
}
type AutomationTransitionOn func(context.Context, *gorm.DB, AutomationTransition) error

type AutomationPreparationField struct {
	AutomationTransition
	Field, Value string
}
type AutomationPreparationFieldOn func(context.Context, *gorm.DB, AutomationPreparationField) error

type AutomationVerification struct {
	TenantID, ProjectID, WorkItemID, CreatorID, OwnerID, RuleID, RunID guid.ID
	Stage, RunAs                                                       string
	LockedPrincipals                                                   []guid.ID
}
type AutomationVerificationOn func(context.Context, *gorm.DB, AutomationVerification) error
type AutomationWorkParticipantsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)

type automationPrincipalLockSetKey struct{}

func automationPrincipalLockFence(ctx context.Context, ids []guid.ID, actor guid.ID) error {
	allowed, automation := ctx.Value(automationPrincipalLockSetKey{}).([]guid.ID)
	if !automation {
		return nil
	}
	for _, id := range append(slices.Clone(ids), actor) {
		if id > 0 && !slices.Contains(allowed, id) {
			return ErrWorkVersion
		}
	}
	return nil
}

// Discover the canonical pipeline's principal lock set before project locks.
func (m *Module) automationWorkParticipantsOn(ctx context.Context, tx *gorm.DB, tenant, project, workID guid.ID) ([]guid.ID, error) {
	if !isWorkActivityTransaction(tx) || m == nil || m.service == nil {
		return nil, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return nil, err
	}
	return m.automationWorkParticipantsForWorkOn(ctx, tx, work)
}

func (m *Module) automationWorkParticipantsForWorkOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem) ([]guid.ID, error) {
	tenant, project, workID := work.TenantID, work.ProjectID, work.ID
	participants := []guid.ID{work.AccountablePrincipalID}
	hidden, err := m.hiddenAcceptancePrincipalsOn(ctx, tx, tenant, project, workID)
	if err != nil {
		return nil, err
	}
	participants = append(participants, hidden...)
	if work.CurrentClaimID != nil {
		result, err := m.resultOn(ctx, tx, tenant, project, workID, *work.CurrentClaimID)
		if err != nil {
			return nil, err
		}
		if result.ActorID > 0 {
			participants = append(participants, result.ActorID)
		}
		if result.SourceExecutionID != nil {
			source := *result.SourceExecutionID
			if m.verificationExecutor.QualityPrincipalsOn != nil {
				ids, err := m.verificationExecutor.QualityPrincipalsOn(ctx, tx, tenant, source)
				if err != nil {
					return nil, err
				}
				participants = append(participants, ids...)
			}
			if m.verificationExecutor.ReviewersOn != nil {
				for _, stage := range []string{"AGENT_TEST", "AGENT_ACCEPTANCE"} {
					ids, err := m.verificationExecutor.ReviewersOn(ctx, tx, tenant, source, stage)
					if err != nil {
						return nil, err
					}
					participants = append(participants, ids...)
				}
			}
			if m.verificationExecutor.SourcePrincipalOn != nil {
				id, err := m.verificationExecutor.SourcePrincipalOn(ctx, tx, tenant, source)
				if err != nil {
					return nil, err
				}
				if id > 0 {
					participants = append(participants, id)
				}
			}
		}
	}
	slices.Sort(participants)
	return slices.Compact(participants), nil
}

// Request the existing automatic verification pipeline, never a verdict. Frozen
// plans, stage predecessors, reviewer independence and Runner fences stay owned
// by the canonical queue. A request with no applicable stage must not succeed.
func (m *Module) automationVerificationOn(ctx context.Context, tx *gorm.DB, in AutomationVerification) error {
	if authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.CreatorID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() || !slices.Contains([]string{"AUTOMATED", "AGENT_TEST", "AGENT_ACCEPTANCE"}, in.Stage) {
		return ErrInvalidArgument
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.CreatorID, EntityID: in.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	creator = context.WithValue(creator, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	_, work, _, err := m.lockedWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.CreatorID, in.OwnerID, "verification_run")
	if err != nil {
		return err
	}
	participants, err := m.automationWorkParticipantsForWorkOn(ctx, tx, work)
	if err != nil {
		return err
	}
	for _, id := range append(participants, in.CreatorID) {
		if !slices.Contains(in.LockedPrincipals, id) {
			return ErrWorkVersion
		}
	}
	if work.CurrentClaimID == nil || work.PausedAt != nil || work.BaselineStale || !slices.Contains([]string{"INTERNAL_QA", "NEEDS_HUMAN_REVIEW"}, work.State) {
		return ErrResultState
	}
	queueCtx := context.WithValue(ctx, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	if err = m.queueAutomaticVerificationOn(queueCtx, tx, in.TenantID, in.ProjectID, in.WorkItemID, *work.CurrentClaimID); err != nil {
		return err
	}
	q := query.BindVerificationRuns(tx, m.service.names)
	runs, err := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.WorkItemID.Eq(in.WorkItemID), q.ResultID.Eq(*work.CurrentClaimID), q.Stage.Eq(in.Stage)).Find()
	if err != nil {
		return err
	}
	applicable := false
	for _, run := range runs {
		applicable = applicable || run.State == "PENDING" || (run.State == "DONE" && run.Verdict == "PASSED")
	}
	if !applicable {
		return ErrVerificationPending
	}
	w := query.BindWorkItems(tx, m.service.names)
	current, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select(w.Version).Where(w.TenantID.Eq(in.TenantID), w.ProjectID.Eq(in.ProjectID), w.ID.Eq(in.WorkItemID)).First()
	if err != nil {
		return err
	}
	detail := map[string]any{"actor_kind": "SYSTEM", "authority_source": in.RunAs, "rule_id": in.RuleID, "run_id": in.RunID, "creator_id": in.CreatorID, "stage": in.Stage, "result_id": *work.CurrentClaimID}
	if err = m.service.audit(ctx, tx, audit.DomainEvent{TenantID: in.TenantID, EventType: "delivery.automation_verification_requested", TargetType: "work_item", TargetID: in.WorkItemID, Detail: detail}); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "automation.verification_requested", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: in.RunAs}, Subject: events.Subject{Kind: "work_item", ID: in.WorkItemID}, SubjectVersion: int64(current.Version), Data: detail})
	return err
}

func (m *Module) automationTransitionOn(ctx context.Context, tx *gorm.DB, in AutomationTransition) error {
	return m.automationPreparationOn(ctx, tx, in, nil)
}

func (m *Module) automationPreparationFieldOn(ctx context.Context, tx *gorm.DB, in AutomationPreparationField) error {
	if in.ToState != "" || !slices.Contains([]string{"risk", "autonomy_level", "estimate_minutes"}, in.Field) {
		return ErrInvalidArgument
	}
	return m.automationPreparationOn(ctx, tx, in.AutomationTransition, &in)
}

func (m *Module) automationPreparationOn(ctx context.Context, tx *gorm.DB, in AutomationTransition, edit *AutomationPreparationField) error {
	if authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.CreatorID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() || (edit == nil && !slices.Contains([]string{"DRAFT", "READY", "BLOCKED"}, in.ToState)) {
		return ErrInvalidArgument
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.CreatorID, EntityID: in.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	creator = context.WithValue(creator, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	row, active, err := m.preparationWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.CreatorID, in.OwnerID, "work_item_write")
	if err != nil {
		return err
	}
	// A rule cannot clear a pause/hold or override a domain-derived state.
	if row.PausedAt != nil {
		return ErrWorkPreparationState
	}
	preparation, err := preparationRecord(row)
	if err != nil {
		return err
	}
	if edit != nil {
		switch edit.Field {
		case "risk":
			preparation.Risk = edit.Value
		case "autonomy_level":
			preparation.AutonomyLevel = edit.Value
		case "estimate_minutes":
			preparation.EstimateMinutes = nil
			if edit.Value != "" {
				minutes, err := strconv.Atoi(edit.Value)
				if err != nil {
					return ErrInvalidArgument
				}
				preparation.EstimateMinutes = &minutes
			}
		}
	}
	if _, err = validatePreparation(preparation.WorkPreparationBody); err != nil {
		return err
	}
	row.Risk, row.AutonomyLevel, row.EstimateMinutes = preparation.Risk, preparation.AutonomyLevel, preparation.EstimateMinutes
	if err = m.validateContextOn(creator, tx, in.TenantID, in.ProjectID, in.CreatorID, preparation.ContextRefs); err != nil {
		return err
	}
	issues, blocks, err := m.evaluatePreparationOn(creator, tx, row, active)
	if err != nil {
		return err
	}
	state := "READY"
	if len(issues) > 0 {
		state = "DRAFT"
	}
	if len(blocks) > 0 {
		state = "BLOCKED"
	}
	if edit == nil && state != in.ToState {
		return ErrWorkPreparationState
	}
	before, expected := row.State, row.Version
	now := time.Now().UTC().Truncate(time.Millisecond)
	encodedIssues, _ := json.Marshal(issues)
	encodedBlocks, _ := json.Marshal(blocks)
	row.State, row.ReadinessOK, row.ReadinessIssues, row.BlockReasons = state, len(issues) == 0, string(encodedIssues), string(encodedBlocks)
	row.PlanRequired = row.Risk != "LOW" || row.AutonomyLevel != "LOW"
	row.Version++
	row.UpdatedAt, row.UpdatedBy = now, nil
	if before != state {
		row.StateChangedAt = now
	}
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(in.TenantID), w.ProjectID.Eq(in.ProjectID), w.ID.Eq(in.WorkItemID), w.Version.Eq(expected)).Select(w.Risk, w.AutonomyLevel, w.EstimateMinutes, w.State, w.ReadinessOK, w.ReadinessIssues, w.BlockReasons, w.PlanRequired, w.Version, w.UpdatedAt, w.UpdatedBy, w.StateChangedAt).Updates(row)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	check := &model.ReadinessCheck{TenantID: in.TenantID, ID: guid.ID(id), ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, WorkItemVersion: row.Version, Trigger: "MANUAL", OK: row.ReadinessOK, Issues: row.ReadinessIssues, CheckedBy: "SYSTEM", CreatedAt: now}
	if edit != nil {
		check.Trigger = "EDIT"
	}
	if err = query.BindReadinessChecks(tx, m.service.names).WithContext(ctx).Create(check); err != nil {
		return err
	}
	id, err = m.ids.Next()
	if err != nil {
		return err
	}
	ref := in.RunAs + ":run:" + in.RunID.String()
	transition := &model.WorkItemTransition{TenantID: in.TenantID, ID: guid.ID(id), ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, FromState: &before, ToState: state, Trigger: "SYSTEM", ActorKind: "SYSTEM", AuthoritySource: in.RunAs, ReasonCode: "READINESS_CHECKED", SourceRef: &ref, VersionAfter: row.Version, CreatedAt: now}
	// The canonical transition insert invokes the installed workflow conditions.
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return err
	}
	if _, err = m.recordWorkActivityOn(ctx, tx, in.TenantID, in.WorkItemID, WorkActivityInput{Kind: "TRANSITIONED", ActorKind: "SYSTEM", RefKind: "transition", RefID: transition.ID, Title: "Automation readiness transition"}); err != nil {
		return err
	}
	detail := map[string]any{"actor_kind": "SYSTEM", "authority_source": in.RunAs, "rule_id": in.RuleID, "run_id": in.RunID, "creator_id": in.CreatorID, "check_id": check.ID, "from": before, "to": state}
	if edit != nil {
		detail["field"] = edit.Field
	}
	if err = m.service.audit(ctx, tx, audit.DomainEvent{TenantID: in.TenantID, EventType: "delivery.work_readiness_checked", TargetType: "work_item", TargetID: in.WorkItemID, Detail: detail}); err != nil {
		return err
	}
	event := events.Event{TenantID: in.TenantID, ProjectID: in.ProjectID, Type: "work_item.ready_checked", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: in.RunAs}, Subject: events.Subject{Kind: "work_item", ID: in.WorkItemID}, SubjectVersion: int64(row.Version), Data: detail}
	if _, err = m.service.events.Publish(ctx, tx, event); err != nil {
		return err
	}
	if before != state {
		event.Type = "work_item.state_changed"
		_, err = m.service.events.Publish(ctx, tx, event)
	}
	return err
}
func (m *Module) automationWorkOwnerOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) (guid.ID, error) {
	if tx == nil || tenant <= 0 || project <= 0 || work <= 0 {
		return 0, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return 0, ErrUnavailable
	}
	q := query.BindWorkItems(tx, m.service.names)
	row, err := q.WithContext(ctx).Select(q.AccountablePrincipalID).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if err != nil {
		return 0, err
	}
	return row.AccountablePrincipalID, nil
}

// AutomationAssignment separates current creator authorization from SYSTEM writes.
// It creates only an assignment; no proposal, execution or result is invented.
type AutomationAssignment struct {
	AutomationTransition
	AssigneePrincipalID guid.ID
}
type AutomationAssignmentOn func(context.Context, *gorm.DB, AutomationAssignment) error

func (m *Module) automationAssignmentOn(ctx context.Context, tx *gorm.DB, in AutomationAssignment) error {
	_, err := m.automationAssignmentResultOn(ctx, tx, in, 0, 0)
	return err
}

func (m *Module) automationAssignmentResultOn(ctx context.Context, tx *gorm.DB, in AutomationAssignment, proposal, decision guid.ID) (guid.ID, error) {
	if !isWorkActivityTransaction(tx) || authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.CreatorID <= 0 || in.AssigneePrincipalID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() {
		return 0, ErrInvalidArgument
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.CreatorID, EntityID: in.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	creator = context.WithValue(creator, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	if err := automationPrincipalLockFence(creator, []guid.ID{in.OwnerID, in.AssigneePrincipalID}, in.CreatorID); err != nil {
		return 0, err
	}
	_, work, _, err := m.lockedWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.CreatorID, in.OwnerID, "work_item_assign")
	if err != nil {
		return 0, err
	}
	return (clientWork{m: m}).dispatchApplyActorOn(creator, ctx, tx, in.TenantID, in.ProjectID, DispatchAssignmentInput{WorkItemID: in.WorkItemID, ProposalID: proposal, DecisionID: decision, AssigneePrincipalID: in.AssigneePrincipalID, ExpectedWorkVersion: work.Version, Reason: "Automation assignment"}, &in)
}

// The proposal owner binds these commands without a reverse module dependency.
// Discover runs before principal/project locks; WorkOn fences the entire roster.
type AutomationDispatch struct {
	AutomationTransition
	Role string
}

type AutomationLinkedItem struct {
	AutomationTransition
	Type, TitleTemplate string
}
type AutomationLinkedItemOn func(context.Context, *gorm.DB, AutomationLinkedItem) error

func (m *Module) automationLinkedItemOn(ctx context.Context, tx *gorm.DB, in AutomationLinkedItem) error {
	if !isWorkActivityTransaction(tx) || authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.CreatorID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() {
		return ErrInvalidArgument
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.CreatorID, EntityID: in.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	creator = context.WithValue(creator, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	if err := automationPrincipalLockFence(creator, []guid.ID{in.OwnerID}, in.CreatorID); err != nil {
		return err
	}
	_, source, _, err := m.lockedWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.CreatorID, in.OwnerID, "work_item_write")
	if err != nil {
		return err
	}
	if source.Objective == nil {
		return ErrInvalidArgument
	}
	title := strings.NewReplacer("{title}", source.Title, "{number}", strconv.Itoa(source.Number)).Replace(in.TitleTemplate)
	if strings.ContainsAny(title, "{}") {
		return ErrInvalidArgument
	}
	input := workCreateBody{Title: title, Type: in.Type, Description: source.Description, Objective: *source.Objective, AccountablePrincipalID: source.AccountablePrincipalID, RequirementID: source.RequirementID}
	if source.RequirementID != nil {
		baseline := source.BaselineVersionNo
		input.BaselineVersionNo = &baseline
	}
	if in.Type == "BUG" {
		input.Severity, input.Priority, input.Reproducibility, input.FoundVersion = source.Severity, source.Priority, source.Reproducibility, source.FoundVersion
	}
	q := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := q.WithContext(creator).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(in.TenantID), q.ProjectID.Eq(in.ProjectID), q.WorkItemID.Eq(source.ID)).Order(q.Position).Find()
	if err != nil {
		return err
	}
	for _, criterion := range criteria {
		input.Criteria = append(input.Criteria, workCriterionInput{Text: criterion.Text, VerificationMethod: criterion.VerificationMethod})
	}
	created, err := m.createWorkActorOn(creator, ctx, tx, in.TenantID, in.ProjectID, in.CreatorID, input, false, &in.AutomationTransition)
	if err != nil {
		return err
	}
	// A NULL SYSTEM creator cannot satisfy an "own rows" read constraint.
	// Reuse the actual read policy so the action cannot commit an inaccessible
	// draft after merely checking permission to create the source owner's work.
	if _, err = m.readWork(creator, tx, in.TenantID, in.ProjectID, created.ID, in.CreatorID); err != nil {
		return err
	}
	// Both sides retain the immutable reference through the canonical activity
	// ledger. This creates no dependency, parent/container or acceptance claim.
	for _, pair := range [][2]guid.ID{{source.ID, created.ID}, {created.ID, source.ID}} {
		if _, err = m.recordWorkActivityOn(ctx, tx, in.TenantID, pair[0], WorkActivityInput{Kind: "DECISION_MADE", ActorKind: "SYSTEM", RefKind: "work_item", RefID: pair[1], Title: "Automation linked draft"}); err != nil {
			return err
		}
	}
	return nil
}

type AutomationDispatchConnector struct {
	Bind    func(discover func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error), run func(context.Context, *gorm.DB, AutomationDispatch) error) error
	WorkOn  func(context.Context, *gorm.DB, AutomationDispatch) (DispatchWorkFacts, error)
	ApplyOn func(context.Context, *gorm.DB, AutomationAssignment, guid.ID, guid.ID) (guid.ID, error)
}
type AutomationDispatchCommands struct {
	Discover func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error)
	Run      func(context.Context, *gorm.DB, AutomationDispatch) error
}

func (m *Module) provideAutomationDispatch(s *module.Services) error {
	commands := &AutomationDispatchCommands{}
	if err := module.Provide[*AutomationDispatchCommands](s, commands); err != nil {
		return err
	}
	return module.Provide[AutomationDispatchConnector](s, AutomationDispatchConnector{
		Bind: func(discover func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error), run func(context.Context, *gorm.DB, AutomationDispatch) error) error {
			if discover == nil || run == nil || commands.Run != nil {
				return ErrInvalidArgument
			}
			commands.Discover, commands.Run = discover, run
			return nil
		},
		WorkOn:  m.automationDispatchWorkOn,
		ApplyOn: m.automationAssignmentResultOn,
	})
}

func (m *Module) automationDispatchWorkOn(ctx context.Context, tx *gorm.DB, in AutomationDispatch) (DispatchWorkFacts, error) {
	if !isWorkActivityTransaction(tx) || authn.PrincipalIDFromContext(ctx) != 0 || authn.TenantIDFromContext(ctx) != in.TenantID || in.CreatorID <= 0 || in.RuleID <= 0 || in.RunID <= 0 || in.RunAs != "SYSTEM:automation:"+in.RuleID.String() || in.Role != "EXECUTOR" {
		return DispatchWorkFacts{}, ErrInvalidArgument
	}
	creator := authn.WithClaims(ctx, &authn.Claims{TenantID: in.TenantID, PrincipalID: in.CreatorID, EntityID: in.ProjectID, SubjectType: authn.SubjectTypePrincipal})
	creator = context.WithValue(creator, automationPrincipalLockSetKey{}, slices.Clone(in.LockedPrincipals))
	if err := automationPrincipalLockFence(creator, []guid.ID{in.OwnerID}, in.CreatorID); err != nil {
		return DispatchWorkFacts{}, err
	}
	_, _, _, err := m.lockedWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID, in.CreatorID, in.OwnerID, "work_item_assign")
	if err != nil {
		return DispatchWorkFacts{}, err
	}
	return (clientWork{m: m}).dispatchWorkOn(creator, tx, in.TenantID, in.ProjectID, in.WorkItemID)
}
