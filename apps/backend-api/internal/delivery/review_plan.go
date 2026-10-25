package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode/utf8"
)

type PlanCoverage struct {
	CriterionID  guid.ID `json:"criterion_id"`
	Approach     string  `json:"approach" maxLength:"2000"`
	Verification string  `json:"verification" maxLength:"2000"`
}
type ReviewPlanContent struct {
	Producer            *PlanProducer      `json:"producer,omitempty" readOnly:"true"`
	Binding             *ReviewPlanBinding `json:"binding,omitempty"`
	Files               []string           `json:"files" maxItems:"100"`
	CriteriaCoverage    []PlanCoverage     `json:"criteria_coverage" maxItems:"100"`
	Risks               []string           `json:"risks" maxItems:"100"`
	OutOfScopeConfirmed bool               `json:"out_of_scope_confirmed"`
}

// Binding is computed by the server and covered by ContentHash. Only hashes
// leave the server; a plan cannot expose hidden acceptance case contents.
type ReviewPlanBinding struct {
	BaselineVersion  int      `json:"baseline_version"`
	RequirementID    *guid.ID `json:"requirement_id"`
	BaselineHash     string   `json:"baseline_hash"`
	SuiteHash        string   `json:"suite_hash"`
	ContractHash     string   `json:"contract_hash"`
	CriteriaHash     string   `json:"criteria_hash"`
	VerificationHash string   `json:"verification_hash"`
}

func planDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (m *Module) reviewPlanBindingOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem) (*ReviewPlanBinding, error) {
	if work.BaselineStale {
		return nil, ErrPlanNotApproved
	}
	b := &ReviewPlanBinding{BaselineVersion: work.BaselineVersionNo, RequirementID: work.RequirementID}
	c := query.BindWorkContracts(tx, m.service.names)
	contract, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(work.TenantID), c.ProjectID.Eq(work.ProjectID), c.WorkItemID.Eq(work.ID)).Order(c.VersionNo.Desc()).First()
	if err != nil {
		return nil, err
	}
	if planDigest([]byte(contract.Snapshot)) != contract.ContentHash {
		return nil, ErrPlanNotApproved
	}
	b.ContractHash = contract.ContentHash
	q := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Order(q.Position, q.ID).Find()
	if err != nil {
		return nil, err
	}
	type criterionBinding struct {
		ID           guid.ID
		Position     int
		Text, Method string
	}
	frozen := make([]criterionBinding, 0, len(criteria))
	for _, v := range criteria {
		frozen = append(frozen, criterionBinding{v.ID, v.Position, v.Text, v.VerificationMethod})
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		return nil, err
	}
	b.CriteriaHash = planDigest(raw)
	var verification any
	if json.Unmarshal([]byte(work.VerificationPlan), &verification) != nil {
		return nil, ErrUnavailable
	}
	raw, err = json.Marshal(verification)
	if err != nil {
		return nil, err
	}
	b.VerificationHash = planDigest(raw)
	if work.RequirementID != nil {
		r := query.BindRequirements(tx, m.service.names)
		requirement, e := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(work.TenantID), r.ProjectID.Eq(work.ProjectID), r.ID.Eq(*work.RequirementID), r.ArchivedAt.IsNull()).First()
		if e != nil {
			return nil, e
		}
		if requirement.Status != "BASELINED" || requirement.CurrentVersionNo != work.BaselineVersionNo {
			return nil, ErrPlanNotApproved
		}
		v := query.BindRequirementVersions(tx, m.service.names)
		baseline, e := v.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(v.TenantID.Eq(work.TenantID), v.ProjectID.Eq(work.ProjectID), v.RequirementID.Eq(*work.RequirementID), v.VersionNo.Eq(work.BaselineVersionNo)).First()
		if e != nil {
			return nil, e
		}
		if _, e = frozenSuiteCaseCount(baseline); e != nil {
			return nil, ErrPlanNotApproved
		}
		if baseline.SuiteHash != "" && planDigest([]byte(baseline.AcceptanceSuite)) != baseline.SuiteHash {
			return nil, ErrPlanNotApproved
		}
		b.BaselineHash, b.SuiteHash = baseline.ContentHash, baseline.SuiteHash
	}
	return b, nil
}

