package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"reflect"
)

// WorkflowTransition is an immutable receipt evaluated in the command's own
// transaction. Facts are resolved by Delivery, never supplied by HTTP callers.
type WorkflowTransition struct {
	TenantID, ProjectID, WorkItemID, ActorID guid.ID
	Type, From, To, ActorKind, Trigger       string
	FromKey, ToKey                           string
	FactOn                                   func(kind, rule, argument string) (bool, error)
}
type WorkflowTransitionCheck func(context.Context, *gorm.DB, WorkflowTransition) error

// WorkflowSelection resolves exact effective display keys and checks their cell.
// Facts and actor identity always come from the locked Delivery aggregate.
type WorkflowSelection func(context.Context, *gorm.DB, WorkflowTransition) (string, error)
type WorkflowTransitionConnector struct {
	Bind          func(WorkflowTransitionCheck) error
	BindSelection func(WorkflowSelection) error
	WorkItemOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) provideWorkflowGovernance(s *module.Services) error {
	if m.runtime != nil && m.runtime.Database() != nil && m.runtime.Database().Write() != nil {
		if err := m.installHumanEscape(m.runtime.Database().Write()); err != nil {
			return err
		}
	}
	return module.Provide[WorkflowTransitionConnector](s, WorkflowTransitionConnector{WorkItemOn: m.workItemReferenceOn, BindSelection: func(selectState WorkflowSelection) error {
		if selectState == nil || m.workflowSelection != nil {
			return ErrInvalidArgument
		}
		m.workflowSelection = selectState
		return nil
	}, Bind: func(check WorkflowTransitionCheck) error {
		if check == nil || m.runtime == nil {
			return ErrUnavailable
		}
		// Registry/OpenAPI-only composition has no writer and cannot execute a
		// domain command. The normal database-backed composition installs here.
		if m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
			return nil
		}
		return m.installWorkflow(m.runtime.Database().Write(), check)
	}})
}

// Observe the source-owned CHECK action after insertion in its existing command
// transaction. The independent Human check and its authorization are performed
// by checkResultOn; only exact-result prior Agent acceptance is an escape.
func (m *Module) installHumanEscape(db *gorm.DB) error {
	return db.Callback().Create().After("gorm:create").Register("delivery:human_quality_escape", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != m.service.names.ResultActions.String() {
			return
		}
		var actions []*model.ResultAction
		switch v := tx.Statement.Dest.(type) {
		case *model.ResultAction:
			actions = []*model.ResultAction{v}
		case []*model.ResultAction:
			actions = v
		case *[]*model.ResultAction:
			actions = *v
		default:
			_ = tx.AddError(ErrInvalidArgument)
			return
		}
		for _, a := range actions {
			if a == nil || a.Kind != "CHECK" || a.Verdict != "FAILED" {
				continue
			}
			if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
				_ = tx.AddError(ErrInvalidArgument)
				return
			}
			_ = tx.AddError(m.humanEscapeOn(tx.Statement.Context, tx.Session(&gorm.Session{NewDB: true, Initialized: true}), a))
			if tx.Error != nil {
				return
			}
		}
	})
}

func (m *Module) humanEscapeOn(ctx context.Context, tx *gorm.DB, action *model.ResultAction) error {
	q := query.BindVerificationRuns(tx, m.service.names)
	runs, err := q.WithContext(ctx).Where(q.TenantID.Eq(action.TenantID), q.ProjectID.Eq(action.ProjectID), q.WorkItemID.Eq(action.WorkItemID), q.ResultID.Eq(action.ResultID), q.State.Eq("DONE"), q.Verdict.Eq("PASSED"), q.Stage.In("AGENT_TEST", "AGENT_ACCEPTANCE")).Order(q.ID).Find()
	if err != nil {
		return err
	}
	passed := []map[string]any{}
	acceptance := false
	for _, r := range runs {
		if r.ReviewerID == nil || r.ReviewerFamily == "" || len(r.ReceiptHash) != 64 {
			continue
		}
		acceptance = acceptance || r.Stage == "AGENT_ACCEPTANCE"
		passed = append(passed, map[string]any{"stage": r.Stage, "principal_id": r.ReviewerID, "model_family": r.ReviewerFamily, "verifier_version": r.ReceiptHash})
	}
	if !acceptance {
		return nil
	}
	w := query.BindWorkItems(tx, m.service.names)
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(action.TenantID), w.ProjectID.Eq(action.ProjectID), w.ID.Eq(action.WorkItemID)).UpdateSimple(w.EscapedFromStage.Value("HUMAN_REVIEW"), w.EscapeCount.Add(1)); err != nil {
		return err
	}
	if err = m.recordReportQualityOn(ctx, tx, action.TenantID, action.ProjectID, action.WorkItemID, "ESCAPE_HUMAN", action.ID, 0, action.CreatedAt, action.ResultID); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: action.TenantID, ProjectID: action.ProjectID, Type: "quality.escaped", Actor: events.Actor{Kind: "HUMAN", ID: action.ActorID, AuthoritySource: action.AuthoritySource}, Subject: events.Subject{Kind: "work_item", ID: action.WorkItemID}, Data: map[string]any{"escape_key": action.WorkItemID.String() + ":" + action.ID.String(), "work_item_id": action.WorkItemID, "stage": "HUMAN_REVIEW", "category": "FUNCTIONAL_DEFECT", "summary": action.Reason, "passed_by": passed, "decision_ref": action.ID}})
	return err
}

