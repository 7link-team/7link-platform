package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectBudget is a locked project fact. Callers must authorize the originating
// domain command before using this internal transaction capability.
type ProjectBudget struct {
	CapMicros   *int64
	SpentMicros int64
	NotifyPct   int
	CapVersion  int
	State       string
	Holds       string
}

type ProjectBudgets struct {
	LockOn  func(context.Context, *gorm.DB, guid.ID, guid.ID) (ProjectBudget, error)
	SpendOn func(context.Context, *gorm.DB, guid.ID, guid.ID, int64) error
}

func (m *Module) provideExecutionWork(s *module.Services) error {
	return module.Provide[ProjectBudgets](s, ProjectBudgets{LockOn: m.lockProjectBudgetOn, SpendOn: m.spendProjectBudgetOn})
}

func (m *Module) lockProjectBudgetOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (ProjectBudget, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return ProjectBudget{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ProjectBudget{}, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.service.names.Projects.IsZero() {
		return ProjectBudget{}, ErrUnavailable
	}
	q := query.BindProjects(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return ProjectBudget{}, err
	}
	return ProjectBudget{CapMicros: r.BudgetCapMicros, SpentMicros: r.BudgetSpentMicros, NotifyPct: r.BudgetNotifyPct, CapVersion: r.BudgetCapVersion, State: r.BudgetState, Holds: r.Holds}, nil
}

func (m *Module) spendProjectBudgetOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, amount int64) error {
	fact, err := m.lockProjectBudgetOn(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	if amount < 0 || fact.SpentMicros < 0 || amount > math.MaxInt64-fact.SpentMicros {
		return ErrInvalidArgument
	}
	q := query.BindProjects(tx, m.service.names)
	state := "NORMAL"
	spent := fact.SpentMicros + amount
	thresholds := []int{}
	if fact.CapMicros != nil {
		if *fact.CapMicros <= 0 || fact.NotifyPct < 50 || fact.NotifyPct > 95 {
			return ErrInvalidArgument
		}
		left := new(big.Int).Mul(big.NewInt(spent), big.NewInt(100))
		right := new(big.Int).Mul(big.NewInt(*fact.CapMicros), big.NewInt(int64(fact.NotifyPct)))
		if left.Cmp(right) >= 0 {
			state = "NOTIFIED"
			if fact.State == "NORMAL" {
				thresholds = append(thresholds, fact.NotifyPct)
			}
		}
		if spent >= *fact.CapMicros {
			state = "EXHAUSTED"
			if fact.State != "EXHAUSTED" {
				thresholds = append(thresholds, 100)
			}
		}
	}
	holds := []map[string]json.RawMessage{}
	if err = json.Unmarshal([]byte(fact.Holds), &holds); err != nil {
		return ErrUnavailable
	}
	if state == "EXHAUSTED" {
		found := false
		for _, hold := range holds {
			if string(hold["kind"]) == `"BUDGET"` {
				found = true
			}
		}
		if !found {
			since, _ := json.Marshal(time.Now().UTC().Truncate(time.Millisecond))
			holds = append(holds, map[string]json.RawMessage{"kind": json.RawMessage(`"BUDGET"`), "since": since})
		}
	}
	encoded, err := json.Marshal(holds)
	if err != nil {
		return err
	}
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).UpdateSimple(q.BudgetSpentMicros.Value(spent), q.BudgetState.Value(state), q.Holds.Value(string(encoded))); err != nil {
		return err
	}
	for _, threshold := range thresholds {
		if m.service.events == nil {
			return ErrUnavailable
		}
		if m.ids == nil || m.service.audit == nil {
			return ErrUnavailable
		}
		id, e := m.ids.Next()
		if e != nil {
			return e
		}
		if id <= 0 {
			return ErrUnavailable
		}
		kind := "THRESHOLD_NOTIFY"
		if threshold == 100 {
			kind = "THRESHOLD_EXHAUSTED"
		}
		row := &model.ProjectBudgetEvent{TenantID: tenant, ID: guid.ID(id), ProjectID: project, Kind: kind, ThresholdLock: &kind, ThresholdPct: &threshold, AuthoritySource: "budget:threshold", CapMicros: fact.CapMicros, SpentMicros: spent, NotifyPct: fact.NotifyPct, CapVersion: fact.CapVersion, State: state, Reason: "", OccurredAt: time.Now().UTC().Truncate(time.Millisecond)}
		if e = query.BindBudgetEvents(tx, m.service.names).WithContext(ctx).Create(row); e != nil {
			return e
		}
		event := audit.NewDomainEvent(ctx, tenant, "delivery.budget_threshold_reached", "project", project)
		event.Detail["threshold_pct"], event.Detail["cap_version"], event.Detail["spent_micros"] = threshold, fact.CapVersion, spent
		if e = m.service.audit(ctx, tx, event); e != nil {
			return e
		}
		_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "budget.threshold_reached", Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(fact.CapVersion), Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "budget:threshold"}, Data: map[string]any{"threshold_pct": threshold, "cap_micros": fact.CapMicros, "spent_micros": spent, "cap_version": fact.CapVersion}})
		if err != nil {
			return err
		}
	}
	return nil
}

