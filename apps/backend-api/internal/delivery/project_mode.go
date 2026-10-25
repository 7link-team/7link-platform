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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrModeSame          = errors.New("delivery: project mode unchanged")
	ErrModeIllegal       = errors.New("delivery: project mode transition illegal")
	ErrModePreconditions = errors.New("delivery: autonomous mode prerequisites unmet")
)

type projectModeBody struct {
	ToMode      string `json:"to_mode" enum:"HUMAN_ONLY,HYBRID,AUTONOMOUS"`
	Reason      string `json:"reason" minLength:"10" maxLength:"2000"`
	ReauthProof string `json:"reauth_proof,omitempty" maxLength:"256"`
}

type projectModeChangeView struct {
	model.ProjectModeChange
	Preconditions   []stageCriterion `json:"preconditions_snapshot"`
	RevokedIDs      []guid.ID        `json:"revoked_delegation_ids"`
	RevokedSpawnIDs []guid.ID        `json:"revoked_spawn_grant_ids"`
}

// ProjectModeReceipt is the committed control-plane change, never a claim that
// an execution runner paused or stopped.
type ProjectModeReceipt struct {
	ModeChange   projectModeChangeView `json:"mode_change"`
	Project      ProjectView           `json:"project"`
	ModeChangeID *guid.ID              `json:"mode_change_id"`
	TakeoverID   *guid.ID              `json:"takeover_id"`
}

func (m *Module) modePrincipalCandidates(ctx context.Context, db *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	ids, err := m.stageExecutorCandidates(ctx, db, tenant, project)
	if err != nil {
		return nil, err
	}
	if m.modeGovernance.PrincipalsOn == nil {
		return nil, ErrUnavailable
	}
	delegated, err := m.modeGovernance.PrincipalsOn(ctx, db, tenant, project)
	if err != nil {
		return nil, err
	}
	ids = append(ids, delegated...)
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (m *Module) modePreconditionsOn(ctx context.Context, tx *gorm.DB, p *model.Project, locked []guid.ID) ([]stageCriterion, error) {
	if m.modeGovernance.PreconditionsOn == nil || m.modeGovernance.AgentRoleOn == nil {
		return nil, ErrUnavailable
	}
	delegation, missing, err := m.modeGovernance.PreconditionsOn(ctx, tx, p.TenantID, p.ProjectID, locked)
	if err != nil {
		return nil, err
	}
	executor := false
	for _, principal := range locked {
		ok, err := m.modeGovernance.AgentRoleOn(ctx, tx, p.TenantID, p.ProjectID, principal, "agent_executor")
		if err != nil {
			return nil, err
		}
		executor = executor || ok
	}
	return []stageCriterion{
		{nil, "P1", delegation, "An active project reviewer delegation allowing approval must remain valid for at least 24 hours."},
		{nil, "P2", len(missing) == 0, "Required effective escalation rules: " + strings.Join(missing, ", ")},
		{nil, "P3", p.BudgetCapMicros != nil && p.BudgetState != "EXHAUSTED", "A non-exhausted budget cap is required."},
		{nil, "P4", executor, "An active Agent with the project executor role is required."},
		{nil, "P5", slices.Contains([]string{"PLANNING", "ACTIVE", "ACCEPTANCE_PENDING"}, p.Stage), "Project stage must permit autonomous work."},
	}, nil
}

func (m *Module) projectModeAuthorizeOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, takeover bool, locked []guid.ID) (*model.Project, string, error) {
	if err := m.lockStagePrincipalsOn(ctx, tx, actor, locked); err != nil {
		return nil, "", err
	}
	permission := "project_mode_change"
	if takeover {
		permission = "project_takeover"
	}
	p, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, permission)
	if err != nil {
		return nil, "", err
	}
	if kind != "HUMAN" {
		return nil, "", ErrWorkForbidden
	}
	if m.modeGovernance.AuthorityOn == nil {
		return nil, "", ErrUnavailable
	}
	source, err := m.modeGovernance.AuthorityOn(ctx, tx, tenant, project, permission)
	if err != nil {
		return nil, "", err
	}
	if source == "" {
		return nil, "", ErrWorkForbidden
	}
	return p, source, nil
}