func (m *Module) workItemReferenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	_, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return ErrProjectNotFound
	}
	return e
}

// All canonical state writers append a transition receipt. Guarding its insert
// covers human, Agent, system and owner-provider commands without replacing
// their authorization, lifecycle guards or atomic audit/outbox transactions.
func (m *Module) installWorkflow(db *gorm.DB, check WorkflowTransitionCheck) error {
	if db == nil || check == nil {
		return ErrUnavailable
	}
	const hook = "delivery:workflow_transition"
	if db.Callback().Create().Get(hook) != nil {
		return ErrInvalidArgument
	}
	return db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != m.service.names.WorkTransitions.String() {
			return
		}
		if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
			_ = tx.AddError(ErrInvalidArgument)
			return
		}
		var visit func(reflect.Value)
		visit = func(v reflect.Value) {
			if !v.IsValid() || tx.Error != nil {
				return
			}
			if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
				if !v.IsNil() {
					visit(v.Elem())
				}
				return
			}
			if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i))
				}
				return
			}
			r, ok := v.Interface().(model.WorkItemTransition)
			if !ok {
				_ = tx.AddError(ErrInvalidArgument)
				return
			}
			if r.FromState == nil || *r.FromState == r.ToState {
				return
			}
			read := tx.Session(&gorm.Session{NewDB: true})
			w := query.BindWorkItems(read, m.service.names)
			row, e := w.WithContext(tx.Statement.Context).Clauses(clause.Locking{Strength: "SHARE"}).Where(w.TenantID.Eq(r.TenantID), w.ProjectID.Eq(r.ProjectID), w.ID.Eq(r.WorkItemID)).First()
			if e != nil {
				_ = tx.AddError(e)
				return
			}
			actor := guid.ID(0)
			if r.ActorID != nil {
				actor = *r.ActorID
			}
			request := WorkflowTransition{TenantID: r.TenantID, ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, ActorID: actor, Type: row.Type, From: *r.FromState, To: r.ToState, ActorKind: r.ActorKind, Trigger: r.Trigger}
			if selection, ok := tx.Statement.Context.Value(workflowSelectionContextKey{}).(WorkflowTransition); ok && selection.WorkItemID == r.WorkItemID && selection.From == request.From && selection.To == request.To {
				request.FromKey, request.ToKey = selection.FromKey, selection.ToKey
			}
			request.FactOn = func(kind, rule, argument string) (bool, error) {
				return m.workflowFactOn(tx.Statement.Context, read, row, actor, kind, rule, argument)
			}
			_ = tx.AddError(check(tx.Statement.Context, read, request))
		}
		visit(reflect.ValueOf(tx.Statement.Dest))
	})
}