// ExecutionWorkLink is an internal capability input, never an authorization token.
// Execution locks all command principals before invoking this provider; it must
// retain those locks through commit. No extra principal lock follows project locks.
type ExecutionWorkLink struct {
	ReviewPlanID                                                                     guid.ID
	TenantID, ProjectID, WorkItemID, AssignmentID, ExecutionID, ActorID, InitiatorID guid.ID
	ParentExecutionID, SourceDecisionID                                              guid.ID
	SourceHeadCommit, SourceFeedback                                                 string
}
type ExecutionArtifact struct {
	BindingID                  guid.ID
	SpaceID, FileID, VersionID guid.ID
	FileVersion                int64
	CriterionID                *guid.ID
	Coverage, Note, SHA256     string
}
type ExecutionEvidence struct {
	Artifact    *ExecutionArtifact `json:"artifact,omitempty"`
	ID          guid.ID            `json:"id"`
	Kind        string             `json:"kind"`
	Title       string             `json:"title"`
	ContentHash string             `json:"content_hash"`
}
type ExecutionCompletion struct {
	State, Reason, HeadCommit string
	Evidence                  []ExecutionEvidence
}

// ExecutionWork exposes the same immutable result identity and independent
// check/acceptance pipeline as manual delivery. Claim ID equals execution ID;
// the immutable completion event carries origin, head and evidence hashes.
type ExecutionWork struct {
	ProgressStopOn          func(context.Context, *gorm.DB, ExecutionWorkLink, string) error
	AuthorizeVisualReviewOn func(context.Context, *gorm.DB, ExecutionWorkLink, guid.ID, int, guid.ID, string) error
	PrincipalsOn            func(context.Context, *gorm.DB, ExecutionWorkLink) ([]guid.ID, error)
	AuthorizeOn             func(context.Context, *gorm.DB, ExecutionWorkLink, string) error
	StartedOn               func(context.Context, *gorm.DB, ExecutionWorkLink) error
	FinishedOn              func(context.Context, *gorm.DB, ExecutionWorkLink, ExecutionCompletion) error
}

// RealtimeTopics applies the same current project-read row policy as the owner
// list route. It grants subscription only; event payloads remain invalidations.
type RealtimeTopics struct {
	Authorize     func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
	AuthorizeWork func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
}

func (m *Module) authorizeRealtimeWork(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	if collection || tenant <= 0 || principal <= 0 || id <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal {
		return false, nil
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.workPolicy == nil || m.service == nil {
		return false, ErrUnavailable
	}
	allowed := false
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, m.service.names)
		row, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ArchivedAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		c, err := m.workPolicy(ctx, tx, tenant, row.ProjectID, "work_item_read", principal)
		if err != nil {
			return err
		}
		scope, err := workScope(c, row.ProjectID, w.CreatedBy)
		if err != nil {
			return err
		}
		base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(id), w.ProjectID.Eq(row.ProjectID), w.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		_, err = base.First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		allowed = err == nil
		return err
	})
	return allowed, err
}

func (m *Module) authorizeRealtimeProject(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	if tenant <= 0 || principal <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal || (!collection && id <= 0) {
		return false, nil
	}
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil || m.listConstraint == nil || m.service == nil {
		return false, ErrUnavailable
	}
	db := m.runtime.Database().Write()
	if collection {
		page, err := m.listProjectRecords(ctx, db, projectListInput{Limit: 1})
		return len(page.Records) > 0, err
	}
	c, err := m.listConstraint(ctx, tenant, "project_read", principal)
	if err != nil || !c.AllowsEntityRows(id) {
		return false, err
	}
	p := query.BindProjects(db, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(id), p.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.AllowAll || slices.Contains(c.ResourceIDs, id.String()) || slices.Contains(c.OwnerIDs, row.CreatedBy.String()), nil
}

func (m *Module) provideExecution(s *module.Services) error {
	if err := m.provideCodeMerge(s); err != nil {
		return err
	}
	if err := m.provideExecutionWork(s); err != nil {
		return err
	}
	if err := module.Provide[ProjectNotificationAuthority](s, m.projectNotificationAuthorityOn); err != nil {
		return err
	}
	if err := module.Provide[WorkNotifications](s, WorkNotifications{FactsOn: m.workNotificationFactsOn, ReviewEligibleOn: m.workNotificationReviewEligibleOn, RequirementExecutorOn: m.requirementNotificationExecutorOn, ActiveEngineerOn: m.activeNotificationEngineerOn, ProjectModeOn: m.notificationProjectModeOn}); err != nil {
		return err
	}
	if err := module.Provide[RealtimeTopics](s, RealtimeTopics{Authorize: m.authorizeRealtimeProject, AuthorizeWork: m.authorizeRealtimeWork}); err != nil {
		return err
	}
	return module.Provide[ExecutionWork](s, ExecutionWork{ProgressStopOn: m.progressStopOn, AuthorizeVisualReviewOn: m.authorizeVisualReviewOn, PrincipalsOn: m.executionPrincipalsOn, AuthorizeOn: m.authorizeExecutionWorkOn, StartedOn: m.startExecutionWorkOn, FinishedOn: m.finishExecutionWorkOn})
}

// ProjectNotificationAuthority evaluates current project-read authority with
// delivery-owned row facts in the consumer's transaction.
type ProjectNotificationAuthority func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)

