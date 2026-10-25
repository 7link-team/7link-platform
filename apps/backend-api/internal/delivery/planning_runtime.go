package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PlanningRuntime participates in the assignment/decision transaction. Prepare
// returns the runtime's principal lock hints before any project rows are locked.
type PlanningRuntime struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	LaunchOn     func(context.Context, *gorm.DB, PlanningLaunch) (guid.ID, error)
	ContinueOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool, string) (guid.ID, error)
	ReviewOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)
}
type PlanningLaunch struct {
	TenantID, ProjectID, WorkItemID, AssignmentID, AgentPrincipalID, RepositoryID guid.ID
	Objective, DefaultBranch                                                      string
	Binding                                                                       *ReviewPlanBinding
	Criteria                                                                      []PlanningCriterion
}
type PlanningCriterion struct {
	ID     guid.ID `json:"id"`
	Text   string  `json:"text"`
	Method string  `json:"method"`
}
type PlanProducer struct {
	ApprovedPlan     *ReviewPlanContent `json:"-"`
	ExecutionID      guid.ID            `json:"execution_id"`
	AgentPrincipalID guid.ID            `json:"agent_principal_id"`
	Objective        string             `json:"objective"`
	Model            string             `json:"model"`
	Family           string             `json:"family"`
	HeadCommit       string             `json:"head_commit"`
}
type PlanningWork struct {
	EndOn             func(context.Context, *gorm.DB, ExecutionWorkLink, string) error
	PrincipalsOn      func(context.Context, *gorm.DB, ExecutionWorkLink) ([]guid.ID, error)
	Bind              func(PlanningRuntime) error
	AuthorizeOn       func(context.Context, *gorm.DB, ExecutionWorkLink, string) (PlanningLaunch, error)
	ImplementationOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (PlanProducer, error)
	CompleteOn        func(context.Context, *gorm.DB, ExecutionWorkLink, PlanProducer, ReviewPlanContent) (ReviewPlanView, error)
	AuthorizeReviewOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ReviewPlanView, error)
	CompleteReviewOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int, string, string, string) error
}

func (m *Module) providePlanningRuntime(s *module.Services) error {
	return module.Provide(s, PlanningWork{Bind: func(v PlanningRuntime) error {
		if v.PrincipalsOn == nil || v.LaunchOn == nil || v.ContinueOn == nil || m.planningRuntime.LaunchOn != nil {
			return ErrInvalidArgument
		}
		m.planningRuntime = v
		return nil
	}, EndOn: m.endPlanningWorkOn, PrincipalsOn: m.planningWorkPrincipalsOn, AuthorizeOn: m.authorizePlanningWorkOn, ImplementationOn: m.planningImplementationOn, CompleteOn: m.completePlanningWorkOn, AuthorizeReviewOn: m.authorizePlanningReviewOn, CompleteReviewOn: m.completePlanningReviewOn})
}

func (m *Module) planningWorkPrincipalsOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink) ([]guid.ID, error) {
	ids, e := m.executionPrincipalsOn(ctx, tx, l)
	if e != nil {
		return nil, e
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, e := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID)).First()
	if e != nil {
		return nil, e
	}
	hidden, e := m.hiddenAcceptancePrincipalsOn(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID)
	if e != nil {
		return nil, e
	}
	ids = append(ids, work.AccountablePrincipalID)
	return append(ids, hidden...), nil
}

func requiresPlanning(w *model.WorkItem) bool {
	return w.PlanRequired || w.Risk == "MEDIUM" || w.Risk == "HIGH" || w.AutonomyLevel == "MEDIUM" || w.AutonomyLevel == "HIGH"
}

