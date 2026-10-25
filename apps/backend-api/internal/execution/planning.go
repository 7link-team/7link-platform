package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// PlanningPlan is accepted only from the automatic assignment connector. Its
// frozen binding is echoed in the actual model report and checked by Delivery.
type PlanningPlan struct {
	Binding       *delivery.ReviewPlanBinding  `json:"binding"`
	Criteria      []delivery.PlanningCriterion `json:"criteria"`
	DefaultBranch string                       `json:"default_branch"`
	Review        *PlanningReview              `json:"review,omitempty"`
	Feedback      string                       `json:"feedback,omitempty"`
}
type PlanningReview struct {
	Content           *delivery.ReviewPlanContent `json:"content"`
	SourceExecutionID guid.ID                     `json:"source_execution_id"`
	PlanID            guid.ID                     `json:"plan_id"`
	Version           int                         `json:"version"`
	ContentHash       string                      `json:"content_hash"`
}

func (m *Module) launchPlanningReviewOn(ctx context.Context, tx *gorm.DB, tenant, planID, reviewer guid.ID) (guid.ID, error) {
	if m.planning.AuthorizeReviewOn == nil {
		return 0, ErrCapabilityUnavailable
	}
	plan, e := m.planning.AuthorizeReviewOn(ctx, tx, tenant, planID, reviewer, false)
	if e != nil {
		return 0, e
	}
	if plan.Content.Producer == nil {
		return 0, ErrInvalidArgument
	}
	source, e := m.rowOn(ctx, tx, tenant, plan.Content.Producer.ExecutionID, false)
	if e != nil {
		return 0, e
	}
	v, e := view(source)
	if e != nil {
		return 0, e
	}
	a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, reviewer)
	if e != nil {
		return 0, e
	}
	if !found || a.State != "ACTIVE" || !slices.Contains(a.Roles, "REVIEWER") || a.ModelFamily == v.Plan.ModelFamily {
		return 0, ErrForbidden
	}
	runtime, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, reviewer)
	if e != nil {
		return 0, e
	}
	if !found || runtime.MachineID == nil || runtime.MachineState != "ONLINE" || runtime.RuntimeKind != a.RuntimeKind {
		return 0, nil
	}
	workspace, e := m.machines.ResolveWorkspaceOn(ctx, tx, tenant, *runtime.MachineID, source.ProjectID, v.Plan.RepositoryID)
	if errors.Is(e, fleet.ErrMachineNotFound) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	p := v.Plan
	p.ExecutorPrincipalID = reviewer
	p.Model, p.ModelFamily, p.Runtime, p.AgentSystemPrompt = a.ModelID, a.ModelFamily, a.RuntimeKind, a.SystemPrompt
	p.Workdir, p.WorkdirID = workspace.Path, workspace.ID
	p.BaseCommit = plan.Content.Producer.HeadCommit
	p.AuthorizationMode = "READ_ONLY"
	p.Artifacts = nil
	p.Knowledge = nil
	p.ContextRefs = nil
	p.Planning = &PlanningPlan{Binding: plan.Content.Binding, Criteria: v.Plan.Planning.Criteria, DefaultBranch: v.Plan.Planning.DefaultBranch, Review: &PlanningReview{Content: &plan.Content, SourceExecutionID: source.ID, PlanID: plan.ID, Version: plan.Version, ContentHash: plan.ContentHash}}
	p.Correlation, e = newCorrelation(ctx, v.Plan.Correlation)
	if e != nil {
		return 0, e
	}
	id, e := m.nextID()
	if e != nil {
		return 0, e
	}
	if e = m.budgetOn(ctx, tx, tenant, WorkLink{TenantID: tenant, ProjectID: source.ProjectID, WorkItemID: source.WorkItemID, AssignmentID: source.AssignmentID, ExecutionID: id}, &p); e != nil {
		return 0, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return 0, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := model.Execution{ID: id, TenantID: tenant, ProjectID: source.ProjectID, WorkItemID: source.WorkItemID, AssignmentID: source.AssignmentID, MachineID: *runtime.MachineID, InitiatorID: 0, BranchReason: "PLAN_REVIEW", Autostart: true, State: "CREATED", StateReason: "PLAN_REVIEW", ResultState: "NONE", PlanJSON: string(raw), PlanHash: hash(raw), DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, e = m.machines.ReserveSlotOn(ctx, tx, tenant, row.MachineID, id); e != nil {
		return 0, e
	}
	if e = m.workspaceFreeOn(ctx, tx, row.TenantID, row.MachineID, row.ID, p.WorkdirID); e != nil {
		return 0, e
	}
	if e = query.BindExecutions(tx, m.names).WithContext(ctx).Create(&row); e != nil {
		return 0, e
	}
	if e = m.eventOn(ctx, tx, &row, "execution.state_changed", "SYSTEM", "SYSTEM:plan_review", map[string]any{"state": "CREATED", "purpose": "PLAN_REVIEW", "plan_id": planID}); e != nil {
		return 0, e
	}
	return id, nil
}

func (m *Module) bindPlanning(s *module.Services) error {
	c, e := module.Resolve[delivery.PlanningWork](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if c.Bind == nil || c.AuthorizeOn == nil || c.CompleteOn == nil {
		return ErrCapabilityUnavailable
	}
	m.planning = c
	return c.Bind(delivery.PlanningRuntime{PrincipalsOn: m.planningPrincipalsOn, LaunchOn: m.launchPlanningOn, ContinueOn: m.continuePlanningOn, ReviewOn: m.launchPlanningReviewOn})
}
func (m *Module) planningPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]guid.ID, error) {
	if m.agentRuntime.ByPrincipalOn == nil || m.machines.ByIDOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	runtime, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, agent)
	if e != nil {
		return nil, e
	}
	if !found || runtime.MachineID == nil {
		return nil, ErrUnavailable
	}
	machine, e := m.machines.ByIDOn(ctx, tx, tenant, *runtime.MachineID)
	if e != nil {
		return nil, e
	}
	ids := []guid.ID{agent, machine.PrincipalID}
	if m.agents.ActiveByRoleOn != nil {
		reviewers, e := m.agents.ActiveByRoleOn(ctx, tx, tenant, "REVIEWER")
		if e != nil {
			return nil, e
		}
		for _, reviewer := range reviewers {
			ids = append(ids, reviewer.PrincipalID)
			binding, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, tenant, reviewer.PrincipalID)
			if e != nil {
				return nil, e
			}
			if !found || binding.MachineID == nil {
				continue
			}
			machine, e := m.machines.ByIDOn(ctx, tx, tenant, *binding.MachineID)
			if e != nil {
				return nil, e
			}
			ids = append(ids, machine.PrincipalID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (m *Module) launchPlanningOn(ctx context.Context, tx *gorm.DB, in delivery.PlanningLaunch) (guid.ID, error) {
	if transaction(tx, in.TenantID, in.ProjectID) != nil || in.AgentPrincipalID <= 0 || m.planning.AuthorizeOn == nil || m.agents.ByPrincipalOn == nil || m.agentRuntime.ByPrincipalOn == nil {
		return 0, ErrCapabilityUnavailable
	}
	agent, found, e := m.agents.ByPrincipalOn(ctx, tx, in.TenantID, in.AgentPrincipalID)
	if e != nil {
		return 0, e
	}
	if !found || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "EXECUTOR") || agent.ModelFamily == "" {
		return 0, ErrForbidden
	}
	runtime, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, in.TenantID, in.AgentPrincipalID)
	if e != nil {
		return 0, e
	}
	if !found || runtime.MachineID == nil || runtime.MachineState != "ONLINE" || runtime.RuntimeKind != agent.RuntimeKind {
		return 0, ErrUnavailable
	}
	id, e := m.nextID()
	if e != nil {
		return 0, e
	}
	l := delivery.ExecutionWorkLink{TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: in.AssignmentID, ExecutionID: id, ActorID: authn.PrincipalIDFromContext(ctx)}
	facts, e := m.planning.AuthorizeOn(ctx, tx, l, "CREATE")
	if e != nil {
		return 0, e
	}
	if facts.AgentPrincipalID != in.AgentPrincipalID {
		return 0, ErrConflict
	}
	workspace, e := m.machines.ResolveWorkspaceOn(ctx, tx, in.TenantID, *runtime.MachineID, in.ProjectID, facts.RepositoryID)
	if e != nil {
		return 0, e
	}
	p := ExecutionPlan{Planning: &PlanningPlan{Binding: facts.Binding, Criteria: facts.Criteria, DefaultBranch: facts.DefaultBranch, Feedback: in.Objective}, Runtime: agent.RuntimeKind, Model: agent.ModelID, ModelFamily: agent.ModelFamily, ExecutorPrincipalID: agent.PrincipalID, AgentSystemPrompt: agent.SystemPrompt, Workdir: workspace.Path, WorkdirID: workspace.ID, RepositoryID: facts.RepositoryID, AuthorizationMode: "READ_ONLY", MaxInputTokens: 32000, MaxOutputTokens: 8000, Objective: facts.Objective}
	if strings.TrimSpace(p.Objective) == "" {
		p.Objective = "Plan the assigned work against its frozen criteria."
	}
	if in.Objective == facts.Objective {
		p.Planning.Feedback = ""
	}
	if len(p.Objective) > 20000 {
		return 0, ErrInvalidArgument
	}
	p.Correlation, e = newCorrelation(ctx, nil)
	if e != nil {
		return 0, e
	}
	if e = m.budgetOn(ctx, tx, in.TenantID, WorkLink{TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: in.AssignmentID, ExecutionID: id}, &p); e != nil {
		return 0, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return 0, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := model.Execution{ID: id, TenantID: in.TenantID, ProjectID: in.ProjectID, WorkItemID: in.WorkItemID, AssignmentID: in.AssignmentID, MachineID: *runtime.MachineID, InitiatorID: 0, BranchReason: "PLANNING", Autostart: true, State: "CREATED", StateReason: "PLANNING", ResultState: "NONE", PlanJSON: string(raw), PlanHash: hash(raw), DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	if _, e = m.machines.ReserveSlotOn(ctx, tx, row.TenantID, row.MachineID, row.ID); e != nil {
		return 0, e
	}
	if e = m.workspaceFreeOn(ctx, tx, row.TenantID, row.MachineID, row.ID, p.WorkdirID); e != nil {
		return 0, e
	}
	if e = query.BindExecutions(tx, m.names).WithContext(ctx).Create(&row); e != nil {
		return 0, e
	}
	if e = m.eventOn(ctx, tx, &row, "execution.state_changed", "SYSTEM", "SYSTEM:planning", map[string]any{"state": "CREATED", "purpose": "PLANNING", "assignment_id": in.AssignmentID}); e != nil {
		return 0, e
	}
	return id, nil
}

func (m *Module) authorizePlanningOn(ctx context.Context, tx *gorm.DB, row *model.Execution, op string) (ExecutionView, error) {
	v, e := view(row)
	if e != nil {
		return v, e
	}
	if v.Plan.Planning == nil || v.Plan.AuthorizationMode != "READ_ONLY" || v.Plan.ExecutorPrincipalID <= 0 || row.InitiatorID != 0 || m.planning.AuthorizeOn == nil {
		return v, ErrForbidden
	}
	machine, e := m.machines.ByIDOn(ctx, tx, row.TenantID, row.MachineID)
	if e != nil {
		return v, e
	}
	ids := []guid.ID{v.Plan.ExecutorPrincipalID, machine.PrincipalID}
	if op != "CANCEL" && op != "CONTROL_RECEIPT" {
		ids, e = m.planningPrincipalsOn(ctx, tx, row.TenantID, v.Plan.ExecutorPrincipalID)
		if e != nil {
			return v, e
		}
		ids = append(ids, machine.PrincipalID)
	}
	if v.Plan.Planning.Review != nil && v.Plan.Planning.Review.SourceExecutionID > 0 {
		source, e := m.rowOn(ctx, tx, row.TenantID, v.Plan.Planning.Review.SourceExecutionID, false)
		if e != nil {
			return v, e
		}
		sourceView, e := view(source)
		if e != nil {
			return v, e
		}
		sourceMachine, e := m.machines.ByIDOn(ctx, tx, row.TenantID, source.MachineID)
		if e != nil {
			return v, e
		}
		ids = append(ids, sourceView.Plan.ExecutorPrincipalID, sourceMachine.PrincipalID)
		if op != "CANCEL" && op != "CONTROL_RECEIPT" {
			extra, e := m.planningPrincipalsOn(ctx, tx, row.TenantID, sourceView.Plan.ExecutorPrincipalID)
			if e != nil {
				return v, e
			}
			ids = append(ids, extra...)
		}
	}
	if m.planning.PrincipalsOn != nil && op != "CANCEL" && op != "CONTROL_RECEIPT" {
		additional, e := m.planning.PrincipalsOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID})
		if e != nil {
			return v, e
		}
		ids = append(ids, additional...)
	}
	if e = m.principalLocks(ctx, tx, append(ids, authn.PrincipalIDFromContext(ctx))...); e != nil {
		return v, e
	}
	if v.Plan.Planning.Review != nil && (op == "CANCEL" || op == "CONTROL_RECEIPT") {
		_, e = m.planning.AuthorizeOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx)}, "REVIEW_CONTROL")
		return v, e
	}
	if v.Plan.Planning.Review != nil {
		if m.planning.AuthorizeReviewOn == nil {
			return v, ErrCapabilityUnavailable
		}
		plan, e := m.planning.AuthorizeReviewOn(ctx, tx, row.TenantID, v.Plan.Planning.Review.PlanID, v.Plan.ExecutorPrincipalID, op == "REPLAY")
		if e != nil {
			return v, e
		}
		if op != "REPLAY" && plan.Version != v.Plan.Planning.Review.Version || plan.ContentHash != v.Plan.Planning.Review.ContentHash {
			return v, ErrConflict
		}
		a, found, e := m.agents.ByPrincipalOn(ctx, tx, row.TenantID, v.Plan.ExecutorPrincipalID)
		if e != nil {
			return v, e
		}
		if !found || a.State != "ACTIVE" || a.ModelID != v.Plan.Model || a.ModelFamily != v.Plan.ModelFamily || a.RuntimeKind != v.Plan.Runtime {
			return v, ErrForbidden
		}
		runtime, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, row.TenantID, a.PrincipalID)
		if e != nil {
			return v, e
		}
		if !found || runtime.MachineID == nil || *runtime.MachineID != row.MachineID || runtime.RuntimeKind != v.Plan.Runtime {
			return v, ErrForbidden
		}
		return v, nil
	}
	facts, e := m.planning.AuthorizeOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx)}, op)
	if e != nil {
		return v, e
	}
	if op == "CANCEL" || op == "CONTROL_RECEIPT" {
		return v, nil
	}
	a, found, e := m.agents.ByPrincipalOn(ctx, tx, row.TenantID, v.Plan.ExecutorPrincipalID)
	if e != nil {
		return v, e
	}
	if !found || a.State != "ACTIVE" || a.ModelID != v.Plan.Model || a.ModelFamily != v.Plan.ModelFamily || a.RuntimeKind != v.Plan.Runtime || facts.AgentPrincipalID != a.PrincipalID {
		return v, ErrForbidden
	}
	actual, _ := json.Marshal(facts.Binding)
	frozen, _ := json.Marshal(v.Plan.Planning.Binding)
	if op != "REPLAY" && string(actual) != string(frozen) {
		return v, ErrConflict
	}
	runtime, found, e := m.agentRuntime.ByPrincipalOn(ctx, tx, row.TenantID, a.PrincipalID)
	if e != nil {
		return v, e
	}
	if !found || runtime.MachineID == nil || *runtime.MachineID != row.MachineID {
		return v, ErrForbidden
	}
	return v, nil
}

