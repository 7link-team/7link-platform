package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

var ErrQueueFull = fmt.Errorf("execution: quality queue full: %w", fleet.ErrSlotsFull)

// QualityLauncher starts a SYSTEM continuation synchronously in delivery's
// transaction. Delivery must prelock PrincipalsOn before any business rows.
type QualityLauncher struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	LaunchOn     func(context.Context, *gorm.DB, QualityLaunchRequest) (guid.ID, error)
}
type QualityLaunchRequest struct {
	TenantID, ProjectID, WorkItemID, ParentExecutionID, SourceDecisionID guid.ID
	HeadCommit, Feedback                                                 string
}

func (m *Module) provideQualityLauncher(s *module.Services) error {
	return module.Provide[QualityLauncher](s, QualityLauncher{PrincipalsOn: m.qualityPrincipalsOn, LaunchOn: m.launchQualityOn})
}
func (m *Module) qualityPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, parent guid.ID) ([]guid.ID, error) {
	row, err := m.rowOn(ctx, tx, tenant, parent, false)
	if err != nil {
		return nil, err
	}
	if m.machines.ByIDOn == nil || m.work.PrincipalsOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, row.MachineID)
	if err != nil {
		return nil, err
	}
	ids, err := m.work.PrincipalsOn(ctx, tx, link(row, 0))
	if err != nil {
		return nil, err
	}
	ids = append(ids, machine.PrincipalID)
	if row.InitiatorID > 0 {
		ids = append(ids, row.InitiatorID)
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrForbidden
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
func (m *Module) launchQualityOn(ctx context.Context, tx *gorm.DB, in QualityLaunchRequest) (guid.ID, error) {
	if transaction(tx, in.TenantID, in.ProjectID) != nil || in.WorkItemID <= 0 || in.ParentExecutionID <= 0 || in.SourceDecisionID <= 0 || !commitHash(in.HeadCommit) || !utf8.ValidString(in.Feedback) || strings.TrimSpace(in.Feedback) == "" || len(in.Feedback) > 20000 {
		return 0, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != in.TenantID || authn.PrincipalIDFromContext(ctx) != 0 {
		return 0, ErrForbidden
	}
	if m.work.AuthorizeOn == nil || m.machines.LockOn == nil || m.machines.ResolveWorkspaceOn == nil || m.machines.ReserveSlotOn == nil {
		return 0, ErrCapabilityUnavailable
	}
	parent, err := m.rowOn(ctx, tx, in.TenantID, in.ParentExecutionID, false)
	if err != nil {
		return 0, err
	}
	if parent.ProjectID != in.ProjectID || parent.WorkItemID != in.WorkItemID || parent.State != "SUCCEEDED" {
		return 0, ErrConflict
	}
	v, err := view(parent)
	if err != nil {
		return 0, err
	}
	// Source action, exact successful parent result/head, current assignment and
	// SYSTEM authority are all revalidated by the owning work aggregate.
	row := &model.Execution{TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: parent.AssignmentID, MachineID: parent.MachineID, ParentExecutionID: &in.ParentExecutionID, SourceDecisionID: &in.SourceDecisionID, BranchReason: "QUALITY", StartPoint: "CONTINUE", FeedbackSnapshot: in.Feedback, Autostart: true, State: "CREATED", ResultState: "NONE", DispatchEpoch: 1, Version: 1}
	q := query.BindExecutions(tx, m.names)
	prior, priorErr := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.SourceDecisionID.Eq(in.SourceDecisionID)).First()
	if priorErr != nil && !errors.Is(priorErr, gorm.ErrRecordNotFound) {
		return 0, priorErr
	}
	if priorErr == nil {
		row.ID = prior.ID
	} else {
		row.ID, err = m.nextID()
		if err != nil {
			return 0, err
		}
	}
	sourceLink := link(row, 0)
	sourceLink.SourceHeadCommit, sourceLink.SourceFeedback = in.HeadCommit, in.Feedback
	if err = m.work.AuthorizeOn(ctx, tx, sourceLink, "CREATE"); err != nil {
		return 0, err
	}
	if priorErr == nil {
		if prior.ParentExecutionID == nil || *prior.ParentExecutionID != in.ParentExecutionID || prior.ProjectID != in.ProjectID || prior.WorkItemID != in.WorkItemID || prior.FeedbackSnapshot != in.Feedback {
			return 0, ErrConflict
		}
		old, e := view(prior)
		if e != nil {
			return 0, e
		}
		if old.Plan.BaseCommit != in.HeadCommit {
			return 0, ErrConflict
		}
		return prior.ID, nil
	}
	plan := v.Plan
	plan.Correlation, err = newCorrelation(ctx, v.Plan.Correlation)
	if err != nil {
		return 0, err
	}
	plan.BaseCommit = in.HeadCommit
	plan.QualityFeedback = in.Feedback
	plan.CommentFeedback = ""
	if m.commentFeedback != nil {
		plan.CommentFeedback, err = m.commentFeedback(executorContext(ctx, row.TenantID, plan.ExecutorPrincipalID), tx, row.TenantID, row.ProjectID, row.WorkItemID, plan.ExecutorPrincipalID)
		if err != nil {
			return 0, err
		}
	}
	if err = m.pinExecutionIdentityOn(ctx, tx, link(row, 0), &plan); err != nil {
		return 0, err
	}
	if plan.ExecutorPrincipalID <= 0 || plan.ExecutorPrincipalID != v.Plan.ExecutorPrincipalID || plan.ModelFamily != v.Plan.ModelFamily {
		return 0, ErrConflict
	}
	if err = m.validateAgentBindingOn(ctx, tx, link(row, 0), row.MachineID, plan); err != nil {
		return 0, err
	}
	if m.knowledge.context.BuildInjectionOn != nil {
		plan.ContextRefs = nil
	}
	if err = m.pinKnowledgeOn(executorContext(ctx, row.TenantID, plan.ExecutorPrincipalID), tx, row, &plan); err != nil {
		return 0, err
	}
	if err = m.budgetOn(ctx, tx, in.TenantID, link(row, 0), &plan); err != nil {
		return 0, err
	}
	workspace, err := m.machines.ResolveWorkspaceOn(ctx, tx, in.TenantID, row.MachineID, in.ProjectID, plan.RepositoryID)
	if err != nil {
		return 0, err
	}
	plan.WorkdirID, plan.Workdir = workspace.ID, workspace.Path
	encoded, err := json.Marshal(plan)
	if err != nil {
		return 0, err
	}
	if len(encoded) > 65535 {
		return 0, ErrInvalidArgument
	}
	row.PlanJSON, row.PlanHash = string(encoded), hash(encoded)
	machine, err := m.machines.LockOn(ctx, tx, in.TenantID, row.MachineID)
	if err != nil {
		return 0, err
	}
	count, err := q.WithContext(ctx).Where(q.TenantID.Eq(in.TenantID), q.MachineID.Eq(row.MachineID), q.State.In("CREATED", "RUNNING")).Count()
	if err != nil {
		return 0, err
	}
	if machine.SlotCount <= 0 || count >= int64(machine.SlotCount) {
		return 0, ErrQueueFull
	}
	if _, err = m.machines.ReserveSlotOn(ctx, tx, in.TenantID, row.MachineID, row.ID); err != nil {
		return 0, err
	}
	if err = m.workspaceFreeOn(ctx, tx, in.TenantID, row.MachineID, row.ID, workspace.ID); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row.CreatedAt, row.UpdatedAt = now, now
	if err = q.WithContext(ctx).Create(row); err != nil {
		return 0, err
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "SYSTEM", "SYSTEM:verification", map[string]any{"state": "CREATED", "plan_hash": row.PlanHash, "parent_execution_id": in.ParentExecutionID.String(), "source_decision_id": in.SourceDecisionID.String(), "branch_reason": "QUALITY"}); err != nil {
		return 0, err
	}
	return row.ID, nil
}