// authorizePlanningWorkOn is invoked only by the execution owner, after locking
// participants. It rechecks the real current assignment, IAM and frozen inputs.
func (m *Module) authorizePlanningWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, op string) (PlanningLaunch, error) {
	out := PlanningLaunch{}
	if !isWorkActivityTransaction(tx) || l.TenantID <= 0 || l.ProjectID <= 0 || l.AssignmentID <= 0 || l.ExecutionID <= 0 || authn.TenantIDFromContext(ctx) != l.TenantID || l.ActorID != authn.PrincipalIDFromContext(ctx) {
		return out, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	p := query.BindProjects(tx, m.service.names)
	project, e := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(l.TenantID), p.ProjectID.Eq(l.ProjectID), p.ArchivedAt.IsNull()).First()
	if e != nil {
		return out, e
	}
	work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID), w.ArchivedAt.IsNull()).First()
	if e != nil {
		return out, e
	}
	control := op == "CANCEL" || op == "CONTROL_RECEIPT" || op == "REVIEW_CONTROL"
	if !control && (project.Stage != "ACTIVE" || work.PausedAt != nil || work.BaselineStale || project.BudgetState == "EXHAUSTED") {
		return out, ErrAssignmentState
	}
	if !control {
		if e = m.requireNoAndonOn(ctx, tx, work); e != nil {
			return out, e
		}
		if e = m.requireCurrentUpstreamOn(ctx, tx, work); e != nil {
			return out, e
		}
	}
	a := query.BindAssignments(tx, m.service.names)
	assignmentQuery := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.AssignmentID), a.Role.Eq("EXECUTOR"))
	if !control {
		assignmentQuery = assignmentQuery.Where(a.Status.Eq("ACTIVE"))
	}
	assignment, e := assignmentQuery.First()
	if e != nil {
		return out, e
	}
	if assignment.AssigneeKind != "AGENT" || assignment.EnsemblePlanID != nil || (!control && (assignment.LeaseExpiresAt == nil || !assignment.LeaseExpiresAt.After(time.Now().UTC()))) {
		return out, ErrAssignmentHolder
	}
	if !control && (m.agents.ByPrincipalOn == nil || m.members == nil) {
		return out, ErrUnavailable
	}
	if !control {
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, assignment.AssigneePrincipalID)
		if e != nil {
			return out, e
		}
		member, e := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, assignment.AssigneePrincipalID)
		if e != nil {
			return out, e
		}
		if !control && (!found || !member || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "EXECUTOR")) {
			return out, ErrAssignmentHolder
		}
		if !control {
			c, e := m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "work_item_execute", assignment.AssigneePrincipalID)
			if e != nil {
				return out, e
			}
			scope, e := workScope(c, l.ProjectID, w.CreatedBy)
			if e != nil {
				return out, e
			}
			base := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(work.ID))
			if scope != nil {
				base = base.Where(scope)
			}
			if _, e = base.First(); e != nil {
				return out, ErrWorkForbidden
			}
		}

	}
	if op == "CREATE" {
		if !requiresPlanning(work) || !slices.Contains([]string{"CLAIMED", "REWORK"}, work.State) || work.CurrentExecutionID != nil {
			return out, ErrAssignmentState
		}
	} else if op != "REPLAY" && op != "REVIEW_CONTROL" && (work.CurrentExecutionID == nil || *work.CurrentExecutionID != l.ExecutionID) {
		return out, ErrAssignmentHolder
	}
	if control {
		return PlanningLaunch{TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: work.ID, AssignmentID: assignment.ID, AgentPrincipalID: assignment.AssigneePrincipalID}, nil
	}
	r := query.BindRepositories(tx, m.service.names)
	repositories, e := r.WithContext(ctx).Where(r.TenantID.Eq(l.TenantID), r.ProjectID.Eq(l.ProjectID), r.IsPrimary.Is(true), r.ArchivedAt.IsNull()).Limit(2).Find()
	if e != nil {
		return out, e
	}
	if len(repositories) != 1 {
		return out, ErrUnavailable
	}
	out = PlanningLaunch{TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: work.ID, AssignmentID: assignment.ID, AgentPrincipalID: assignment.AssigneePrincipalID, RepositoryID: repositories[0].ID, DefaultBranch: repositories[0].DefaultBranch, Criteria: []PlanningCriterion{}}
	if work.Objective != nil {
		out.Objective = *work.Objective
	}
	out.Binding, e = m.reviewPlanBindingOn(ctx, tx, work)
	if e != nil {
		return out, e
	}
	q := query.BindWorkCriteria(tx, m.service.names)
	criteria, e := q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.WorkItemID.Eq(work.ID)).Order(q.Position, q.ID).Find()
	if e != nil {
		return out, e
	}
	for _, criterion := range criteria {
		out.Criteria = append(out.Criteria, PlanningCriterion{criterion.ID, criterion.Text, criterion.VerificationMethod})
	}
	if op == "CREATE" {
		changed, e := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(work.ID), w.Version.Eq(work.Version), w.CurrentExecutionID.IsNull()).Update(w.CurrentExecutionID, l.ExecutionID)
		if e != nil {
			return out, e
		}
		if changed.RowsAffected != 1 {
			return out, ErrWorkVersion
		}
	}
	return out, nil
}