// WorkNotificationFacts exposes delivery-owned audience facts without exporting
// storage or treating an event actor as the result's executor.
type WorkNotificationFacts struct {
	ProjectID, ExecutorID, AssignerID guid.ID
	Risk                              string
}

type WorkNotifications struct {
	FactsOn               func(context.Context, *gorm.DB, guid.ID, guid.ID) (WorkNotificationFacts, bool, error)
	ReviewEligibleOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	RequirementExecutorOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	ActiveEngineerOn      func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	ProjectModeOn         func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error)
}

func (m *Module) requirementNotificationExecutorOn(ctx context.Context, tx *gorm.DB, tenant, requirement, principal guid.ID) (bool, error) {
	if tx == nil || tenant <= 0 || requirement <= 0 || principal <= 0 || m == nil || m.service == nil {
		return false, ErrInvalidArgument
	}
	a := query.BindAssignments(tx, m.service.names)
	w := query.BindWorkItems(tx, m.service.names)
	count, err := a.WithContext(ctx).Join(w, w.TenantID.EqCol(a.TenantID), w.ProjectID.EqCol(a.ProjectID), w.ID.EqCol(a.WorkItemID)).Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(principal), a.AssigneeKind.Eq("HUMAN"), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), w.RequirementID.Eq(requirement), w.ArchivedAt.IsNull()).Count()
	return count > 0, err
}

func (m *Module) activeNotificationEngineerOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	if tx == nil || tenant <= 0 || project <= 0 || principal <= 0 || m == nil || m.service == nil {
		return false, ErrInvalidArgument
	}
	a := query.BindAssignments(tx, m.service.names)
	w := query.BindWorkItems(tx, m.service.names)
	count, err := a.WithContext(ctx).Join(w, w.TenantID.EqCol(a.TenantID), w.ProjectID.EqCol(a.ProjectID), w.ID.EqCol(a.WorkItemID)).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.AssigneePrincipalID.Eq(principal), a.AssigneeKind.Eq("HUMAN"), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), w.State.Eq("IN_PROGRESS"), w.ArchivedAt.IsNull()).Count()
	return count > 0, err
}

func (m *Module) notificationProjectModeOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (string, error) {
	if tx == nil || tenant <= 0 || project <= 0 || m == nil || m.service == nil {
		return "", ErrInvalidArgument
	}
	p := query.BindProjects(tx, m.service.names)
	row, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.ArchivedAt.IsNull()).First()
	if err != nil {
		return "", err
	}
	return row.TeamMode, nil
}

func (m *Module) workNotificationFactsOn(ctx context.Context, tx *gorm.DB, tenant, workID guid.ID) (WorkNotificationFacts, bool, error) {
	var out WorkNotificationFacts
	if tx == nil || tenant <= 0 || workID <= 0 || m == nil || m.service == nil {
		return out, false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.ProjectID = work.ProjectID
	out.Risk = work.Risk
	a := query.BindAssignments(tx, m.service.names)
	assignment, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(work.ProjectID), a.WorkItemID.Eq(workID), a.Role.Eq("EXECUTOR")).Order(a.ID.Desc()).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, false, err
	}
	if assignment != nil {
		if assignment.AssigneeKind == "HUMAN" {
			out.ExecutorID = assignment.AssigneePrincipalID
		}
		if assignment.CreatedBy != nil {
			out.AssignerID = *assignment.CreatedBy
		}
	}

	return out, true, nil
}

