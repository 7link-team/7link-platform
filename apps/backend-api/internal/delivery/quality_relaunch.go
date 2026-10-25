package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

type QualityLaunchRequest struct {
	TenantID, ProjectID, WorkItemID, ParentExecutionID, SourceDecisionID guid.ID
	HeadCommit, Feedback                                                 string
}

type qualityDecision struct {
	Action            string  `json:"action"`
	ParentExecutionID guid.ID `json:"parent_execution_id"`
	HeadCommit        string  `json:"head_commit"`
	Round             int     `json:"round"`
}

type qualityFailure struct {
	SourceDecisionID guid.ID `json:"source_decision_id"`
	VerificationID   guid.ID `json:"verification_id"`
	CriterionID      guid.ID `json:"criterion_id"`
	Category         string  `json:"category"`
	Location         string  `json:"location"`
	Expected         string  `json:"expected"`
	Detail           string  `json:"detail"`
	ReceiptHash      string  `json:"receipt_hash"`
}

func qualityFailureFact(action guid.ID, run *model.VerificationRun) qualityFailure {
	detail := []rune(run.Reason)
	if len(detail) > 500 {
		detail = detail[:500]
	}
	return qualityFailure{SourceDecisionID: action, VerificationID: run.ID, CriterionID: run.CriterionID, Category: "FUNCTIONAL_DEFECT", Location: run.Method + ":" + run.Stage, Expected: "PASSED", Detail: string(detail), ReceiptHash: run.ReceiptHash}
}

func qualitySnapshot(decision qualityDecision, failures []qualityFailure) string {
	raw, _ := json.Marshal(struct {
		qualityDecision
		Failures []qualityFailure `json:"feedback"`
	}{decision, failures})
	return string(raw)
}

// Collect participants before project/work locks, including the source machine.
// Merely gathering IDs grants no authority; the launcher revalidates every fact.
func (m *Module) prelockQualityOn(ctx context.Context, tx *gorm.DB, tenant, project, work, result, actor, owner guid.ID) error {
	if m.verificationExecutor.QualityPrincipalsOn == nil {
		return nil
	}
	row, err := m.resultOn(ctx, tx, tenant, project, work, result)
	if err != nil {
		return err
	}
	if row.SourceExecutionID == nil {
		return nil
	}
	ids, err := m.verificationExecutor.QualityPrincipalsOn(ctx, tx, tenant, *row.SourceExecutionID)
	if err != nil {
		return err
	}
	if err = m.lockStagePrincipalsOn(ctx, tx, actor, append(ids, owner)); err != nil {
		return err
	}
	return m.qualityIAMFenceOn(ctx, tx, tenant, project, work)
}

func (m *Module) qualityIAMFenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) error {
	a := query.BindAssignments(tx, m.service.names)
	holder, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.WorkItemID.Eq(work), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.AssigneeKind.Eq("AGENT")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = m.workPolicy(ctx, tx, tenant, project, "work_item_execute", holder.AssigneePrincipalID)
	return err
}

// A failed internal round is a SYSTEM return action plus structured feedback.
// The child is created synchronously, so any launch failure rolls back the
// verdict/action/feedback together; the durable completion event retries it.
func (m *Module) relaunchQualityOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, runs []*model.VerificationRun, now time.Time) error {
	if row.SourceExecutionID == nil || m.verificationExecutor.LaunchQualityOn == nil {
		return ErrUnavailable
	}
	decision := qualityDecision{Action: "REQUEST_CHANGES", ParentExecutionID: *row.SourceExecutionID, HeadCommit: row.HeadCommit, Round: work.InternalRoundCount}
	reason, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	source := "SYSTEM:verification"
	for _, run := range runs {
		if run.Round == decision.Round && run.Verdict == "FAILED" && (run.Stage == "AGENT_TEST" || run.Stage == "AGENT_ACCEPTANCE") {
			source = "SYSTEM:internal_qa"
		}
	}
	action, err := m.appendResultActionOn(ctx, tx, work, row, 0, "RETURN", "FAILED", string(reason), source, nil, now)
	if err != nil {
		return err
	}
	failures := []qualityFailure{}
	// Stable order freezes the same snapshot for authorization and replay.
	slices.SortFunc(runs, func(a, b *model.VerificationRun) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for _, run := range runs {
		if run.Round != decision.Round || run.Method == "HUMAN" || run.State != "DONE" || run.Verdict != "FAILED" {
			continue
		}
		if len(run.ReceiptHash) != 64 {
			return ErrInvalidArgument
		}
		fact := qualityFailureFact(action.ID, run)
		detail, e := json.Marshal(fact)
		if e != nil {
			return e
		}
		id, e := m.ids.Next()
		if e != nil {
			return e
		}
		if e = query.BindResultFeedback(tx, m.service.names).WithContext(ctx).Create(&model.ResultFeedback{TenantID: row.TenantID, ID: guid.ID(id), ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ResultVersionID: row.ID, Kind: "FEEDBACK", Category: "QUALITY_REJECTION", Detail: string(detail), CreatedAt: now}); e != nil {
			return e
		}
		failures = append(failures, fact)
	}
	if len(failures) == 0 {
		return ErrInvalidArgument
	}
	// Save REWORK before delegating CREATE; its authorization re-reads the work.
	w := query.BindWorkItems(tx, m.service.names)
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(work.TenantID), w.ID.Eq(work.ID), w.Version.Eq(work.Version)).UpdateSimple(w.State.Value("REWORK"), w.InternalRoundCount.Value(work.InternalRoundCount)); err != nil {
		return err
	}
	system := authn.WithClaims(ctx, &authn.Claims{TenantID: row.TenantID})
	id, err := m.verificationExecutor.LaunchQualityOn(system, tx, QualityLaunchRequest{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, ParentExecutionID: *row.SourceExecutionID, SourceDecisionID: action.ID, HeadCommit: row.HeadCommit, Feedback: qualitySnapshot(decision, failures)})
	if err != nil {
		return err
	}
	if id <= 0 {
		return ErrUnavailable
	}
	work.CurrentExecutionID = &id
	event := audit.NewDomainEvent(system, row.TenantID, "delivery.quality_relaunched", "result", row.ID)
	event.Detail["decision_id"], event.Detail["branch_execution_id"] = action.ID.String(), id.String()
	event.Detail["authority_source"], event.Detail["visibility"] = source, "INTERNAL"
	if err = m.service.audit(system, tx, event); err != nil {
		return err
	}
	_, err = m.service.events.Publish(system, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "result.feedback_added", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: source}, Subject: events.Subject{Kind: "result", ID: row.ID}, Data: map[string]any{"decision_id": action.ID.String(), "branch_execution_id": id.String(), "kind": "FEEDBACK", "category": "QUALITY_REJECTION", "visibility": "INTERNAL"}})
	return err
}