func (m *Module) pullPlanningOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, f RunnerFence) ([]ExecutionView, bool, error) {
	q := query.BindExecutions(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(f.MachineID), q.BranchReason.In("PLANNING", "PLAN_REVIEW"), q.State.Eq("CREATED"), q.LeaseExpiresAt.IsNull()).Order(q.ID).Limit(1).Find()
	if e != nil || len(rows) == 0 {
		return nil, false, e
	}
	v, e := m.authorizePlanningOn(ctx, tx, rows[0], "DISPATCH")
	if e != nil {
		if rows[0].BranchReason == "PLAN_REVIEW" && errors.Is(e, delivery.ErrPlanNotApproved) {
			if e = m.validateRunner(ctx, tx, tenant, f); e != nil {
				return nil, true, e
			}
			row, e := m.rowOn(ctx, tx, tenant, rows[0].ID, true)
			if e != nil {
				return nil, true, e
			}
			if row.State != "CREATED" || row.LeaseExpiresAt != nil {
				return nil, true, ErrConflict
			}
			if m.work.SettleBudgetOn == nil {
				return nil, true, ErrCapabilityUnavailable
			}
			if e = m.work.SettleBudgetOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: StateCanceled, Unused: row.DispatchEpoch == 1}); e != nil {
				return nil, true, e
			}
			if e = m.machines.ReleaseSlotOn(ctx, tx, tenant, row.MachineID, row.ID); e != nil {
				return nil, true, e
			}
			if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": StateCanceled, "result_state": "CONFIRMED", "state_reason": "PLAN_REVIEW_SUPERSEDED", "version": row.Version + 1}); e != nil {
				return nil, true, e
			}
			row.State = StateCanceled
			row.ResultState = "CONFIRMED"
			row.StateReason = "PLAN_REVIEW_SUPERSEDED"
			row.Version++
			if e = m.eventOn(ctx, tx, row, "execution.state_changed", "SYSTEM", "SYSTEM:plan_review_superseded", map[string]any{"state": StateCanceled, "never_leased": true}); e != nil {
				return nil, true, e
			}
			return []ExecutionView{}, true, nil
		}
		return nil, true, e
	}
	if e = m.validateRunner(ctx, tx, tenant, f); e != nil {
		return nil, true, e
	}
	row, e := m.rowOn(ctx, tx, tenant, v.ID, true)
	if e != nil {
		return nil, true, e
	}
	if row.State != "CREATED" || row.LeaseExpiresAt != nil {
		return []ExecutionView{}, true, nil
	}
	expiry := time.Now().UTC().Truncate(time.Millisecond).Add(30 * time.Second)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"lease_expires_at": expiry, "version": row.Version + 1})
	if e != nil {
		return nil, true, e
	}
	if changed.RowsAffected != 1 {
		return nil, true, ErrConflict
	}
	v.LeaseExpiresAt = &expiry
	v.Version++
	row.Version = v.Version
	if e = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"state": "CREATED", "purpose": "PLANNING", "leased": true}); e != nil {
		return nil, true, e
	}
	return []ExecutionView{v}, true, nil
}