func (m *Module) workflowFactOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID, kind, rule, argument string) (bool, error) {
	// Authority discovery may have established a REPEATABLE READ snapshot
	// before the project lock. Conditions must observe committed current facts,
	// including the result projection's downstream queries.
	tx = tx.Clauses(clause.Locking{Strength: "SHARE"})
	switch kind {
	case "REQUIRED_FIELDS":
		switch rule {
		case "introduced_reason":
			return work.IntroducedReason != nil && *work.IntroducedReason != "", nil
		case "module":
			return work.Module != nil && *work.Module != "", nil
		case "estimate_minutes":
			return work.EstimateMinutes != nil && *work.EstimateMinutes > 0, nil
		case "introduced_stage":
			return work.IntroducedStage != nil && *work.IntroducedStage != "", nil
		case "fixed_version_id":
			return work.FixedVersion != nil && *work.FixedVersion != "", nil
		case "acceptance_criteria":
			q := query.BindWorkCriteria(tx, m.service.names)
			rows, e := q.WithContext(ctx).Select(q.ID).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Limit(1).Find()
			return len(rows) > 0, e
		case "assignee":
			q := query.BindAssignments(tx, m.service.names)
			rows, e := q.WithContext(ctx).Select(q.ID).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID), q.Status.Eq("ACTIVE")).Limit(1).Find()
			return len(rows) > 0, e
		}
	case "WORK_ITEM_ROLE":
		if rule == "ACCOUNTABLE" {
			return actor > 0 && work.AccountablePrincipalID == actor, nil
		}
		if rule == "REPORTER" {
			return actor > 0 && work.CreatedBy != nil && *work.CreatedBy == actor, nil
		}
		if rule == "EXECUTOR" {
			q := query.BindAssignments(tx, m.service.names)
			rows, e := q.WithContext(ctx).Select(q.ID).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID), q.AssigneePrincipalID.Eq(actor), q.Status.Eq("ACTIVE")).Limit(1).Find()
			return actor > 0 && len(rows) > 0, e
		}
	case "RELATED_OBJECTS":
		if rule == "HAS_PARENT" {
			return work.ParentID != nil, nil
		}
		if rule == "ALL_DEPENDENCIES_DONE" {
			q := query.BindDependencies(tx, m.service.names)
			deps, e := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Find()
			if e != nil {
				return false, e
			}
			w := query.BindWorkItems(tx, m.service.names)
			for _, d := range deps {
				r, e := w.WithContext(ctx).Where(w.TenantID.Eq(work.TenantID), w.ProjectID.Eq(work.ProjectID), w.ID.Eq(d.DependsOnID)).First()
				if e != nil {
					return false, e
				}
				if r.State != "ACCEPTED" {
					return false, nil
				}
			}
			return true, nil
		}
	case "EVIDENCE":
		if rule == "LINKED_MR_MERGED" || rule == "DEPLOYMENT_CONTAINS_MERGE" {
			if work.CurrentClaimID == nil {
				return false, nil
			}
			result, e := m.resultOn(ctx, tx, work.TenantID, work.ProjectID, work.ID, *work.CurrentClaimID)
			if e != nil {
				return false, e
			}
			if rule == "LINKED_MR_MERGED" {
				_, e = m.trustedDefectMergeOn(ctx, tx, work, result)
			} else {
				e = m.defectDeploymentReadyOn(ctx, tx, work, result)
			}
			if errors.Is(e, ErrVerificationPending) {
				return false, nil
			}
			return e == nil, e
		}
		if rule != "EVERY_AC_HAS_PASS_EVIDENCE" && rule != "INTERNAL_QA_PASSED" {
			return false, ErrUnavailable
		}
		if work.CurrentClaimID == nil {
			return false, nil
		}
		q := query.BindResultVersions(tx, m.service.names)
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID), q.ID.Eq(*work.CurrentClaimID)).First()
		if e != nil {
			return false, e
		}
		v, e := m.resultViewOn(ctx, tx, result)
		if e != nil {
			return false, e
		}
		if rule == "INTERNAL_QA_PASSED" {
			return v.Completion.L2, nil
		}
		if len(v.CriteriaMap) == 0 {
			return false, nil
		}
		for _, c := range v.CriteriaMap {
			if c.Coverage != "COVERED" || c.Verdict != "PASSED" {
				return false, nil
			}
		}
		return true, nil
	}
	// Unsupported sources stay unavailable; absence is never passing evidence.
	return false, ErrUnavailable
}