func (m *Module) requirePlanBindingOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, content ReviewPlanContent) error {
	if content.Binding == nil {
		return ErrPlanNotApproved
	}
	current, err := m.reviewPlanBindingOn(ctx, tx, work)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(content.Binding)
	if string(a) != string(b) {
		return ErrPlanNotApproved
	}
	return nil
}

type ReviewPlanView struct {
	model.ReviewPlan
	Content ReviewPlanContent `json:"content"`
}

var ErrPlanNotApproved = errors.Join(ErrResultState, errors.New("delivery: current review plan not approved"))

// The caller holds the current project/work fences. Approval is checked again
// at every execution boundary, so a new proposal cannot reuse an older approval.
func (m *Module) requireApprovedReviewPlanOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem) error {
	if !work.PlanRequired && work.Risk != "MEDIUM" && work.Risk != "HIGH" && work.AutonomyLevel != "MEDIUM" && work.AutonomyLevel != "HIGH" {
		return nil
	}
	if work.BaselineStale {
		return ErrPlanNotApproved
	}
	q := query.BindReviewPlans(tx, m.service.names)
	// A locking read observes the latest committed plan even if earlier IAM
	// reads established a MySQL REPEATABLE READ snapshot before the work lock.
	plan, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Order(q.Round.Desc(), q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrPlanNotApproved
	}
	if err != nil {
		return err
	}
	if plan.State != "APPROVED" || !plan.CoverageOK || plan.DecidedBy == nil || plan.DecidedAt == nil || *plan.DecidedBy == plan.ProposedBy {
		return ErrPlanNotApproved
	}
	hash := sha256.Sum256([]byte(plan.Content))
	if hex.EncodeToString(hash[:]) != plan.ContentHash {
		return ErrPlanNotApproved
	}
	view, err := reviewPlanView(plan)
	if err != nil {
		return err
	}
	if err = m.requirePlanBindingOn(ctx, tx, work, view.Content); err != nil {
		return err
	}
	if err = m.validatePlanCoverageOn(ctx, tx, work, view.Content); errors.Is(err, ErrResultCoverage) || errors.Is(err, ErrInvalidArgument) {
		return ErrPlanNotApproved
	}
	return err
}

