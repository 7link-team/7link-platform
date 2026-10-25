package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrStageIllegal   = errors.New("delivery: illegal stage transition")
	ErrStageCriteria  = errors.New("delivery: stage criteria unmet")
	ErrStageDelivered = errors.New("delivery: client acceptance incomplete")
	ErrStageClose     = errors.New("delivery: project has open work")
	ErrStageReason    = errors.New("delivery: stage reason required")
)

type stageFailure struct {
	err  error
	meta map[string]any
}

func (e stageFailure) Error() string { return e.err.Error() }
func (e stageFailure) Unwrap() error { return e.err }

type stageChangeBody struct {
	ScopeChangeID *guid.ID `json:"scope_change_id,omitempty"`
	ReauthProof   string   `json:"reauth_proof,omitempty" maxLength:"512" writeOnly:"true"`
	ConfirmCode   string   `json:"confirm_code,omitempty" maxLength:"16"`
	cookie        string
	ToStage       string  `json:"to_stage" enum:"DISCOVERY,PLANNING,ACTIVE,BLOCKED,ACCEPTANCE_PENDING,DELIVERED,CLOSED,CANCELLED"` //nolint:misspell // Frozen enum.
	Reason        string  `json:"reason,omitempty" maxLength:"2000"`
	BlockedKind   *string `json:"blocked_kind,omitempty" enum:"CLIENT_CLARIFICATION,DEPENDENCY,EXTERNAL,OTHER"`
}
type stageCriterion struct {
	WorkItemIDs []guid.ID `json:"work_item_ids,omitempty"`
	Key         string    `json:"key"`
	Passed      bool      `json:"passed"`
	Detail      string    `json:"detail"`
}
type stageReadiness struct {
	Target    string           `json:"target"`
	Allowed   bool             `json:"allowed"`
	HumanOnly bool             `json:"human_only"`
	Criteria  []stageCriterion `json:"criteria"`
}
type stageTransitionView struct {
	model.ProjectStageTransition
	Criteria []stageCriterion `json:"criteria"`
}
type stageReceipt struct {
	Transition stageTransitionView `json:"transition"`
	Project    ProjectView         `json:"project"`
}

func stageTargets(stage string) []string {
	out := ordinaryStageTargets(stage)
	if slices.Contains([]string{"INTAKE", "DISCOVERY", "PLANNING", "ACTIVE", "BLOCKED", "ACCEPTANCE_PENDING", "DELIVERED"}, stage) {
		out = append(out, "CANCELLED") //nolint:misspell // Preserve the published domain or serialized contract spelling.
	}
	return out
}
func ordinaryStageTargets(stage string) []string {
	switch stage {
	case "INTAKE":
		return []string{"DISCOVERY"}
	case "DISCOVERY":
		return []string{"PLANNING"}
	case "PLANNING":
		return []string{"DISCOVERY", "ACTIVE"}
	case "ACTIVE":
		return []string{"BLOCKED", "ACCEPTANCE_PENDING"}
	case "BLOCKED":
		return []string{"ACTIVE"}
	case "ACCEPTANCE_PENDING":
		return []string{"ACTIVE", "DELIVERED"}
	case "DELIVERED":
		return []string{"ACTIVE", "CLOSED"}
	default:
		return []string{}
	}
}