func (m *Module) workNotificationReviewEligibleOn(ctx context.Context, tx *gorm.DB, tenant, workID, principal guid.ID) (bool, error) {
	if tx == nil || tenant <= 0 || workID <= 0 || principal <= 0 || m == nil || m.service == nil {
		return false, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	work, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if work.CurrentClaimID == nil {
		return false, nil
	}
	result, err := m.resultOn(ctx, tx, tenant, work.ProjectID, workID, *work.CurrentClaimID)
	if err != nil {
		return false, err
	}
	_, err = m.resultAuthorityOn(ctx, tx, work, result, principal, "decision_approve")
	if errors.Is(err, ErrWorkForbidden) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) projectNotificationAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	if m == nil || m.service == nil || m.workPolicy == nil || tx == nil || tenant <= 0 || project <= 0 || principal <= 0 {
		return false, ErrUnavailable
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "project_read", principal)
	if err != nil || !c.AllowsEntityRows(project) {
		return false, err
	}
	q := query.BindProjects(tx, m.service.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return c.AllowAll || slices.Contains(c.ResourceIDs, project.String()) || r.CreatedBy != nil && slices.Contains(c.OwnerIDs, r.CreatedBy.String()), nil
}
func (m *Module) executionWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, operation string) (*model.WorkItem, *model.Assignment, error) {
	planned := l.InitiatorID == 0 && l.ReviewPlanID > 0
	system := planned || l.InitiatorID == 0 && l.SourceDecisionID > 0 && l.ParentExecutionID > 0
	if tx == nil || tx.Statement == nil || l.TenantID <= 0 || l.ProjectID <= 0 || l.WorkItemID <= 0 || l.AssignmentID <= 0 || l.ExecutionID <= 0 || (!system && (l.ActorID <= 0 || l.InitiatorID <= 0)) || authn.TenantIDFromContext(ctx) != l.TenantID || authn.PrincipalIDFromContext(ctx) != l.ActorID {
		return nil, nil, ErrWorkForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.principals.ActiveHumanByID == nil || m.members == nil || m.ids == nil || m.service.audit == nil || m.service.events == nil {
		return nil, nil, ErrUnavailable
	}
	c, err := m.executionAuthorityOn(ctx, tx, l, system, operation)
	if err != nil {
		return nil, nil, err
	}
	p := query.BindProjects(tx, m.service.names)
	project, err := p.WithContext(ctx).Where(p.TenantID.Eq(l.TenantID), p.ProjectID.Eq(l.ProjectID), p.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return nil, nil, err
	}
	if project.Stage != "ACTIVE" {
		return nil, nil, ErrWorkProjectReadOnly
	}
	if slices.Contains([]string{"CREATE", "DISPATCH", "RECOVER", "START"}, operation) {
		if project.BudgetState == "EXHAUSTED" {
			return nil, nil, ErrWorkProjectReadOnly
		}
		var holds []struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(project.Holds), &holds) != nil {
			return nil, nil, ErrUnavailable
		}
		for _, hold := range holds {
			if slices.Contains([]string{"BUDGET", "MANUAL", "ESCALATION"}, hold.Kind) {
				return nil, nil, ErrWorkProjectReadOnly
			}
		}
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, l.ProjectID, w.CreatedBy)
	if err != nil {
		return nil, nil, err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID), w.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"})
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	work, err := stmt.First()
	if err != nil {
		return nil, nil, err
	}
	if work.PausedAt != nil || work.IsContainer {
		return nil, nil, ErrAssignmentState
	}
	if system && !planned && (work.BaselineStale || work.CurrentClaimID == nil || *work.CurrentClaimID != l.ParentExecutionID) {
		return nil, nil, ErrResultState
	}
	if system && !planned {
		parent, e := m.resultOn(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, l.ParentExecutionID)
		if e != nil {
			return nil, nil, e
		}
		if parent.BaselineVersionNo != work.BaselineVersionNo {
			return nil, nil, ErrResultState
		}
	}
	a := query.BindAssignments(tx, m.service.names)
	assignment, err := a.WithContext(ctx).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.AssignmentID), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE")).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return nil, nil, err
	}
	heavy := assignment.EnsemblePlanID != nil
	if heavy {
		if err = m.checkHeavyExecutionOn(ctx, tx, l, assignment.EnsemblePlanID); err != nil {
			return nil, nil, err
		}
	}
	if assignment.AssigneeKind == "HUMAN" {
		if assignment.AssigneePrincipalID != l.InitiatorID && !heavy {
			return nil, nil, ErrAssignmentHolder
		}
	} else if assignment.AssigneeKind == "AGENT" {
		if m.agents.ByPrincipalOn == nil {
			return nil, nil, ErrUnavailable
		}
		agent, found, e := m.agents.ByPrincipalOn(ctx, tx, l.TenantID, assignment.AssigneePrincipalID)
		if e != nil {
			return nil, nil, e
		}
		if !found || agent.State != "ACTIVE" || assignment.AgentID == nil || agent.ID != *assignment.AgentID || !slices.Contains(agent.Roles, "EXECUTOR") {
			return nil, nil, ErrAssignmentHolder
		}
		member, e := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, assignment.AssigneePrincipalID)
		if e != nil {
			return nil, nil, e
		}
		if !member {
			return nil, nil, ErrAssignmentHolder
		}
	} else {
		return nil, nil, ErrAssignmentHolder
	}
	now := time.Now().UTC()
	if assignment.LeaseExpiresAt == nil || !assignment.LeaseExpiresAt.After(now) {
		return nil, nil, ErrAssignmentState
	}
	if operation == "CREATE" {
		if work.State != "CLAIMED" && work.State != "REWORK" && !(heavy && work.State == "IN_PROGRESS") && !(system && work.CurrentExecutionID != nil && *work.CurrentExecutionID == l.ExecutionID && work.State == "IN_PROGRESS") {
			return nil, nil, ErrAssignmentState
		}
		if work.CurrentExecutionID != nil && *work.CurrentExecutionID != l.ExecutionID {
			return nil, nil, ErrAssignmentHeld
		}
	} else if !heavy && (work.CurrentExecutionID == nil || *work.CurrentExecutionID != l.ExecutionID) {
		return nil, nil, ErrAssignmentHolder
	}
	if slices.Contains([]string{"CREATE", "DISPATCH", "RECOVER", "START"}, operation) {
		if err = m.requireNoAndonOn(ctx, tx, work); err != nil {
			return nil, nil, err
		}
		if err = m.requireCurrentUpstreamOn(ctx, tx, work); err != nil {
			return nil, nil, err
		}
		if err = m.requireApprovedReviewPlanOn(ctx, tx, work); err != nil {
			return nil, nil, err
		}
	}
	return work, assignment, nil
}
func (m *Module) authorizeExecutionWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, operation string) error {
	if operation == "CONTROL_RECEIPT" {
		_, _, err := m.executionStopWorkOn(ctx, tx, l, false)
		return err
	}
	if operation == "CANCEL" {
		_, _, err := m.executionStopWorkOn(ctx, tx, l, true)
		return err
	}
	if !slices.Contains([]string{"CREATE", "DISPATCH", "RECEIPT", "CANCEL", "RECOVER"}, operation) {
		return ErrInvalidArgument
	}
	work, assignment, err := m.executionWorkOn(ctx, tx, l, operation)
	if err != nil {
		return err
	}
	if operation != "CREATE" || work.CurrentExecutionID != nil || assignment.EnsemblePlanID != nil {
		return nil
	}
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID), w.Version.Eq(work.Version), w.CurrentExecutionID.IsNull()).Update(w.CurrentExecutionID, l.ExecutionID)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrWorkVersion
	}
	return nil
}
func (m *Module) startExecutionWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink) error {
	work, _, err := m.executionWorkOn(ctx, tx, l, "START")
	if err != nil {
		return err
	}
	if work.State == "IN_PROGRESS" {
		return nil
	}
	if work.State != "CLAIMED" && work.State != "REWORK" {
		return ErrAssignmentState
	}
	before := work.State
	work.State = "IN_PROGRESS"
	return m.executionWorkChangedOn(ctx, tx, l, work, before, "EXECUTION_STARTED", time.Now().UTC().Truncate(time.Millisecond))
}
func (m *Module) finishExecutionWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, in ExecutionCompletion) error {
	var work *model.WorkItem
	var assignment *model.Assignment
	var err error
	if in.State == "CANCELLED" { //nolint:misspell // Spec freezes this protocol state spelling.
		work, assignment, err = m.executionStopWorkOn(ctx, tx, l, false)
	} else {
		work, assignment, err = m.executionWorkOn(ctx, tx, l, "RECEIPT")
	}
	if err != nil {
		return err
	}
	if assignment.EnsemblePlanID != nil {
		return m.checkHeavyExecutionOn(ctx, tx, l, assignment.EnsemblePlanID)
	}
	if !slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED"}, in.State) { //nolint:misspell // Spec freezes this protocol state spelling.
		return ErrInvalidArgument
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if in.State == "SUCCEEDED" {
		if work.State != "IN_PROGRESS" || len(in.Evidence) == 0 || len(in.Evidence) > 50 {
			return ErrResultState
		}
		if len(in.HeadCommit) != 40 && len(in.HeadCommit) != 64 {
			return ErrInvalidArgument
		}
		if _, err = hex.DecodeString(in.HeadCommit); err != nil {
			return ErrInvalidArgument
		}
		seen := map[guid.ID]bool{}
		for _, e := range in.Evidence {
			if e.ID <= 0 || seen[e.ID] || len(e.ContentHash) != 64 || strings.TrimSpace(e.Title) == "" {
				return ErrInvalidArgument
			}
			if _, err = hex.DecodeString(e.ContentHash); err != nil {
				return ErrInvalidArgument
			}
			seen[e.ID] = true
		}
		r := query.BindResultVersions(tx, m.service.names)
		prior, e := r.WithContext(ctx).Where(r.TenantID.Eq(l.TenantID), r.ID.Eq(l.ExecutionID)).First()
		if e == nil {
			if prior.ProjectID == l.ProjectID && prior.WorkItemID == l.WorkItemID && work.CurrentClaimID != nil && *work.CurrentClaimID == prior.ID {
				return nil
			}
			return ErrResultState
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		sequence := 1
		if work.CurrentClaimID != nil {
			previous, e := m.resultOn(ctx, tx, l.TenantID, l.ProjectID, l.WorkItemID, *work.CurrentClaimID)
			if e != nil {
				return e
			}
			sequence = previous.Sequence + 1
		}
		row := &model.ResultVersion{VerificationPlan: work.VerificationPlan, SourceExecutionID: &l.ExecutionID, HeadCommit: in.HeadCommit, TenantID: l.TenantID, ID: l.ExecutionID, ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, Sequence: sequence, Summary: "Execution " + l.ExecutionID.String() + " completed at " + in.HeadCommit, ActorID: l.InitiatorID, BaselineVersionNo: work.BaselineVersionNo, PreviousResultID: work.CurrentClaimID, CreatedAt: now}
		if err = r.WithContext(ctx).Create(row); err != nil {
			return err
		}
		for _, e := range in.Evidence {
			if e.Artifact == nil {
				continue
			}
			if err = m.pinExecutionEvidenceOn(ctx, tx, l, row.ID, e); err != nil {
				return err
			}
		}

		work.CurrentClaimID = &row.ID
		work.AcceptedAt = nil
		work.AcceptedDecisionID = nil
		work.DefectStage = nil
	}
	before := work.State
	if in.State == "SUCCEEDED" {
		work.State = "NEEDS_HUMAN_REVIEW"
		if work.InternalQARequired {
			var verificationPlan []workVerificationStep
			if err = json.Unmarshal([]byte(work.VerificationPlan), &verificationPlan); err != nil {
				return err
			}
			for _, step := range verificationPlan {
				if step.Method != "HUMAN" {
					work.State = "INTERNAL_QA"
					break
				}
			}
		}
		if (work.Type == "BUG" || work.Type == "INCIDENT") && in.HeadCommit != "" {
			work.State = "VERIFYING"
		}
	} else if slices.Contains([]string{"CLAIMED", "IN_PROGRESS", "REWORK"}, work.State) {
		work.State = "CLAIMED"
	}
	w := query.BindWorkItems(tx, m.service.names)
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(l.WorkItemID), w.CurrentExecutionID.Eq(l.ExecutionID)).UpdateSimple(w.CurrentExecutionID.Null()); err != nil {
		return err
	}
	// Assignment helper records MACHINE provenance and the state transition; the
	// exact result pointer is changed separately in this same locked transaction.
	if _, err = w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ID.Eq(l.WorkItemID)).Select(w.CurrentClaimID, w.AcceptedAt, w.AcceptedDecisionID, w.DefectStage).Updates(work); err != nil {
		return err
	}
	if err = m.executionWorkChangedOn(ctx, tx, l, work, before, "EXECUTION_"+in.State, now); err != nil {
		return err
	}
	detail := map[string]any{"origin": "EXECUTION", "execution_id": l.ExecutionID.String(), "state": in.State, "reason": in.Reason, "head_commit": in.HeadCommit, "evidence": in.Evidence}
	if in.State == "SUCCEEDED" {
		detail["result_id"] = l.ExecutionID.String()
	}
	event := audit.NewDomainEvent(ctx, l.TenantID, "delivery.execution_finished", "work_item", l.WorkItemID)
	event.Detail = detail
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: l.TenantID, ProjectID: l.ProjectID, Type: "work_item.state_changed", Actor: events.Actor{Kind: "MACHINE", ID: l.ActorID, AuthoritySource: "execution:" + l.ExecutionID.String()}, Subject: events.Subject{Kind: "work_item", ID: l.WorkItemID}, SubjectVersion: int64(work.Version), Data: detail})
	return err
}