func (m *Module) changeProjectModeOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, expected int, in projectModeBody, cookie string, takeover bool, locked []guid.ID) (ProjectModeReceipt, error) {
	var out ProjectModeReceipt
	in.Reason = strings.TrimSpace(in.Reason)
	if expected < 1 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(in.Reason) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || !slices.Contains([]string{"HUMAN_ONLY", "HYBRID", "AUTONOMOUS"}, in.ToMode) {
		return out, ErrInvalidArgument
	}
	p, source, err := m.projectModeAuthorizeOn(ctx, tx, tenant, project, actor, takeover, locked)
	if err != nil {
		return out, err
	}
	if slices.Contains([]string{"CLOSED", "CANCELLED"}, p.Stage) { //nolint:misspell // Frozen protocol state.
		return out, ErrWorkProjectReadOnly
	}
	if p.Version != expected {
		return out, ErrWorkVersion
	}
	if takeover && in.ToMode != "HYBRID" {
		return out, ErrInvalidArgument
	}
	// Taking back authority cannot re-enable Agents in a pure Human project.
	if takeover && p.TeamMode == "HUMAN_ONLY" {
		in.ToMode = "HUMAN_ONLY"
	}
	if !takeover && p.TeamMode == in.ToMode {
		return out, ErrModeSame
	}
	if p.TeamMode == "HUMAN_ONLY" && in.ToMode == "AUTONOMOUS" {
		return out, ErrModeIllegal
	}
	// Proof consumption uses the same already-held principal fence and transaction.
	needsProof := takeover || in.ToMode == "AUTONOMOUS" || (p.TeamMode == "AUTONOMOUS" && in.ToMode == "HUMAN_ONLY")
	if needsProof {
		if m.scopeReauthentication == nil {
			return out, ErrUnavailable
		}
		purpose := "team_mode_change"
		if takeover {
			purpose = "project_takeover"
		}
		if err = m.scopeReauthentication(ctx, tx, actor, cookie, in.ReauthProof, purpose, "project:"+project.String()); err != nil {
			return out, err
		}
	}
	criteria := []stageCriterion{}
	if in.ToMode == "AUTONOMOUS" {
		criteria, err = m.modePreconditionsOn(ctx, tx, p, locked)
		if err != nil {
			return out, err
		}
		for _, c := range criteria {
			if !c.Passed {
				return out, stageFailure{ErrModePreconditions, map[string]any{"preconditions": criteria}}
			}
		}
	}
	revoked := []guid.ID{}
	if takeover || (p.TeamMode == "AUTONOMOUS" && in.ToMode != "AUTONOMOUS") {
		if m.modeGovernance.RevokeOn == nil {
			return out, ErrUnavailable
		}
		permission := "project_mode_change"
		if takeover {
			permission = "project_takeover"
		}
		revoked, err = m.modeGovernance.RevokeOn(ctx, tx, tenant, project, permission, in.Reason, locked)
		if err != nil {
			return out, err
		}
	}
	revokedSpawn := []guid.ID{}
	if takeover || (p.TeamMode == "AUTONOMOUS" && in.ToMode == "HUMAN_ONLY") {
		if m.modeGovernance.RevokeSpawnOn == nil {
			return out, ErrUnavailable
		}
		permission := "project_mode_change"
		if takeover {
			permission = "project_takeover"
		}
		revokedSpawn, err = m.modeGovernance.RevokeSpawnOn(ctx, tx, tenant, project, permission, in.Reason, locked)
		if err != nil {
			return out, err
		}
	}
	from := p.TeamMode
	now := time.Now().UTC().Truncate(time.Millisecond)
	if from != in.ToMode {
		p.TeamModeChangedAt = &now
	}
	p.TeamMode, p.UpdatedAt, p.UpdatedBy = in.ToMode, now, &actor
	p.AutonomyVersion = nil
	p.Version++
	if in.ToMode == "HUMAN_ONLY" || takeover || (from == "AUTONOMOUS" && in.ToMode == "HYBRID") {
		p.DispatchPolicy = "MANUAL"
	}
	if in.ToMode == "AUTONOMOUS" {
		if !p.DispatchPolicyExplicit {
			p.DispatchPolicy = "AUTO"
		}
		var holds []map[string]any
		if json.Unmarshal([]byte(p.Holds), &holds) != nil {
			return out, ErrUnavailable
		}
		holds = slices.DeleteFunc(holds, func(h map[string]any) bool { return h["kind"] == "AUTONOMY_PRECONDITION" })
		raw, err := json.Marshal(holds)
		if err != nil {
			return out, err
		}
		p.Holds = string(raw)
	}
	if in.ToMode == "HUMAN_ONLY" {
		if err = m.releaseUnstartedAgentAssignmentsOn(ctx, tx, p, actor, source, now); err != nil {
			return out, err
		}
	}
	q := query.BindProjects(tx, m.service.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Version.Eq(expected)).Updates(map[string]any{"team_mode": p.TeamMode, "team_mode_changed_at": p.TeamModeChangedAt, "dispatch_policy": p.DispatchPolicy, "holds": p.Holds, "autonomy_version": nil, "version": p.Version, "updated_at": p.UpdatedAt, "updated_by": p.UpdatedBy})
	if err != nil {
		return out, err
	}
	if changed.RowsAffected != 1 {
		return out, ErrWorkVersion
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	guards, err := json.Marshal(criteria)
	if err != nil {
		return out, err
	}
	revokedJSON, err := json.Marshal(revoked)
	if err != nil {
		return out, err
	}
	revokedSpawnJSON, err := json.Marshal(revokedSpawn)
	if err != nil {
		return out, err
	}
	trigger := "MANUAL"
	if takeover {
		trigger = "TAKEOVER"
	}
	r := model.ProjectModeChange{TenantID: tenant, ID: guid.ID(id), ProjectID: project, FromMode: from, ToMode: in.ToMode, Trigger: trigger, Reason: in.Reason, PreconditionsSnapshot: string(guards), RevokedDelegationIDs: string(revokedJSON), RevokedSpawnGrantIDs: string(revokedSpawnJSON), ActorID: actor, AuthoritySource: source, VersionAfter: p.Version, OccurredAt: now}
	if err = query.BindModeChanges(tx, m.service.names).WithContext(ctx).Create(&r); err != nil {
		return out, err
	}
	auditKind := "delivery.project_mode_changed"
	if takeover {
		auditKind = "delivery.project_taken_over"
	}
	event := audit.NewDomainEvent(ctx, tenant, auditKind, "project", project)
	event.Detail["from"], event.Detail["to"], event.Detail["reason"], event.Detail["trigger"], event.Detail["mode_change_id"], event.Detail["authority_source"], event.Detail["revoked_delegation_ids"] = from, in.ToMode, in.Reason, trigger, r.ID.String(), source, revoked
	event.Detail["revoked_spawn_grant_ids"] = revokedSpawn
	var modeChangeID, takeoverID *guid.ID
	if from != in.ToMode {
		modeChangeID = &r.ID
	}
	if takeover {
		takeoverID = &r.ID
	}
	event.Detail["mode_change_id"], event.Detail["takeover_id"] = modeChangeID, takeoverID
	if err = m.service.audit(ctx, tx, event); err != nil {
		return out, err
	}
	// A takeover of an already-HYBRID project still revokes authority, but must
	// not invent a mode change for downstream consumers.
	if from != in.ToMode {
		_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.mode_changed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(p.Version), Data: map[string]any{"from": from, "to": in.ToMode, "trigger": trigger, "mode_change_id": r.ID.String(), "reason": in.Reason, "revoked_delegation_ids": revoked, "revoked_spawn_grant_ids": revokedSpawn}})
		if err != nil {
			return out, err
		}
	}
	if takeover {
		_, err = m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.taken_over", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(p.Version), Data: map[string]any{"from": from, "to": in.ToMode, "takeover_id": r.ID.String(), "mode_change_id": modeChangeID, "reason": in.Reason, "revoked_delegation_ids": revoked, "revoked_spawn_grant_ids": revokedSpawn}})
		if err != nil {
			return out, err
		}
	}
	view, err := projectRecordView(*p)
	return ProjectModeReceipt{ModeChange: projectModeChangeView{r, criteria, revoked, revokedSpawn}, Project: view, ModeChangeID: modeChangeID, TakeoverID: takeoverID}, err
}