// stageCriteriaOn reads real aggregate facts under the project's serialization
// lock, using current locking reads even after an IAM snapshot. Missing later aggregates are explicit criteria, never successful guards.
func (m *Module) stageCriteriaOn(ctx context.Context, tx *gorm.DB, project *model.Project, target string, eligible bool) ([]stageCriterion, error) {
	criteria := []stageCriterion{}
	add := func(key string, passed bool, detail string) {
		criteria = append(criteria, stageCriterion{nil, key, passed, detail})
	}
	tenant, entity := project.TenantID, project.ProjectID
	switch {
	case target == "CANCELLED": //nolint:misspell // Preserve the published domain or serialized contract spelling.
		add("human_step_up_cancel", true, "Human cancellation requires a current direct role, project code, reason and fresh proof.")
	case project.Stage == "DELIVERED" && target == "ACTIVE":
		add("effective_scope_change", true, "Supply the effective post-delivery scope change ID.")
	case project.Stage == "INTAKE" || (project.Stage == "DISCOVERY" && target == "PLANNING"):
		if m.sources.ProjectFactsOn == nil {
			return nil, ErrUnavailable
		}
		facts, err := m.sources.ProjectFactsOn(ctx, tx, tenant, entity, project.OriginIntakeID)
		if err != nil {
			return nil, err
		}
		r := query.BindRequirements(tx, m.service.names)
		requirements, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(entity), r.ArchivedAt.IsNull()).Order(r.ID).Find()
		if err != nil {
			return nil, err
		}
		w := query.BindWorkItems(tx, m.service.names)
		c := query.BindWorkContracts(tx, m.service.names)
		confirmed, err := w.WithContext(ctx).Join(c, c.TenantID.EqCol(w.TenantID), c.ProjectID.EqCol(w.ProjectID), c.WorkItemID.EqCol(w.ID)).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ArchivedAt.IsNull(), w.State.Neq("CANCELLED")).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find() //nolint:misspell // Frozen state.
		if err != nil {
			return nil, err
		}
		if project.Stage == "INTAKE" {
			add("source_or_requirement_or_confirmed_work", facts.HasSource || len(requirements) > 0 || len(confirmed) > 0, "A linked source, registered Requirement or confirmed work is required.")
		} else {
			if len(requirements) > 0 {
				baselined := true
				for _, row := range requirements {
					ac := query.BindCriteria(tx, m.service.names)
					count, e := ac.WithContext(ctx).Where(ac.TenantID.Eq(tenant), ac.ProjectID.Eq(entity), ac.RequirementID.Eq(row.ID)).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
					if e != nil {
						return nil, e
					}
					versions := query.BindRequirementVersions(tx, m.service.names)
					versionCount, e := versions.WithContext(ctx).Where(versions.TenantID.Eq(tenant), versions.ProjectID.Eq(entity), versions.RequirementID.Eq(row.ID), versions.VersionNo.Eq(row.CurrentVersionNo)).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
					if e != nil {
						return nil, e
					}
					baselined = baselined && row.Status == "BASELINED" && row.CurrentVersionNo >= 1 && len(count) > 0 && len(versionCount) == 1
				}
				add("all_requirements_baselined", baselined, "Every registered Requirement needs a fixed baseline and acceptance criteria.")
			} else {
				ready, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ArchivedAt.IsNull(), w.State.Eq("READY"), w.ReadinessOK.Is(true), w.PausedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
				if e != nil {
					return nil, e
				}
				add("confirmed_ready_work", len(confirmed) > 0 && len(ready) > 0, "Without Requirements, confirmed work and at least one READY work item are required.")
			}
			add("clarifications_closed", facts.OpenClarifications == 0, "All linked clarification rounds must be closed.")
		}
	case project.Stage == "PLANNING" && target == "ACTIVE":
		w := query.BindWorkItems(tx, m.service.names)
		ready, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.ArchivedAt.IsNull(), w.State.Eq("READY"), w.ReadinessOK.Is(true), w.PausedAt.IsNull(), w.IsContainer.Is(false)).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
		if err != nil {
			return nil, err
		}
		add("ready_work", len(ready) > 0, "At least one READY leaf work item is required.")
		c := query.BindConstitutions(tx, m.service.names)
		active := false
		if project.ConstitutionVersionNo != nil {
			count, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(entity), c.VersionNo.Eq(*project.ConstitutionVersionNo), c.State.Eq("ACTIVE"), c.ActiveKey.Eq(entity)).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
			if e != nil {
				return nil, e
			}
			active = len(count) == 1
		}
		add("active_constitution", active, "The current constitution must be ACTIVE.")
		add("eligible_executor", eligible, "A current active member with project-scoped work_item_execute authority is required.")
		add("autonomous_budget", project.TeamMode != "AUTONOMOUS" || project.BudgetCapMicros != nil, "AUTONOMOUS mode requires a budget cap.")
	case project.Stage == "ACTIVE" && target == "ACCEPTANCE_PENDING":
		d := query.BindDeliveries(tx, m.service.names)
		count, err := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.ProjectID.Eq(entity), d.State.Eq("SENT"), d.ArchivedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
		if err != nil {
			return nil, err
		}
		if m.stagePackages.FactsOn == nil {
			return nil, ErrUnavailable
		}
		facts, e := m.stagePackages.FactsOn(ctx, tx, tenant, entity)
		if e != nil {
			return nil, e
		}
		add("sent_delivery", len(count) > 0 || facts.Published, "At least one delivery must be published.")
	case target == "DELIVERED" || target == "CLOSED":
		return m.stageTerminalCriteriaOn(ctx, tx, project, target)
	default:
		add("explicit_command", true, "This stage requires an explicit authorized command and reason.")
	}
	return criteria, nil
}