// SYSTEM authority is an actual immutable source action, not an actor name.
// It remains required on CREATE, DISPATCH and RECEIPT for the entire child.
func (m *Module) executionAuthorityOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, system bool, operation string) (authz.DataConstraint, error) {
	if l.ReviewPlanID > 0 {
		return m.plannedImplementationAuthorityOn(ctx, tx, l, operation)
	}
	if !system {
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, l.InitiatorID)
		if err != nil {
			return authz.DataConstraint{}, err
		}
		if !active || human.ID != l.InitiatorID {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, l.InitiatorID)
		if err != nil {
			return authz.DataConstraint{}, err
		}
		if !member {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		return m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "work_item_execute", l.InitiatorID)
	}
	if !slices.Contains([]string{"CREATE", "DISPATCH", "RECEIPT", "START", "RECOVER"}, operation) || (operation == "CREATE" && l.ActorID != 0) || (operation != "CREATE" && l.ActorID <= 0) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	if operation == "RECOVER" {
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, l.ActorID)
		if err != nil {
			return authz.DataConstraint{}, err
		}
		if !active || human.ID != l.ActorID {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, l.ActorID)
		if err != nil {
			return authz.DataConstraint{}, err
		}
		if !member {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		operator, err := m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "execution_stop", l.ActorID)
		if err != nil {
			return authz.DataConstraint{}, err
		}
		if !operator.AllowsEntityRows(l.ProjectID) {
			return authz.DataConstraint{}, ErrWorkForbidden
		}
		if !operator.AllowAll && !slices.Contains(operator.ResourceIDs, l.ProjectID.String()) {
			w := query.BindWorkItems(tx, m.service.names)
			owner, err := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID)).First()
			if err != nil {
				return authz.DataConstraint{}, err
			}
			if owner.CreatedBy == nil || !slices.Contains(operator.OwnerIDs, owner.CreatedBy.String()) {
				return authz.DataConstraint{}, ErrWorkForbidden
			}
		}
	}
	a := query.BindResultActions(tx, m.service.names)
	action, err := a.WithContext(ctx).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.SourceDecisionID), a.ResultID.Eq(l.ParentExecutionID), a.ActorID.Eq(guid.ID(0)), a.Kind.Eq("RETURN"), a.Verdict.Eq("FAILED"), a.AuthoritySource.In("SYSTEM:verification", "SYSTEM:internal_qa")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	if err != nil {
		return authz.DataConstraint{}, err
	}
	row, err := m.resultOn(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, action.ResultID)
	if err != nil {
		return authz.DataConstraint{}, err
	}
	var decision qualityDecision
	if json.Unmarshal([]byte(action.Reason), &decision) != nil || decision.Action != "REQUEST_CHANGES" || decision.ParentExecutionID != l.ParentExecutionID || decision.HeadCommit != row.HeadCommit || row.SourceExecutionID == nil || *row.SourceExecutionID != l.ParentExecutionID || decision.Round <= 0 {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return authz.DataConstraint{}, err
	}
	slices.SortFunc(runs, func(a, b *model.VerificationRun) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	failures := []qualityFailure{}
	for _, run := range runs {
		if run.Round == decision.Round && run.Method != "HUMAN" && run.State == "DONE" && run.Verdict == "FAILED" && len(run.ReceiptHash) == 64 {
			failures = append(failures, qualityFailureFact(action.ID, run))
		}
	}
	if len(failures) == 0 || operation == "CREATE" && (l.SourceHeadCommit != row.HeadCommit || l.SourceFeedback != qualitySnapshot(decision, failures)) {
		return authz.DataConstraint{}, ErrWorkForbidden
	}
	assign := query.BindAssignments(tx, m.service.names)
	holder, err := assign.WithContext(ctx).Where(assign.TenantID.Eq(l.TenantID), assign.ProjectID.Eq(l.ProjectID), assign.WorkItemID.Eq(l.WorkItemID), assign.ID.Eq(l.AssignmentID), assign.AssigneeKind.Eq("AGENT"), assign.Status.Eq("ACTIVE")).First()
	if err != nil {
		return authz.DataConstraint{}, fmt.Errorf("quality assignment: %w", err)
	}
	return m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "work_item_execute", holder.AssigneePrincipalID)
}