func (m *Module) releaseUnstartedAgentAssignmentsOn(ctx context.Context, tx *gorm.DB, p *model.Project, actor guid.ID, source string, now time.Time) error {
	w := query.BindWorkItems(tx, m.service.names)
	works, err := w.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(w.TenantID.Eq(p.TenantID), w.ProjectID.Eq(p.ProjectID), w.ArchivedAt.IsNull(), w.CurrentExecutionID.IsNull(), w.CurrentClaimID.IsNull(), w.State.In("DRAFT", "READY", "CLAIMED", "BLOCKED")).Order(w.ID).Find()
	if err != nil {
		return err
	}
	a := query.BindAssignments(tx, m.service.names)
	for _, work := range works {
		rows, err := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(p.TenantID), a.ProjectID.Eq(p.ProjectID), a.WorkItemID.Eq(work.ID), a.AssigneeKind.Eq("AGENT"), a.Status.Eq("ACTIVE")).Order(a.ID).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			row.Status, row.ActiveSlot, row.EndedAt, row.Version, row.UpdatedAt, row.UpdatedBy = "RELEASED", nil, &now, row.Version+1, now, &actor
			reason := "PROJECT_HUMAN_ONLY"
			row.EndReason = &reason
			changed, err := a.WithContext(ctx).Where(a.TenantID.Eq(p.TenantID), a.ID.Eq(row.ID), a.Status.Eq("ACTIVE"), a.Version.Eq(row.Version-1)).Select(a.Status, a.ActiveSlot, a.EndedAt, a.EndReason, a.Version, a.UpdatedAt, a.UpdatedBy).Updates(row)
			if err != nil {
				return err
			}
			if changed.RowsAffected != 1 {
				return ErrWorkVersion
			}
			before := work.State
			if row.Role == "EXECUTOR" && work.State == "CLAIMED" {
				work.State = "READY"
			}
			if err = m.assignmentWorkChangedByOn(ctx, tx, work, row, actor, "HUMAN", source, before, reason, now); err != nil {
				return err
			}
		}
	}
	return nil
}