// Assignment holder identity is immutable. Read it before business locks, then
// revalidate the exact assignment after those principal rows are locked.
func (m *Module) executionPrincipalsOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink) ([]guid.ID, error) {
	if tx == nil || l.TenantID <= 0 || l.AssignmentID <= 0 || m == nil || m.service == nil {
		return nil, ErrInvalidArgument
	}
	a := query.BindAssignments(tx, m.service.names)
	row, err := a.WithContext(ctx).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.AssignmentID)).First()
	if err != nil {
		return nil, err
	}
	return []guid.ID{row.AssigneePrincipalID}, nil
}

func (m *Module) pinExecutionEvidenceOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, resultID guid.ID, e ExecutionEvidence) error {
	a := e.Artifact
	if a == nil || a.SpaceID <= 0 || a.FileID <= 0 || a.VersionID <= 0 || a.FileVersion < 1 || !slices.Contains([]string{"COVERED", "PARTIAL", "NOT_APPLICABLE"}, a.Coverage) || len(a.SHA256) != 64 || len(a.Note) > 2000 || (a.Coverage == "NOT_APPLICABLE" && strings.TrimSpace(a.Note) == "") {
		return ErrInvalidArgument
	}
	if m.resultFiles == nil {
		return ErrUnavailable
	}
	if a.CriterionID != nil {
		c := query.BindWorkCriteria(tx, m.service.names)
		if _, err := c.WithContext(ctx).Where(c.TenantID.Eq(l.TenantID), c.ProjectID.Eq(l.ProjectID), c.WorkItemID.Eq(l.WorkItemID), c.ID.Eq(*a.CriterionID)).First(); err != nil {
			return err
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return err
	}
	bid, err := m.ids.Next()
	if err != nil {
		return err
	}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil {
		return ErrWorkForbidden
	}
	scoped := *claims
	scoped.EntityID = l.ProjectID
	ctx = authn.WithClaims(ctx, &scoped)
	input := files.ResourceBindingInput{ID: guid.ID(bid), SpaceID: a.SpaceID, FileID: a.FileID, VersionID: &a.VersionID, ExpectedFileVersion: a.FileVersion, Purpose: resultEvidencePurpose, ResourceKind: "delivery_result", ResourceID: resultID, Slot: "evidence." + guid.ID(id).String(), ActorID: l.ActorID}
	var pin files.PinnedResource
	if a.BindingID > 0 {
		references, ok := m.resultFiles.(files.ExecutionArtifactReferences)
		if !ok {
			return ErrUnavailable
		}
		pin, err = references.PinReferenceOn(ctx, tx, a.BindingID, l.ExecutionID, input)
	} else {
		pin, err = m.resultFiles.PinOn(ctx, tx, input)
	}
	if err != nil {
		return err
	}
	if pin.SHA256 != a.SHA256 {
		return ErrInvalidArgument
	}
	return query.BindResultEvidence(tx, m.service.names).WithContext(ctx).Create(&model.ResultEvidence{TenantID: l.TenantID, ID: guid.ID(id), ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, ResultID: resultID, BindingID: pin.BindingID, SpaceID: pin.SpaceID, FileID: pin.FileID, VersionID: pin.VersionID, CriterionID: a.CriterionID, Coverage: a.Coverage, Note: a.Note, Name: pin.Name, SHA256: pin.SHA256})
}

// The private Files stamp is minted only after execution rechecks its exact
// current runner fence. It permits attaching that execution's own artifact;
// ordinary reads still go through the owner's current work/file permissions.
func (m *Module) executionWorkAuthorizeResultResource(ctx context.Context, tx *gorm.DB, id guid.ID, owner files.ResourceOwner) error {
	entity := authn.EntityIDFromContext(ctx)
	if owner.Scope == "entity" && owner.ID == entity && files.ExecutionReferenceAuthorized(ctx, id, entity) {
		r := query.BindResultVersions(tx, m.service.names)
		_, err := r.WithContext(ctx).Where(r.TenantID.Eq(authn.TenantIDFromContext(ctx)), r.ProjectID.Eq(entity), r.ID.Eq(id)).First()
		return err
	}
	return m.authorizeResultResource(ctx, tx, id, owner)
}

// Execution progress does not reassign the work. In particular failure returning
// to CLAIMED must not emit the Human self-claim helper's work_item.assigned event.
func (m *Module) executionWorkChangedOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, work *model.WorkItem, before, reason string, now time.Time) error {
	if err := m.recordReportL3On(ctx, tx, work, before, now); err != nil {
		return err
	}
	expected := work.Version
	work.Version++
	work.UpdatedAt = now
	work.UpdatedBy = &l.ActorID
	work.StateChangedAt = now
	w := query.BindWorkItems(tx, m.service.names)
	changed, err := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID), w.Version.Eq(expected)).Select(w.State, w.Version, w.UpdatedAt, w.UpdatedBy, w.StateChangedAt).Updates(work)
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
	source := "execution:" + l.ExecutionID.String()
	transitionRef := source + ":" + strconv.Itoa(work.Version)
	transition := &model.WorkItemTransition{TenantID: l.TenantID, ID: guid.ID(id), ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, FromState: &before, ToState: work.State, Trigger: "SYSTEM", ActorKind: "SYSTEM", AuthoritySource: source, ReasonCode: reason, SourceRef: &transitionRef, VersionAfter: work.Version, CreatedAt: now}
	if err = query.BindWorkTransitions(tx, m.service.names).WithContext(ctx).Create(transition); err != nil {
		return err
	}
	event := audit.NewDomainEvent(ctx, l.TenantID, "delivery.execution_progress", "work_item", l.WorkItemID)
	event.Detail["execution_id"] = l.ExecutionID.String()
	event.Detail["from"] = before
	event.Detail["to"] = work.State
	event.Detail["reason_code"] = reason
	if err = m.service.audit(ctx, tx, event); err != nil {
		return err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: l.TenantID, ProjectID: l.ProjectID, Type: "work_item.state_changed", Actor: events.Actor{Kind: "MACHINE", ID: l.ActorID, AuthoritySource: source}, Subject: events.Subject{Kind: "work_item", ID: l.WorkItemID}, SubjectVersion: int64(work.Version), Data: map[string]any{"from": before, "to": work.State, "reason_code": reason, "execution_id": l.ExecutionID.String()}})
	return err
}