func (m *Module) bindDelegationProjects(s *module.Services) error {
	if err := m.bindEnsemble(s); err != nil {
		return err
	}
	c, e := module.Resolve[governance.DelegationProjectConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	if e = c.Bind(governance.DelegationProjectFacts{InspectOn: m.delegationProjectOn}); e != nil {
		return e
	}
	claims, e := module.Resolve[governance.ClaimEvaluationConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if claims.Bind == nil {
		return ErrUnavailable
	}
	if e = claims.Bind(governance.ClaimEvaluationSource{ReadOn: m.claimEvaluationOn}); e != nil {
		return e
	}
	costs, e := module.Resolve[governance.SpawnCostConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if costs.BindAssignment == nil {
		return ErrUnavailable
	}
	return costs.BindAssignment(m.spawnAssignmentOn)
}

func (m *Module) spawnAssignmentOn(ctx context.Context, tx *gorm.DB, tenant, project, assignment guid.ID) (guid.ID, bool, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || assignment <= 0 {
		return 0, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, false, ErrInvalidArgument
	}
	if m == nil || m.service == nil {
		return 0, false, ErrUnavailable
	}
	q := query.BindAssignments(tx, m.service.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(assignment)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if row.AgentID == nil {
		return 0, true, nil
	}
	return *row.AgentID, true, nil
}

// Read the exact persisted completion and current claim under the work fence.
// Human manual results have no self-confidence score; a missing runtime diff
// is handled explicitly by governance, never reconstructed from attachments.
func (m *Module) claimEvaluationOn(ctx context.Context, tx *gorm.DB, tenant, project, work, claim guid.ID) (governance.ClaimEvaluationFacts, bool, error) {
	var empty governance.ClaimEvaluationFacts
	if m == nil || m.service == nil {
		return empty, false, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || claim <= 0 {
		return empty, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	row, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(work), w.ArchivedAt.IsNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, false, nil
	}
	if e != nil {
		return empty, false, e
	}
	if row.CurrentClaimID == nil || *row.CurrentClaimID != claim {
		return empty, false, nil
	}
	r := query.BindResultVersions(tx, m.service.names)
	_, e = r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.WorkItemID.Eq(work), r.ID.Eq(claim)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, false, nil
	}
	if e != nil {
		return empty, false, e
	}
	return governance.ClaimEvaluationFacts{ClaimID: claim, WorkItemID: work, SelfConfidence: row.SelfConfidence}, true, nil
}

// The caller holds participating principal locks and current grant authority.
// Project locking makes grants serialize with terminal/mode/budget changes.
func (m *Module) delegationProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (governance.DelegationProjectState, error) {
	var out governance.DelegationProjectState
	if m == nil || m.service == nil {
		return out, ErrUnavailable
	}
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	q := query.BindProjects(tx, m.service.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, governance.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	var holds []json.RawMessage
	if json.Unmarshal([]byte(r.Holds), &holds) != nil {
		return out, ErrUnavailable
	}
	out = governance.DelegationProjectState{Stage: r.Stage, Mode: r.TeamMode, DelegatedAllowed: r.TeamMode == "AUTONOMOUS" && len(holds) == 0 && r.BudgetState == "NORMAL" && r.Stage != "CLOSED" && r.Stage != "CANCELLED"} //nolint:misspell // Frozen state.
	return out, nil
}

func (m *Module) bindEnsemble(s *module.Services) error {
	c, e := module.Resolve[fleet.EnsembleConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.BindWork == nil {
		return ErrUnavailable
	}
	if c.BindRuntime != nil {
		if e = m.bindEnsembleRuntime(s, c); e != nil {
			return e
		}
	}
	return c.BindWork(m.ensembleWorkOn)
}
func (m *Module) ensembleWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, permission string) error {
	if m == nil || m.service == nil || tx == nil || authn.TenantIDFromContext(ctx) != tenant {
		return fleet.ErrMachineForbidden
	}
	if permission != "work_item_read" && permission != "work_item_assign" {
		return fleet.ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return fleet.ErrMachineNotFound
	}
	if e != nil {
		return e
	}
	_, _, _, e = m.lockedWorkOn(ctx, tx, tenant, project, work, authn.PrincipalIDFromContext(ctx), row.AccountablePrincipalID, permission)
	if errors.Is(e, ErrWorkForbidden) || errors.Is(e, ErrInvalidMember) || errors.Is(e, ErrWorkProjectReadOnly) {
		return fleet.ErrMachineForbidden
	}
	if errors.Is(e, ErrProjectNotFound) {
		return fleet.ErrMachineNotFound
	}
	return e
}