func (m *Module) completePlanningWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, producer PlanProducer, content ReviewPlanContent) (ReviewPlanView, error) {
	var out ReviewPlanView
	launch, e := m.authorizePlanningWorkOn(ctx, tx, l, "RECEIPT")
	if e != nil {
		return out, e
	}
	if producer.ExecutionID != l.ExecutionID || producer.AgentPrincipalID != launch.AgentPrincipalID || producer.Model == "" || producer.Family == "" || len(producer.HeadCommit) != 40 && len(producer.HeadCommit) != 64 {
		return out, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, e := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(l.WorkItemID)).First()
	if e != nil {
		return out, e
	}
	if e = m.validatePlanCoverageOn(ctx, tx, work, content); e != nil {
		return out, e
	}
	if e = m.requirePlanBindingOn(ctx, tx, work, content); e != nil {
		return out, e
	}
	producer.Objective = launch.Objective
	content.Producer = &producer
	raw, e := json.Marshal(content)
	if e != nil {
		return out, e
	}
	q := query.BindReviewPlans(tx, m.service.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(l.TenantID), q.WorkItemID.Eq(work.ID)).Order(q.Round.Desc()).Limit(1).Find()
	if e != nil {
		return out, e
	}
	round := 1
	if len(rows) > 0 {
		round = rows[0].Round + 1
	}
	if round > 3 {
		return out, ErrResultState
	}
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.WorkItemID.Eq(work.ID), q.State.Eq("PROPOSED")).Update(q.State, "SUPERSEDED"); e != nil {
		return out, e
	}
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := model.ReviewPlan{ID: guid.ID(id), TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: work.ID, ProposedBy: producer.AgentPrincipalID, SourceKind: "AGENT", Content: string(raw), ContentHash: planDigest(raw), CoverageOK: true, Round: round, State: "PROPOSED", Version: 1, CreatedAt: now}
	if e = q.WithContext(ctx).Create(&plan); e != nil {
		return out, e
	}
	before := work.State
	work.State = "PLAN_REVIEW"
	if _, e = w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(work.ID)).Updates(map[string]any{"current_execution_id": nil, "current_plan_id": plan.ID}); e != nil {
		return out, e
	}
	if e = m.executionWorkChangedOn(ctx, tx, l, work, before, "PLAN_PROPOSED", now); e != nil {
		return out, e
	}
	if e = m.recordReviewPlanOn(ctx, tx, &plan, producer.AgentPrincipalID, "proposed", ""); e != nil {
		return out, e
	}
	if e = m.queuePlanningReviewOn(ctx, tx, work, &plan); e != nil {
		return out, e
	}
	return reviewPlanView(&plan)
}

func (m *Module) planReviewerOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, plan *model.ReviewPlan, actor guid.ID) (string, error) {
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return "", e
	}
	if active && human.ID == actor {
		return "permission:plan_approve", nil
	}
	if m.agents.ByPrincipalOn == nil {
		return "", ErrUnavailable
	}
	reviewer, found, e := m.agents.ByPrincipalOn(ctx, tx, work.TenantID, actor)
	if e != nil {
		return "", e
	}
	if !found || reviewer.State != "ACTIVE" || !slices.Contains(reviewer.Roles, "REVIEWER") || reviewer.ModelFamily == "" {
		return "", ErrResultIndependent
	}
	v, e := reviewPlanView(plan)
	if e != nil {
		return "", e
	}
	if v.Content.Producer == nil || v.Content.Producer.AgentPrincipalID == actor || v.Content.Producer.Family == reviewer.ModelFamily {
		return "", ErrResultIndependent
	}
	a := query.BindAssignments(tx, m.service.names)
	assignments, e := a.WithContext(ctx).Where(a.TenantID.Eq(work.TenantID), a.WorkItemID.Eq(work.ID), a.Role.Eq("EXECUTOR")).Find()
	if e != nil {
		return "", e
	}
	for _, assignment := range assignments {
		if assignment.AssigneePrincipalID == actor || assignment.CreatedBy != nil && *assignment.CreatedBy == actor {
			return "", ErrResultIndependent
		}
	}
	if m.deliveryAuthority == nil {
		return "", ErrUnavailable
	}
	decision, e := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, ActorKind: "AGENT", PrincipalID: actor, Code: "plan_approve", Resource: governance.ResourceRef{Kind: "work_item", ID: work.ID}, Facts: &governance.WorkItemFacts{Type: work.Type, Risk: work.Risk, AssignmentRole: "REVIEWER", AssigneePrincipalID: v.Content.Producer.AgentPrincipalID, RequireHumanDecision: work.RequireHumanDecision, Paused: work.PausedAt != nil}})
	if e != nil {
		return "", e
	}
	if !decision.Allowed || !strings.HasPrefix(decision.AuthoritySource, "POLICY:") {
		return "", ErrWorkForbidden
	}
	return decision.AuthoritySource, nil
}

func (m *Module) prelockPlanningDecisionOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, planID, actor, owner guid.ID) error {
	q := query.BindReviewPlans(tx, m.service.names)
	plan, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(workID), q.ID.Eq(planID)).First()
	if e != nil {
		return e
	}
	v, e := reviewPlanView(plan)
	if e != nil {
		return e
	}
	if v.Content.Producer == nil {
		return nil
	}
	if m.planningRuntime.PrincipalsOn == nil {
		return ErrUnavailable
	}
	ids, e := m.planningRuntime.PrincipalsOn(ctx, tx, tenant, v.Content.Producer.AgentPrincipalID)
	if e != nil {
		return e
	}
	return m.lockStagePrincipalsOn(ctx, tx, actor, append(ids, owner))
}

func (m *Module) plannedImplementationAuthorityOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, operation string) (authz.DataConstraint, error) {
	deny := authz.DataConstraint{}
	if l.InitiatorID != 0 || l.ReviewPlanID <= 0 || !slices.Contains([]string{"CREATE", "DISPATCH", "START", "RECEIPT", "RECOVER"}, operation) {
		return deny, ErrWorkForbidden
	}
	q := query.BindReviewPlans(tx, m.service.names)
	plan, e := q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.ProjectID.Eq(l.ProjectID), q.WorkItemID.Eq(l.WorkItemID), q.ID.Eq(l.ReviewPlanID)).First()
	if e != nil {
		return deny, e
	}
	v, e := reviewPlanView(plan)
	if e != nil {
		return deny, e
	}
	if plan.SourceKind != "AGENT" || plan.State != "APPROVED" || plan.DecidedBy == nil || v.Content.Producer == nil || v.Content.Producer.ExecutionID != l.ParentExecutionID || plan.ContentHash != planDigest([]byte(plan.Content)) {
		return deny, ErrPlanNotApproved
	}
	a := query.BindAssignments(tx, m.service.names)
	assignment, e := a.WithContext(ctx).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.AssignmentID), a.Status.Eq("ACTIVE"), a.AssigneeKind.Eq("AGENT")).First()
	if e != nil {
		return deny, e
	}
	if assignment.AssigneePrincipalID != v.Content.Producer.AgentPrincipalID {
		return deny, ErrAssignmentHolder
	}
	if m.agents.ByPrincipalOn == nil {
		return deny, ErrUnavailable
	}
	agent, found, e := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, assignment.AssigneePrincipalID)
	if e != nil {
		return deny, e
	}
	if !found || agent.State != "ACTIVE" || agent.ModelID != v.Content.Producer.Model || agent.ModelFamily != v.Content.Producer.Family {
		return deny, ErrAssignmentHolder
	}
	return m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "work_item_execute", assignment.AssigneePrincipalID)
}

func (m *Module) planningImplementationOn(ctx context.Context, tx *gorm.DB, tenant, planID, executionID guid.ID) (PlanProducer, error) {
	q := query.BindReviewPlans(tx, m.service.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(planID), q.State.Eq("APPROVED")).First()
	if e != nil {
		return PlanProducer{}, e
	}
	v, e := reviewPlanView(row)
	if e != nil {
		return PlanProducer{}, e
	}
	if v.Content.Producer == nil || v.Content.Producer.ExecutionID != executionID || row.ContentHash != planDigest([]byte(row.Content)) {
		return PlanProducer{}, ErrPlanNotApproved
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(row.ProjectID), w.ID.Eq(row.WorkItemID)).First()
	if e != nil {
		return PlanProducer{}, e
	}
	if e = m.requireApprovedReviewPlanOn(ctx, tx, work); e != nil {
		return PlanProducer{}, e
	}
	producer := *v.Content.Producer
	producer.ApprovedPlan = &v.Content
	return producer, nil
}