func (m *Module) receiptPlanningOn(ctx context.Context, tx *gorm.DB, snapshot *model.Execution, f RunnerFence, in ReceiptInput) (ReceiptView, error) {
	empty := ReceiptView{}
	if in.Upload != nil || in.Progress != nil || in.EvidenceKind != "" || in.EvidenceTitle != "" {
		return empty, ErrInvalidArgument
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return empty, e
	}
	digest := hash(raw)
	r := query.BindReceipts(tx, m.names)
	prior, e := r.WithContext(ctx).Where(r.TenantID.Eq(snapshot.TenantID), r.ExecutionID.Eq(snapshot.ID), r.DispatchEpoch.Eq(in.DispatchEpoch), r.Seq.Eq(in.Seq)).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, e
	}
	operation := receiptOperation(in.Kind)
	if prior != nil {
		operation = "REPLAY"
	}
	v, e := m.authorizePlanningOn(ctx, tx, snapshot, operation)
	if e != nil {
		return empty, e
	}
	if e = m.validateRunner(ctx, tx, snapshot.TenantID, f); e != nil {
		return empty, e
	}
	row, e := m.rowOn(ctx, tx, snapshot.TenantID, snapshot.ID, true)
	if e != nil {
		return empty, e
	}
	if row.MachineID != f.MachineID || row.DispatchEpoch != in.DispatchEpoch {
		return empty, ErrFenced
	}
	prior, e = r.WithContext(ctx).Where(r.TenantID.Eq(row.TenantID), r.ExecutionID.Eq(row.ID), r.DispatchEpoch.Eq(in.DispatchEpoch), r.Seq.Eq(in.Seq)).First()
	if e == nil {
		if prior.ContentHash != digest {
			return empty, ErrConflict
		}
		return ReceiptView{Seq: in.Seq, State: prior.State, Replayed: true, CancelRequested: row.CancelRequestedAt != nil, PauseRequested: row.StateReason == "PAUSE_REQUESTED", ResumeRequested: row.StateReason == "RESUME_REQUESTED"}, nil
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return empty, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(now) || in.Seq != row.LastSeq+1 {
		return empty, ErrFenced
	}
	next, result, reason := row.State, row.ResultState, row.StateReason
	released := false
	settled := false
	switch in.Kind {
	case "ACCEPTED":
		if row.State != "CREATED" || row.CancelRequestedAt != nil {
			return empty, ErrConflict
		}
		next = "RUNNING"
	case "HEARTBEAT", "LOG":
		if row.State != "RUNNING" && (in.Kind != "HEARTBEAT" || row.State != "PAUSED") {
			return empty, ErrConflict
		}
	case "PAUSED":
		if row.State != "RUNNING" || (row.StateReason != "PAUSE_REQUESTED" && row.CancelRequestedAt == nil) {
			return empty, ErrConflict
		}
		next = "PAUSED"
		if row.CancelRequestedAt == nil {
			reason = "HOLD_ACKNOWLEDGED"
		}
	case "RESUMED":
		if row.State != "PAUSED" || (row.StateReason != "RESUME_REQUESTED" && row.StateReason != "HOLD_ACKNOWLEDGED" && row.CancelRequestedAt == nil) {
			return empty, ErrConflict
		}
		next = "RUNNING"
		if row.CancelRequestedAt == nil {
			reason = "HOLD_RELEASE_ACKNOWLEDGED"
			if row.StateReason == "HOLD_ACKNOWLEDGED" {
				reason = "PAUSE_REQUESTED"
			}
		}
	case "COMPLETED":
		if row.State != "RUNNING" || row.CancelRequestedAt != nil || !commitHash(in.HeadCommit) {
			return empty, ErrConflict
		}
		if e = m.machines.ReleaseSlotOn(ctx, tx, row.TenantID, row.MachineID, row.ID); e != nil {
			return empty, e
		}
		released = true
		// Release the completed planner/reviewer's budget reservation before
		// calculating the next runtime's budget in this same transaction.
		cost, e := actualExecutionCost(v.Plan, in.Usage)
		if e != nil {
			return empty, e
		}
		if m.work.SettleBudgetOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if e = m.work.SettleBudgetOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: "SUCCEEDED", ActualMicros: cost}); e != nil {
			return empty, e
		}
		settled = true
		// The actual terminal report has ended this read-only runtime. The next
		// runtime must see its workspace as free in this same atomic transaction.
		terminalQuery := query.BindExecutions(tx, m.names)
		if _, e = terminalQuery.WithContext(ctx).Where(terminalQuery.TenantID.Eq(row.TenantID), terminalQuery.ID.Eq(row.ID), terminalQuery.Version.Eq(row.Version)).Update(terminalQuery.State, "SUCCEEDED"); e != nil {
			return empty, e
		}
		if v.Plan.Planning.Review != nil {
			var decision struct {
				Action   string `json:"action"`
				Feedback string `json:"feedback"`
			}
			decoder := json.NewDecoder(strings.NewReader(in.Content))
			decoder.DisallowUnknownFields()
			if !json.Valid([]byte(in.Content)) || decoder.Decode(&decision) != nil {
				return empty, ErrInvalidArgument
			}
			review := v.Plan.Planning.Review
			if m.planning.CompleteReviewOn == nil || in.HeadCommit != v.Plan.BaseCommit {
				return empty, ErrConflict
			}
			if e = m.planning.CompleteReviewOn(ctx, tx, row.TenantID, review.PlanID, v.Plan.ExecutorPrincipalID, review.Version, review.ContentHash, decision.Action, decision.Feedback); e != nil {
				return empty, e
			}
			next, result, reason = "SUCCEEDED", "CONFIRMED", "PLAN_REVIEWED"
			break
		}
		var content delivery.ReviewPlanContent
		decoder := json.NewDecoder(strings.NewReader(in.Content))
		decoder.DisallowUnknownFields()
		if !json.Valid([]byte(in.Content)) || decoder.Decode(&content) != nil {
			return empty, ErrInvalidArgument
		}
		if m.planning.CompleteOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		producer := delivery.PlanProducer{ExecutionID: row.ID, AgentPrincipalID: v.Plan.ExecutorPrincipalID, Model: v.Plan.Model, Family: v.Plan.ModelFamily, HeadCommit: in.HeadCommit}
		if _, e = m.planning.CompleteOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx)}, producer, content); e != nil {
			return empty, e
		}
		next, result, reason = "SUCCEEDED", "CONFIRMED", "PLAN_PROPOSED"
	case "FAILED":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, result, reason = "FAILED", "CONFIRMED", "RUNTIME_ERROR"
	case "LOST":
		if !active(row.State) {
			return empty, ErrConflict
		}
		next, result, reason = "INTERRUPTED", "UNKNOWN", "HEARTBEAT_LOST"
	case StateCanceled:
		if row.CancelRequestedAt == nil || !active(row.State) {
			return empty, ErrConflict
		}
		next, result, reason = StateCanceled, "CONFIRMED", "USER_CANCEL"
	default:
		return empty, ErrInvalidArgument
	}
	expiry := now.Add(30 * time.Second)
	var lease *time.Time = &expiry
	if !active(next) {
		lease = nil
		cost, err := actualExecutionCost(v.Plan, in.Usage)
		if err != nil {
			return empty, err
		}
		if m.work.SettleBudgetOn == nil {
			return empty, ErrCapabilityUnavailable
		}
		if !settled {
			if e = m.work.SettleBudgetOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: next, ActualMicros: cost}); e != nil {
				return empty, e
			}
		}
		if next != "SUCCEEDED" && next != "INTERRUPTED" && v.Plan.Planning.Review == nil {
			if m.planning.EndOn == nil {
				return empty, ErrCapabilityUnavailable
			}
			if e = m.planning.EndOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: row.TenantID, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx)}, next); e != nil {
				return empty, e
			}
		}
		if !released {
			if e = m.machines.ReleaseSlotOn(ctx, tx, row.TenantID, row.MachineID, row.ID); e != nil {
				return empty, e
			}
		}
	}
	q := query.BindExecutions(tx, m.names)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": next, "state_reason": reason, "result_state": result, "last_seq": in.Seq, "lease_expires_at": lease, "version": row.Version + 1, "updated_at": now})
	if e != nil {
		return empty, e
	}
	if changed.RowsAffected != 1 {
		return empty, ErrConflict
	}
	id, e := m.nextID()
	if e != nil {
		return empty, e
	}
	if e = r.WithContext(ctx).Create(&model.Receipt{ID: id, TenantID: row.TenantID, ExecutionID: row.ID, DispatchEpoch: row.DispatchEpoch, Seq: in.Seq, Kind: in.Kind, ContentHash: digest, State: next, CreatedAt: now}); e != nil {
		return empty, e
	}
	row.State, row.ResultState, row.StateReason, row.Version = next, result, reason, row.Version+1
	if e = m.eventOn(ctx, tx, row, "execution.state_changed", "MACHINE", "IAM", map[string]any{"state": next, "purpose": "PLANNING", "receipt_kind": in.Kind, "seq": in.Seq}); e != nil {
		return empty, e
	}
	return ReceiptView{Seq: in.Seq, State: next, CancelRequested: row.CancelRequestedAt != nil, PauseRequested: reason == "PAUSE_REQUESTED", ResumeRequested: reason == "RESUME_REQUESTED"}, nil
}