type workTakeoverBody struct {
	Version     int    `json:"version" minimum:"1"`
	Reason      string `json:"reason" minLength:"10" maxLength:"2000"`
	ReauthProof string `json:"reauth_proof" minLength:"1" maxLength:"256"`
}

func (m *Module) takeoverWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor, owner guid.ID, in workTakeoverBody, cookie string) (WorkView, error) {
	var out WorkView
	if in.Version < 1 || !utf8.ValidString(in.Reason) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalidArgument
	}
	_, work, _, err := m.lockedWorkOn(ctx, tx, tenant, project, id, actor, owner, "project_takeover")
	if err != nil {
		return out, err
	}
	if m.modeGovernance.AuthorityOn == nil || m.scopeReauthentication == nil {
		return out, ErrUnavailable
	}
	source, err := m.modeGovernance.AuthorityOn(ctx, tx, tenant, project, "project_takeover")
	if err != nil {
		return out, err
	}
	if work.Version != in.Version {
		return out, ErrWorkVersion
	}
	if work.State == "ACCEPTED" || work.State == "CANCELLED" { //nolint:misspell // Frozen protocol state.
		return out, ErrResultState
	}
	if err = m.scopeReauthentication(ctx, tx, actor, cookie, in.ReauthProof, "project_takeover", "project:"+project.String()); err != nil {
		return out, err
	}
	work.RequireHumanDecision = true
	if err = m.resultWorkChangedOn(ctx, tx, work, actor, work.State, "WORK_TAKEN_OVER", source, 0, time.Now().UTC().Truncate(time.Millisecond)); err != nil {
		return out, err
	}
	return workRecord(work), nil
}