// stageExecutorCandidates discovers a lock set only. Current membership,
// principal lifecycle and scoped execute authority are rechecked in transaction.
func (m *Module) stageExecutorCandidates(ctx context.Context, db *gorm.DB, tenant, entity guid.ID) ([]guid.ID, error) {
	if m == nil || m.service == nil || m.projectAccess.TenantMembers.IsZero() {
		return nil, ErrUnavailable
	}
	p := query.BindProjects(db, m.service.names)
	var members gen.DO
	members.UseDB(db)
	members.UseTable(m.projectAccess.TenantMembers.String())
	table := members.As("stage_member")
	principal := field.NewInt64("stage_member", "principal_id")
	var rows []struct{ PrincipalID guid.ID }
	err := p.WithContext(ctx).Select(principal).Join(table, field.NewField("stage_member", "tenant_id").EqCol(p.TenantID)).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity), field.NewString("stage_member", "status").Eq("active")).Order(principal).Scan(&rows)
	ids := make([]guid.ID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PrincipalID)
	}
	return ids, err
}

func (m *Module) lockStagePrincipalsOn(ctx context.Context, tx *gorm.DB, actor guid.ID, candidates []guid.ID) error {
	if m == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	ids := append([]guid.ID{actor}, candidates...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id <= 0 {
			return ErrInvalidArgument
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) stageExecutorOn(ctx context.Context, tx *gorm.DB, tenant, entity guid.ID, candidates []guid.ID, humanOnly bool) (bool, error) {
	for _, id := range candidates {
		member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, id)
		if err != nil {
			return false, err
		}
		if !member {
			continue
		}
		human, active, err := m.principals.ActiveHumanByID(ctx, tx, id)
		if err != nil {
			return false, err
		}
		if !active || human.ID != id {
			if humanOnly {
				continue
			}
			if m.agents.ByPrincipalOn == nil {
				return false, ErrUnavailable
			}
			agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, id)
			if e != nil {
				return false, e
			}
			if !found || agent.TenantID != tenant || agent.PrincipalID != id || agent.State != "ACTIVE" {
				continue
			}
		}
		constraint, err := m.workPolicy(ctx, tx, tenant, entity, "work_item_execute", id)
		if err != nil {
			return false, err
		}
		w := query.BindWorkItems(tx, m.service.names)
		scope, err := workScope(constraint, entity, w.CreatedBy)
		if errors.Is(err, ErrWorkForbidden) {
			continue
		}
		if err != nil {
			return false, err
		}
		base := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(entity), w.State.Eq("READY"), w.ReadinessOK.Is(true), w.PausedAt.IsNull(), w.IsContainer.Is(false), w.ArchivedAt.IsNull())
		if scope != nil {
			base = base.Where(scope)
		}
		count, err := base.Clauses(clause.Locking{Strength: "UPDATE"}).Limit(1).Find()
		if err != nil {
			return false, err
		}
		if len(count) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) changeProjectStageOn(ctx context.Context, tx *gorm.DB, tenant, entity, actor guid.ID, expected int, body stageChangeBody, candidates []guid.ID) (stageReceipt, error) {
	var empty stageReceipt
	body.Reason = strings.TrimSpace(body.Reason)
	if !utf8.ValidString(body.Reason) || utf8.RuneCountInString(body.Reason) > 2000 || expected < 1 {
		return empty, ErrInvalidArgument
	}
	if m.stageAuthority == nil {
		return empty, ErrUnavailable
	}
	if err := m.lockProjectStageOn(ctx, tx, tenant, entity, actor, body.ToStage, candidates); err != nil {
		return empty, err
	}
	project, kind, err := m.constitutionProjectOn(ctx, tx, tenant, entity, actor, "project_stage_change")
	if err != nil {
		return empty, err
	}
	authority, err := m.stageAuthority(ctx, tx, tenant, entity, actor, kind)
	if err != nil {
		return empty, err
	}
	if !authority.Allowed || authority.AuthoritySource == "" {
		return empty, ErrWorkForbidden
	}
	if project.Version != expected {
		return empty, ErrWorkVersion
	}
	if !slices.Contains(stageTargets(project.Stage), body.ToStage) {
		return empty, stageFailure{ErrStageIllegal, map[string]any{"allowed_targets": stageTargets(project.Stage)}}
	}
	if slices.Contains([]string{"DELIVERED", "CLOSED", "CANCELLED"}, body.ToStage) && (kind != "HUMAN" || authority.DelegationID != 0 || !strings.HasPrefix(authority.AuthoritySource, "ROLE:")) { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return empty, ErrWorkForbidden
	}
	if body.ToStage == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return m.cancelStageOn(ctx, tx, tenant, entity, actor, expected, body, body.cookie, candidates)
	}
	if project.Stage == "DELIVERED" && body.ToStage == "ACTIVE" {
		ok, e := m.scopeChangeEffectiveOn(ctx, tx, project, body.ScopeChangeID)
		if e != nil {
			return empty, e
		}
		if !ok {
			return empty, ErrStageCriteria
		}
	}
	needsReason := (project.Stage == "DELIVERED" && body.ToStage == "ACTIVE") || body.ToStage == "BLOCKED" || project.Stage == "BLOCKED" || (body.ToStage == "DISCOVERY" && project.Stage == "PLANNING") || (body.ToStage == "ACTIVE" && project.Stage == "ACCEPTANCE_PENDING")
	if needsReason && utf8.RuneCountInString(body.Reason) < 10 {
		return empty, ErrStageReason
	}
	if body.ToStage == "BLOCKED" && (body.BlockedKind == nil || !slices.Contains([]string{"CLIENT_CLARIFICATION", "DEPENDENCY", "EXTERNAL", "OTHER"}, *body.BlockedKind)) {
		return empty, ErrInvalidArgument
	}
	if body.ToStage != "BLOCKED" && body.BlockedKind != nil {
		return empty, ErrInvalidArgument
	}
	eligible := false
	if project.Stage == "PLANNING" && body.ToStage == "ACTIVE" {
		eligible, err = m.stageExecutorOn(ctx, tx, tenant, entity, candidates, project.TeamMode == "HUMAN_ONLY")
		if err != nil {
			return empty, err
		}
	}
	criteria, err := m.stageCriteriaOn(ctx, tx, project, body.ToStage, eligible)
	if err != nil {
		return empty, err
	}
	for _, criterion := range criteria {
		if !criterion.Passed {
			meta := map[string]any{"criteria": criteria}
			cause := ErrStageCriteria
			if body.ToStage == "DELIVERED" {
				cause = ErrStageDelivered
				meta["missing_work_item_ids"] = []guid.ID{}
				for _, v := range criteria {
					if v.Key == "client_accepted_work" {
						meta["missing_work_item_ids"] = v.WorkItemIDs
					}
				}
			}
			if body.ToStage == "CLOSED" {
				cause = ErrStageClose
			}
			return empty, stageFailure{cause, meta}
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	from := project.Stage
	project.Stage, project.StageChangedAt, project.UpdatedAt, project.UpdatedBy = body.ToStage, now, now, &actor
	project.Version++
	if body.ToStage == "DELIVERED" && project.DeliveredAt == nil {
		project.DeliveredAt = &now
	}
	if body.ToStage == "CLOSED" {
		if e := m.finishProjectOn(ctx, tx, project, authority.AuthoritySource, body.Reason, candidates); e != nil {
			return empty, e
		}
		project.ClosedAt = &now
		project.Holds, project.Health, project.HealthReasons = "[]", "NONE", "[]"
	}
	if body.ToStage == "ACTIVE" && project.StartedAt == nil {
		project.StartedAt = &now
	}
	project.BlockedKind, project.BlockedReason = nil, nil
	if body.ToStage == "BLOCKED" {
		project.BlockedKind = body.BlockedKind
		project.BlockedReason = &body.Reason
	}
	p := query.BindProjects(tx, m.service.names)
	result, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(entity), p.Version.Eq(expected)).Select(p.Stage, p.StageChangedAt, p.StartedAt, p.DeliveredAt, p.ClosedAt, p.Holds, p.Health, p.HealthReasons, p.BlockedKind, p.BlockedReason, p.Version, p.UpdatedAt, p.UpdatedBy).Updates(project)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrWorkVersion
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	snapshot, _ := json.Marshal(criteria)
	transition := model.ProjectStageTransition{TenantID: tenant, ID: guid.ID(id), ProjectID: entity, FromStage: from, ToStage: body.ToStage, ActorKind: kind, ActorID: actor, AuthoritySource: authority.AuthoritySource, Reason: body.Reason, BlockedKind: body.BlockedKind, CriteriaSnapshot: string(snapshot), ScopeChangeID: body.ScopeChangeID, VersionAfter: project.Version, OccurredAt: now}
	if err = query.BindStageTransitions(tx, m.service.names).WithContext(ctx).Create(&transition); err != nil {
		return empty, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.project_stage_changed", "project", entity)
	event.Detail["from_stage"], event.Detail["to_stage"], event.Detail["transition_id"], event.Detail["authority_source"] = from, body.ToStage, transition.ID.String(), authority.AuthoritySource
	if err = m.service.audit(ctx, tx, event); err != nil {
		return empty, err
	}
	_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: entity, Type: "project.stage_changed", Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority.AuthoritySource}, Subject: events.Subject{Kind: "project", ID: entity}, SubjectVersion: int64(project.Version), Data: map[string]any{"from_stage": from, "to_stage": body.ToStage, "transition_id": transition.ID.String(), "reason": body.Reason}})
	if err != nil {
		return empty, err
	}
	view, err := projectRecordView(*project)
	return stageReceipt{Transition: stageTransitionView{transition, criteria}, Project: view}, err
}