// Stop authority belongs to the current operator; a revoked initiator or a
// blocked project must not prevent stopping further spend. Runner-confirmed
// cancellation uses execution's current machine/session fence instead.
func (m *Module) executionStopWorkOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, operator bool) (*model.WorkItem, *model.Assignment, error) {
	if tx == nil || tx.Statement == nil || l.TenantID <= 0 || l.ProjectID <= 0 || l.WorkItemID <= 0 || l.AssignmentID <= 0 || l.ExecutionID <= 0 || l.ActorID <= 0 || authn.TenantIDFromContext(ctx) != l.TenantID || authn.PrincipalIDFromContext(ctx) != l.ActorID {
		return nil, nil, ErrWorkForbidden
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.workPolicy == nil || m.principals.ActiveHumanByID == nil || m.members == nil {
		return nil, nil, ErrUnavailable
	}
	c := authz.DataConstraint{AllowAll: true}
	if operator {
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, l.ActorID)
		if err != nil {
			return nil, nil, err
		}
		if !active || human.ID != l.ActorID {
			return nil, nil, ErrWorkForbidden
		}
		member, err := m.members.IsMemberActiveOn(ctx, tx, l.TenantID, l.ActorID)
		if err != nil {
			return nil, nil, err
		}
		if !member {
			return nil, nil, ErrWorkForbidden
		}
		c, err = m.workPolicy(ctx, tx, l.TenantID, l.ProjectID, "execution_stop", l.ActorID)
		if err != nil {
			return nil, nil, err
		}
	}
	p := query.BindProjects(tx, m.service.names)
	if _, err := p.WithContext(ctx).Where(p.TenantID.Eq(l.TenantID), p.ProjectID.Eq(l.ProjectID)).Clauses(clause.Locking{Strength: "UPDATE"}).First(); err != nil {
		return nil, nil, err
	}
	w := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, l.ProjectID, w.CreatedBy)
	if err != nil {
		return nil, nil, err
	}
	stmt := w.WithContext(ctx).Where(w.TenantID.Eq(l.TenantID), w.ProjectID.Eq(l.ProjectID), w.ID.Eq(l.WorkItemID)).Clauses(clause.Locking{Strength: "UPDATE"})
	if scope != nil {
		stmt = stmt.Where(scope)
	}
	work, err := stmt.First()
	if err != nil {
		return nil, nil, err
	}
	a := query.BindAssignments(tx, m.service.names)
	assignment, err := a.WithContext(ctx).Where(a.TenantID.Eq(l.TenantID), a.ProjectID.Eq(l.ProjectID), a.WorkItemID.Eq(l.WorkItemID), a.ID.Eq(l.AssignmentID)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if err != nil {
		return nil, nil, err
	}
	if assignment.EnsemblePlanID != nil {
		err = m.checkHeavyExecutionOn(ctx, tx, l, assignment.EnsemblePlanID)
	} else if work.CurrentExecutionID == nil || *work.CurrentExecutionID != l.ExecutionID {
		err = ErrAssignmentHolder
	}
	return work, assignment, err
}