func reviewPlanView(r *model.ReviewPlan) (ReviewPlanView, error) {
	v := ReviewPlanView{ReviewPlan: *r}
	if json.Unmarshal([]byte(r.Content), &v.Content) != nil {
		return v, ErrUnavailable
	}
	return v, nil
}
func (m *Module) validatePlanCoverageOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, c ReviewPlanContent) error {
	if !c.OutOfScopeConfirmed || len(c.Files) > 100 || len(c.Risks) > 100 || len(c.CriteriaCoverage) > 100 {
		return ErrInvalidArgument
	}
	for _, list := range [][]string{c.Files, c.Risks} {
		for _, s := range list {
			if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 2000 {
				return ErrInvalidArgument
			}
		}
	}
	q := query.BindWorkCriteria(tx, m.service.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Find()
	if e != nil {
		return e
	}
	if len(rows) == 0 || len(rows) != len(c.CriteriaCoverage) {
		return ErrResultCoverage
	}
	seen := map[guid.ID]bool{}
	for _, v := range c.CriteriaCoverage {
		if v.CriterionID <= 0 || seen[v.CriterionID] || strings.TrimSpace(v.Approach) == "" || strings.TrimSpace(v.Verification) == "" || utf8.RuneCountInString(v.Approach) > 2000 || utf8.RuneCountInString(v.Verification) > 2000 {
			return ErrResultCoverage
		}
		seen[v.CriterionID] = true
	}
	for _, r := range rows {
		if !seen[r.ID] {
			return ErrResultCoverage
		}
	}
	return nil
}
func (m *Module) proposeReviewPlanOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, actor, owner guid.ID, expected int, content ReviewPlanContent) (ReviewPlanView, error) {
	var out ReviewPlanView
	_, work, _, e := m.lockedWorkOn(ctx, tx, tenant, project, workID, actor, owner, "work_item_write")
	if e != nil {
		return out, e
	}
	if work.Version != expected {
		return out, ErrWorkVersion
	}
	if work.State == "ACCEPTED" || work.State == "CANCELLED" || work.CurrentExecutionID != nil { //nolint:misspell // Frozen work-item state.
		return out, ErrResultState
	}
	if e = m.validatePlanCoverageOn(ctx, tx, work, content); e != nil {
		return out, e
	}
	q := query.BindReviewPlans(tx, m.service.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WorkItemID.Eq(workID)).Order(q.Round.Desc()).Find()
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
	if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WorkItemID.Eq(workID), q.State.Eq("PROPOSED")).Updates(map[string]any{"state": "SUPERSEDED"}); e != nil {
		return out, e
	}
	content.Producer = nil
	content.Binding, e = m.reviewPlanBindingOn(ctx, tx, work)
	if e != nil {
		return out, e
	}
	data, e := json.Marshal(content)
	if e != nil {
		return out, ErrInvalidArgument
	}
	hash := sha256.Sum256(data)
	id, e := m.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	row := model.ReviewPlan{ID: guid.ID(id), TenantID: tenant, ProjectID: project, WorkItemID: workID, ProposedBy: actor, SourceKind: "HUMAN", Content: string(data), ContentHash: hex.EncodeToString(hash[:]), CoverageOK: true, Round: round, State: "PROPOSED", Version: 1, CreatedAt: now}
	if e = q.WithContext(ctx).Create(&row); e != nil {
		return out, e
	}
	if e = m.recordReviewPlanOn(ctx, tx, &row, actor, "proposed", ""); e != nil {
		return out, e
	}
	return reviewPlanView(&row)
}
func (m *Module) decideReviewPlanOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, id, actor, owner guid.ID, expected int, hash, action, feedback string) (ReviewPlanView, error) {
	var out ReviewPlanView
	if (action != "APPROVE" && action != "REJECT") || utf8.RuneCountInString(feedback) > 2000 || (action == "REJECT" && strings.TrimSpace(feedback) == "") {
		return out, ErrInvalidArgument
	}
	if e := m.prelockPlanningDecisionOn(ctx, tx, tenant, project, workID, id, actor, owner); e != nil {
		return out, e
	}
	_, work, _, e := m.lockedWorkActorOn(ctx, tx, tenant, project, workID, actor, owner, "plan_approve", true)
	if e != nil {
		return out, e
	}
	if work.State == "ACCEPTED" || work.State == "CANCELLED" || work.CurrentExecutionID != nil { //nolint:misspell // Frozen work-item state.
		return out, ErrResultState
	}
	q := query.BindReviewPlans(tx, m.service.names)
	r, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.Eq(workID), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, ErrProjectNotFound
	}
	if e != nil {
		return out, e
	}
	authoritySource, e := m.planReviewerOn(ctx, tx, work, r, actor)
	if e != nil {
		return out, e
	}
	if r.ProposedBy == actor {
		return out, ErrResultIndependent
	}
	if r.Version != expected || r.ContentHash != hash || r.ContentHash != planDigest([]byte(r.Content)) {
		return out, ErrWorkVersion
	}
	if r.State != "PROPOSED" {
		return out, ErrResultState
	}
	v, e := reviewPlanView(r)
	if e != nil {
		return out, e
	}
	if e = m.requirePlanBindingOn(ctx, tx, work, v.Content); e != nil {
		return out, e
	}
	if e = m.validatePlanCoverageOn(ctx, tx, work, v.Content); e != nil {
		return out, e
	}
	state, kind := "APPROVED", "approved"
	if action == "REJECT" {
		state, kind = "REJECTED", "rejected"
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	changed, e := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.Version.Eq(expected), q.State.Eq("PROPOSED")).Updates(map[string]any{"state": state, "feedback": feedback, "decided_by": actor, "decided_at": now, "version": expected + 1})
	if e != nil {
		return out, e
	}
	if changed.RowsAffected != 1 {
		return out, ErrWorkVersion
	}
	r.State, r.Feedback, r.DecidedBy, r.DecidedAt, r.Version = state, &feedback, &actor, &now, expected+1
	if e = m.recordReviewPlanOn(ctx, tx, r, actor, kind, feedback, authoritySource); e != nil {
		return out, e
	}
	if v.Content.Producer != nil {
		if work.State != "PLAN_REVIEW" || m.planningRuntime.ContinueOn == nil {
			return out, ErrResultState
		}
		before := work.State
		work.State = "CLAIMED"
		if action == "REJECT" && r.Round >= 3 {
			work.State = "NEEDS_HUMAN_REVIEW"
			reason := "INTERNAL_ROUNDS_EXHAUSTED"
			work.RequireHumanDecision = true
			work.EscalationReason = &reason
			if e = tx.Table(m.service.names.WorkItems.String()).Where("tenant_id=? AND id=?", tenant, work.ID).Updates(map[string]any{"require_human_decision": true, "escalation_reason": reason}).Error; e != nil {
				return out, e
			}
			if _, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "work_item.escalated", Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:plan_review"}, Subject: events.Subject{Kind: "work_item", ID: work.ID}, SubjectVersion: int64(work.Version + 1), Data: map[string]any{"work_item_id": work.ID.String(), "reason_code": reason, "plan_round": r.Round}}); e != nil {
				return out, e
			}
		}
		w := query.BindWorkItems(tx, m.service.names)
		if _, e = w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ID.Eq(work.ID)).Update(w.CurrentPlanID, r.ID); e != nil {
			return out, e
		}
		if e = m.executionWorkChangedOn(ctx, tx, ExecutionWorkLink{TenantID: tenant, ProjectID: project, WorkItemID: work.ID, ActorID: actor, ExecutionID: v.Content.Producer.ExecutionID}, work, before, "PLAN_"+state, now); e != nil {
			return out, e
		}
		if work.State == "CLAIMED" {
			if _, e = m.planningRuntime.ContinueOn(ctx, tx, tenant, r.ID, v.Content.Producer.ExecutionID, action == "APPROVE", feedback); e != nil {
				return out, e
			}
		}
	}
	return reviewPlanView(r)
}
func (m *Module) recordReviewPlanOn(ctx context.Context, tx *gorm.DB, r *model.ReviewPlan, actor guid.ID, kind, feedback string, authority ...string) error {
	source := "permission:plan_approve"
	if kind == "proposed" {
		source = "permission:work_item_write"
		if r.SourceKind == "AGENT" {
			source = "SYSTEM:planning"
		}
	}
	if len(authority) > 0 && authority[0] != "" {
		source = authority[0]
	}
	detail := map[string]any{"plan_id": r.ID, "work_item_id": r.WorkItemID, "state": r.State, "content_hash": r.ContentHash, "source_kind": r.SourceKind, "feedback": feedback, "runtime_launch": "NOT_REQUESTED", "authority_source": source}
	if r.SourceKind == "AGENT" && kind != "proposed" {
		detail["runtime_launch"] = "REQUESTED"
		if kind == "rejected" && r.Round >= 3 {
			detail["runtime_launch"] = "ROUND_LIMIT"
		}
	}
	e := m.service.audit(ctx, tx, audit.DomainEvent{TenantID: r.TenantID, PrincipalID: actor, EventType: "delivery.plan_" + kind, TargetType: "plan", TargetID: r.ID, Detail: detail})
	if e != nil {
		return e
	}
	actorKind := "HUMAN"
	if r.SourceKind == "AGENT" {
		if kind == "proposed" {
			actorKind = "AGENT"
		} else {
			human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
			if e != nil {
				return e
			}
			if !active || human.ID != actor {
				actorKind = "AGENT"
			}
		}
	}
	if actorKind == "AGENT" && kind == "rejected" {
		detail["visibility"] = "INTERNAL"
	}
	_, e = m.service.events.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: "plan." + kind, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "plan", ID: r.ID}, SubjectVersion: int64(r.Version), Data: detail})
	return e
}