func (m *Module) continuePlanningOn(ctx context.Context, tx *gorm.DB, tenant, planID, executionID guid.ID, approved bool, feedback string) (guid.ID, error) {
	if transaction(tx, tenant, planID) != nil || executionID <= 0 {
		return 0, ErrInvalidArgument
	}
	parent, e := m.rowOn(ctx, tx, tenant, executionID, false)
	if e != nil {
		return 0, e
	}
	if parent.BranchReason != "PLANNING" || parent.State != "SUCCEEDED" {
		return 0, ErrConflict
	}
	source, e := view(parent)
	if e != nil {
		return 0, e
	}
	if !approved {
		return m.launchPlanningOn(ctx, tx, delivery.PlanningLaunch{TenantID: tenant, ProjectID: parent.ProjectID, WorkItemID: parent.WorkItemID, AssignmentID: parent.AssignmentID, AgentPrincipalID: source.Plan.ExecutorPrincipalID, Objective: "Previous review rejected the plan. Address these findings: " + feedback})
	}
	if m.planning.ImplementationOn == nil {
		return 0, ErrCapabilityUnavailable
	}
	producer, e := m.planning.ImplementationOn(ctx, tx, tenant, planID, executionID)
	if e != nil {
		return 0, e
	}
	agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, source.Plan.ExecutorPrincipalID)
	if e != nil {
		return 0, e
	}
	if !found || agent.State != "ACTIVE" || agent.ModelFamily != producer.Family || agent.ModelID != producer.Model {
		return 0, ErrForbidden
	}
	if agent.AuthorizationMode != "WORKSPACE_WRITE" && agent.AuthorizationMode != "ON_REQUEST" {
		return 0, ErrApprovalUnavailable
	}
	id, e := m.nextID()
	if e != nil {
		return 0, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := model.Execution{ID: id, TenantID: tenant, ProjectID: parent.ProjectID, WorkItemID: parent.WorkItemID, AssignmentID: parent.AssignmentID, MachineID: parent.MachineID, InitiatorID: 0, ParentExecutionID: &parent.ID, SourceDecisionID: &planID, BranchReason: "PLANNED", Autostart: true, State: "CREATED", ResultState: "NONE", DispatchEpoch: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
	p := source.Plan
	p.Planning = nil
	p.ApprovedPlanID = planID
	p.ApprovedPlan = producer.ApprovedPlan
	p.BaseCommit = producer.HeadCommit
	p.AuthorizationMode = agent.AuthorizationMode
	p.Objective = producer.Objective
	if strings.TrimSpace(p.Objective) == "" {
		p.Objective = "Implement the approved frozen execution plan exactly."
	}
	if m.commentFeedback != nil {
		p.CommentFeedback, e = m.commentFeedback(executorContext(ctx, tenant, p.ExecutorPrincipalID), tx, tenant, parent.ProjectID, parent.WorkItemID, p.ExecutorPrincipalID)
		if e != nil {
			return 0, e
		}
	}
	p.QualityFeedback = ""
	p.ApprovalProtocol = ""
	if p.AuthorizationMode == "ON_REQUEST" {
		if p.Runtime == "CODEX" {
			p.ApprovalProtocol = "CODEX_APP_SERVER_V1"
		} else {
			p.ApprovalProtocol = approvalProtocol
		}
	}
	p.Correlation, e = newCorrelation(ctx, source.Plan.Correlation)
	if e != nil {
		return 0, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return 0, e
	}
	row.PlanJSON, row.PlanHash = string(raw), hash(raw)
	system := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant})
	if e = m.work.AuthorizeOn(system, tx, link(&row, 0), "CREATE"); e != nil {
		return 0, e
	}
	if e = m.pinExecutionIdentityOn(system, tx, link(&row, 0), &p); e != nil {
		return 0, e
	}
	if e = m.validateAgentBindingOn(system, tx, link(&row, 0), row.MachineID, p); e != nil {
		return 0, e
	}
	if e = m.budgetOn(system, tx, tenant, link(&row, 0), &p); e != nil {
		return 0, e
	}
	workspace, e := m.machines.ResolveWorkspaceOn(system, tx, tenant, row.MachineID, row.ProjectID, p.RepositoryID)
	if e != nil {
		return 0, e
	}
	p.WorkdirID, p.Workdir = workspace.ID, workspace.Path
	raw, e = json.Marshal(p)
	if e != nil {
		return 0, e
	}
	row.PlanJSON, row.PlanHash = string(raw), hash(raw)
	if _, e = m.machines.ReserveSlotOn(system, tx, tenant, row.MachineID, row.ID); e != nil {
		return 0, e
	}
	if e = m.workspaceFreeOn(system, tx, tenant, row.MachineID, row.ID, workspace.ID); e != nil {
		return 0, e
	}
	if e = query.BindExecutions(tx, m.names).WithContext(system).Create(&row); e != nil {
		return 0, e
	}
	if e = m.eventOn(system, tx, &row, "execution.state_changed", "SYSTEM", "SYSTEM:plan_approved", map[string]any{"state": "CREATED", "plan_id": planID, "parent_execution_id": parent.ID}); e != nil {
		return 0, e
	}
	return id, nil
}