func (m *Module) authorizePlanningReviewOn(ctx context.Context, tx *gorm.DB, tenant, planID, reviewer guid.ID, replay bool) (ReviewPlanView, error) {
	var empty ReviewPlanView
	if !isWorkActivityTransaction(tx) || authn.TenantIDFromContext(ctx) != tenant || reviewer <= 0 {
		return empty, ErrInvalidArgument
	}
	q := query.BindReviewPlans(tx, m.service.names)
	plan, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(planID)).First()
	if e != nil {
		return empty, e
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, e := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(plan.ProjectID), w.ID.Eq(plan.WorkItemID), w.ArchivedAt.IsNull()).First()
	if e != nil {
		return empty, e
	}
	if work.PausedAt != nil {
		return empty, ErrAssignmentState
	}
	if !replay && work.State != "PLAN_REVIEW" || work.CurrentPlanID == nil || *work.CurrentPlanID != plan.ID || !replay && plan.State != "PROPOSED" || plan.SourceKind != "AGENT" || plan.ContentHash != planDigest([]byte(plan.Content)) {
		return empty, ErrPlanNotApproved
	}
	if e = m.requireNoAndonOn(ctx, tx, work); e != nil {
		return empty, e
	}
	scoped := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: reviewer, SubjectType: authn.SubjectTypePrincipal})
	member, e := m.members.IsMemberActiveOn(scoped, tx, tenant, reviewer)
	if e != nil {
		return empty, e
	}
	if !member {
		return empty, ErrInvalidMember
	}
	if _, e = m.planReviewerOn(scoped, tx, work, plan, reviewer); e != nil {
		return empty, e
	}
	c, e := m.workPolicy(scoped, tx, tenant, work.ProjectID, "plan_approve", reviewer)
	if e != nil {
		return empty, e
	}
	scope, e := workScope(c, work.ProjectID, w.CreatedBy)
	if e != nil {
		return empty, e
	}
	base := w.WithContext(scoped).Where(w.TenantID.Eq(tenant), w.ID.Eq(work.ID))
	if scope != nil {
		base = base.Where(scope)
	}
	if _, e = base.First(); e != nil {
		return empty, ErrWorkForbidden
	}
	v, e := reviewPlanView(plan)
	if e != nil {
		return empty, e
	}
	if e = m.requirePlanBindingOn(ctx, tx, work, v.Content); e != nil {
		return empty, e
	}
	return v, nil
}

func (m *Module) queuePlanningReviewOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, plan *model.ReviewPlan) error {
	if m.planningRuntime.ReviewOn == nil || m.agents.ActiveByRoleOn == nil {
		return nil
	}
	candidates, e := m.agents.ActiveByRoleOn(ctx, tx, work.TenantID, "REVIEWER")
	if e != nil {
		return e
	}
	slices.SortFunc(candidates, func(a, b fleet.AgentIdentity) int {
		if a.PrincipalID < b.PrincipalID {
			return -1
		}
		if a.PrincipalID > b.PrincipalID {
			return 1
		}
		return 0
	})
	for _, candidate := range candidates {
		_, e = m.authorizePlanningReviewOn(ctx, tx, work.TenantID, plan.ID, candidate.PrincipalID, false)
		if errors.Is(e, ErrResultIndependent) || errors.Is(e, ErrWorkForbidden) || errors.Is(e, ErrInvalidMember) {
			continue
		}
		if e != nil {
			return e
		}
		id, e := m.planningRuntime.ReviewOn(ctx, tx, work.TenantID, plan.ID, candidate.PrincipalID)
		if e != nil {
			return e
		}
		if id > 0 {
			return nil
		}
	}
	return nil
}

func (m *Module) completePlanningReviewOn(ctx context.Context, tx *gorm.DB, tenant, planID, reviewer guid.ID, version int, hash, action, feedback string) error {
	plan, e := m.authorizePlanningReviewOn(ctx, tx, tenant, planID, reviewer, false)
	if e != nil {
		return e
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, e := w.WithContext(ctx).Select(w.AccountablePrincipalID).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(plan.ProjectID), w.ID.Eq(plan.WorkItemID), w.ArchivedAt.IsNull()).First()
	if e != nil {
		return e
	}
	scoped := authn.WithClaims(ctx, &authn.Claims{TenantID: tenant, PrincipalID: reviewer, SubjectType: authn.SubjectTypePrincipal})
	_, e = m.decideReviewPlanOn(scoped, tx, tenant, plan.ProjectID, plan.WorkItemID, plan.ID, reviewer, work.AccountablePrincipalID, version, hash, action, feedback)
	return e
}

// endPlanningWorkOn clears only the exact failed planner pointer. Review tasks
// have no execution pointer and leave their source plan awaiting another review.
func (m *Module) endPlanningWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, state string) error {
	if _, e := m.authorizePlanningWorkOn(ctx, tx, l, "CONTROL_RECEIPT"); e != nil {
		return e
	}
	q := query.BindWorkItems(tx, m.service.names)
	work, e := q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.ID.Eq(l.WorkItemID)).First()
	if e != nil {
		return e
	}
	before := work.State
	work.State = "CLAIMED"
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(l.TenantID), q.ID.Eq(work.ID)).Update(q.CurrentExecutionID, nil); e != nil {
		return e
	}
	return m.executionWorkChangedOn(ctx, tx, l, work, before, "PLANNING_"+state, time.Now().UTC().Truncate(time.Millisecond))
}
