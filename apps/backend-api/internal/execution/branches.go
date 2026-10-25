package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"gorm.io/gorm"
)

type StrategyBranch struct {
	Reason    string  `json:"reason" minLength:"1" maxLength:"2000"`
	Objective string  `json:"objective" minLength:"1" maxLength:"20000"`
	MachineID guid.ID `json:"machine_id,omitempty"`
}
type strategySeed struct {
	parent  guid.ID
	version int
	reason  string
}

// strategyAuthorityOn also runs before idempotency lookup: revocation, project
// authority is checked before lookup; the returned branch is reauthorized
// against current assignment/holds/plan approval after lookup.
func (m *Module) strategyAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.Execution, error) {
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	if m.machines.ByIDOn == nil || m.work.AuthorizeOn == nil {
		return nil, ErrUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, r.MachineID)
	if err != nil {
		return nil, err
	}
	if err = m.workPrincipalLocks(ctx, tx, link(r, authn.PrincipalIDFromContext(ctx)), authn.PrincipalIDFromContext(ctx), r.InitiatorID, machine.PrincipalID); err != nil {
		return nil, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, r.ProjectID, "execution_start"); err != nil {
		return nil, err
	}
	return r, nil
}

// BranchStrategyOn creates a separate command. It never recovers, cancels or
// changes the source. Root baseline and ancestry are frozen under the work lock.
func (m *Module) BranchStrategyOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, in StrategyBranch) (ExecutionView, error) {
	if !bounded(in.Reason, 2000) || in.MachineID < 0 || version < 1 {
		return ExecutionView{}, ErrInvalidArgument
	}
	// Read immutable input only; createOn acquires all principals before business locks.
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return ExecutionView{}, err
	}
	v, err := view(r)
	if err != nil {
		return ExecutionView{}, err
	}
	machine := in.MachineID
	if machine == 0 {
		machine = r.MachineID
	}
	plan := v.Plan
	plan.Objective = in.Objective
	plan.ContextRefs = nil
	plan.Knowledge = nil
	out, err := m.createOn(ctx, tx, tenant, ExecutionCreate{strategy: &strategySeed{parent: id, version: version, reason: in.Reason}, ProjectID: r.ProjectID, WorkItemID: r.WorkItemID, AssignmentID: r.AssignmentID, MachineID: machine, Plan: plan})
	if err != nil {
		return out, err
	}
	row, err := m.rowOn(ctx, tx, tenant, out.ID, false)
	if err != nil {
		return out, err
	}
	err = m.eventOn(ctx, tx, row, "execution.branch_created", "HUMAN", "permission:execution_start", map[string]any{"parent_execution_id": id.String(), "branch_reason": "STRATEGY", "start_point": "BASELINE", "reason": in.Reason, "base_commit": out.Plan.BaseCommit, "plan_hash": out.PlanHash})
	return out, err
}

func (m *Module) freezeStrategyOn(ctx context.Context, tx *gorm.DB, row *model.Execution, plan *ExecutionPlan, seed strategySeed) error {
	parent, err := m.rowOn(ctx, tx, row.TenantID, seed.parent, true)
	if err != nil {
		return err
	}
	if parent.Version != seed.version || parent.ProjectID != row.ProjectID || parent.WorkItemID != row.WorkItemID || parent.AssignmentID != row.AssignmentID || parent.BranchReason == "ENSEMBLE" || parent.BranchReason == "SKILL_DISTILLATION" {
		return ErrConflict
	}
	root := parent
	seen := map[guid.ID]bool{}
	for {
		if seen[root.ID] || len(seen) >= 1000 {
			return ErrConflict
		}
		seen[root.ID] = true
		if root.ProjectID != row.ProjectID || root.WorkItemID != row.WorkItemID {
			return ErrConflict
		}
		if branchParent(root) == nil {
			break
		}
		root, err = m.rowOn(ctx, tx, row.TenantID, *branchParent(root), false)
		if err != nil {
			return err
		}
	}
	v, err := view(root)
	if err != nil {
		return err
	}
	if !commitHash(v.Plan.BaseCommit) || v.Plan.RepositoryID != plan.RepositoryID {
		return ErrConflict
	}
	plan.BaseCommit = v.Plan.BaseCommit
	row.StrategyParentID = &seed.parent
	row.BranchReason = "STRATEGY"
	row.StartPoint = "BASELINE"
	row.FeedbackSnapshot = seed.reason
	return nil
}

// BranchHistoryOn is scoped to the source's work item before pagination. It
// returns source records only; an ancestor outside this page is not a new root.
func (m *Module) BranchHistoryOn(ctx context.Context, tx *gorm.DB, tenant, id, cursor guid.ID, limit int) (ExecutionPage, error) {
	out := ExecutionPage{Items: []ExecutionView{}}
	if cursor < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	r, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return out, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, r.ProjectID, "execution_read"); err != nil {
		return out, err
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(r.ProjectID), q.WorkItemID.Eq(r.WorkItemID), q.ID.Gt(cursor)).Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	for _, r := range rows {
		v, e := view(r)
		if e != nil {
			return out, e
		}
		if e = m.authorizeKnowledgeViewOn(ctx, tx, tenant, v); e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if out.HasMore {
		out.NextCursor = rows[len(rows)-1].ID.String()
	}
	return out, nil
}

func branchParent(row *model.Execution) *guid.ID {
	if row.StrategyParentID != nil {
		return row.StrategyParentID
	}
	return row.ParentExecutionID
}
