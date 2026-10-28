package governance

import (
	activityi18n "7link.com/platform/internal/governance/i18n"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DelegationProjectFacts is bound by the project owner. InspectOn holds the
// project fence; authorization and terminal-project policy remain with caller.
type DelegationProjectFacts struct {
	InspectOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error)
}
type DelegationProjectState struct {
	Stage, Mode      string
	DelegatedAllowed bool
}
type DelegationProjectConnector struct {
	Bind func(DelegationProjectFacts) error
}

type delegationServices struct {
	agents   fleet.AgentDirectory
	members  core.ActiveMemberChecker
	bindings core.EntityRoleBindingStore
	grants   core.RoleGrantStore
	replay   idempotency.Store
	projects DelegationProjectFacts
}

func (m *Module) bindDelegationProjects(f DelegationProjectFacts) error {
	if f.InspectOn == nil || m.delegations.projects.InspectOn != nil {
		return ErrInvalidArgument
	}
	m.delegations.projects = f
	return nil
}

func (m *Module) injectDelegations(s *module.Services) error {
	var e error
	if m.delegations.agents, e = module.Resolve[fleet.AgentDirectory](s); e != nil {
		return e
	}
	if m.delegations.members, e = module.Resolve[core.ActiveMemberChecker](s); e != nil {
		return e
	}
	if m.delegations.bindings, e = module.Resolve[core.EntityRoleBindingStore](s); e != nil {
		return e
	}
	revoke, e := module.Resolve[core.EntityRoleBindingRevoker](s)
	if e != nil {
		return e
	}
	m.delegations.grants.RemoveEntityRoleBinding = revoke
	if m.delegations.replay, e = module.Resolve[idempotency.Store](s); e != nil {
		return e
	}
	catalog, e := module.Resolve[events.Catalog](s)
	if e != nil {
		return e
	}
	return catalog.AddTypes(Name, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_delegation_scheduled_name", DescriptionKey: activityi18n.Namespace + ".activity_event_delegation_scheduled_description", Name: "delegation.scheduled", SchemaVersion: 1, SubjectKind: "delegation", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_delegation_activated_name", DescriptionKey: activityi18n.Namespace + ".activity_event_delegation_activated_description", Name: "delegation.activated", SchemaVersion: 1, SubjectKind: "delegation", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_delegation_granted_name", DescriptionKey: activityi18n.Namespace + ".activity_event_delegation_granted_description", Name: "delegation.granted", SchemaVersion: 1, SubjectKind: "delegation", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_delegation_revoked_name", DescriptionKey: activityi18n.Namespace + ".activity_event_delegation_revoked_description", Name: "delegation.revoked", SchemaVersion: 1, SubjectKind: "delegation", Realtime: events.RealtimeSignal}, events.TypeSpec{DisplayNameKey: activityi18n.Namespace + ".activity_event_delegation_expired_name", DescriptionKey: activityi18n.Namespace + ".activity_event_delegation_expired_description", Name: "delegation.expired", SchemaVersion: 1, SubjectKind: "delegation", Realtime: events.RealtimeSignal})
}

type DelegationBinding struct {
	RoleKey string  `json:"role_key"`
	RoleID  guid.ID `json:"role_id"`
}
type DelegationView struct {
	ID                 guid.ID `json:"id"`
	ProjectID          guid.ID `json:"project_id"`
	GranteePrincipalID guid.ID `json:"grantee_principal_id"`
	GranteeAgentID     guid.ID `json:"grantee_agent_id"`
	GrantorPrincipalID guid.ID `json:"grantor_principal_id"`
	DelegationConditions
	StartsAt  time.Time       `json:"starts_at"`
	EndsAt    time.Time       `json:"ends_at"`
	Reason    string          `json:"reason"`
	State     string          `json:"state" enum:"SCHEDULED,ACTIVE,REVOKED,EXPIRED"`
	EndReason *string         `json:"end_reason,omitempty"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	EndNote   *string         `json:"end_note,omitempty"`
	Version   int             `json:"version"`
	UseCount  *int64          `json:"use_count,omitempty"`
	Uses      []DelegationUse `json:"uses,omitempty"`
}

type DelegationUse struct {
	WorkItemID guid.ID   `json:"work_item_id"`
	DecisionID guid.ID   `json:"decision_id"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}
type DelegationGrantInput struct {
	StartsAt           *time.Time `json:"starts_at,omitempty" doc:"Optional future activation time; omitted grants immediately."`
	ProjectID          guid.ID    `json:"project_id"`
	GranteePrincipalID guid.ID    `json:"grantee_principal_id"`
	DelegationConditions
	DurationMinutes      int      `json:"duration_minutes" minimum:"15" maximum:"43200"`
	Reason               string   `json:"reason" minLength:"10" maxLength:"2000"`
	ReplacesDelegationID *guid.ID `json:"replaces_delegation_id,omitempty"`
	ReauthProof          string   `json:"reauth_proof" maxLength:"256"`
}

func delegationView(r *model.Delegation) (DelegationView, error) {
	var c DelegationConditions
	if r == nil || json.Unmarshal([]byte(r.ConditionsJSON), &c) != nil || !validDelegationConditions(c) {
		return DelegationView{}, ErrUnavailable
	}
	state := r.State
	if pendingDelegation(r) {
		state = "SCHEDULED"
	}
	return DelegationView{ID: r.ID, ProjectID: r.ProjectID, GranteePrincipalID: r.GranteePrincipalID, GranteeAgentID: r.GranteeAgentID, GrantorPrincipalID: r.GrantorPrincipalID, DelegationConditions: c, StartsAt: r.StartsAt, EndsAt: r.EndsAt, Reason: r.Reason, State: state, EndReason: r.EndReason, EndedAt: r.EndedAt, EndNote: r.EndNote, Version: r.Version}, nil
}

func (m *Module) delegationAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string) error {
	if m == nil || m.seed == nil || m.explain == nil || m.delegations.members == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if tenant <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if e := m.principals.Lock(ctx, tx, actor); e != nil {
		return e
	}
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !active || human.ID != actor {
		return ErrForbidden
	}
	active, e = m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if e != nil {
		return e
	}
	if !active {
		return ErrForbidden
	}
	explanation, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, EntityID: project, PrincipalID: actor, Permission: permission})
	if e != nil {
		return e
	}
	if !explanation.Allowed {
		return ErrForbidden
	}
	source, e := m.seed.roleAuthoritySource(ctx, tx, tenant, permission, explanation)
	if e != nil {
		return e
	}
	if source.source == "" {
		return ErrForbidden
	}
	return nil
}

func (m *Module) grantDelegationOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in DelegationGrantInput, cookie string) (DelegationView, error) {
	var out DelegationView
	if tx == nil || tx.Statement == nil {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return out, ErrForbidden
	}
	if in.ProjectID <= 0 || in.GranteePrincipalID <= 0 || !validDelegationConditions(in.DelegationConditions) || in.DurationMinutes < 15 || in.DurationMinutes > 43200 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || (in.ReplacesDelegationID != nil && *in.ReplacesDelegationID <= 0) {
		return out, ErrInvalidArgument
	}
	if m == nil || m.seed == nil || m.seed.ids == nil || m.reauth == nil || m.delegations.agents.ByPrincipalOn == nil || m.delegations.bindings.PutOn == nil || m.delegations.projects.InspectOn == nil {
		return out, ErrUnavailable
	}
	now := m.seed.now()
	startsAt := now
	if in.StartsAt != nil {
		startsAt = in.StartsAt.UTC().Truncate(time.Millisecond)
		if !startsAt.After(now) || in.ReplacesDelegationID != nil {
			return out, ErrInvalidArgument
		}
	}
	actor := authn.PrincipalIDFromContext(ctx)
	agentID, e := m.validateDelegationGrantOn(ctx, tx, tenant, in)
	if e != nil {
		return out, e
	}
	if e := m.reauth(ctx, tx, actor, cookie, in.ReauthProof, "delegation_grant", "project:"+in.ProjectID.String()+":grantee:"+in.GranteePrincipalID.String()); e != nil {
		return out, ErrForbidden
	}
	q := query.BindDelegations(tx, m.seed.names)
	activeKey := in.GranteePrincipalID.String() + ":" + in.ProjectID.String()
	old, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ActiveKey.Eq(activeKey)).Take()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return out, e
	}
	if old != nil {
		if in.ReplacesDelegationID == nil || *in.ReplacesDelegationID != old.ID {
			return out, ErrVersion
		}
		if e = m.endDelegationRowOn(ctx, tx, old, "REVOKED", "REPLACED", in.Reason); e != nil {
			return out, e
		}
	} else if in.ReplacesDelegationID != nil {
		return out, ErrVersion
	}
	id, e := m.seed.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	conditions, e := json.Marshal(in.DelegationConditions)
	if e != nil {
		return out, e
	}
	r := &model.Delegation{ID: guid.ID(id), TenantID: tenant, ProjectID: in.ProjectID, GranteePrincipalID: in.GranteePrincipalID, GranteeAgentID: agentID, GrantorPrincipalID: actor, ConditionsJSON: string(conditions), StartsAt: startsAt, EndsAt: startsAt.Add(time.Duration(in.DurationMinutes) * time.Minute), Reason: in.Reason, State: "ACTIVE", ActiveKey: &activeKey, ReplacesDelegationID: in.ReplacesDelegationID, Version: 1, CreatedAt: now, UpdatedAt: now}
	if in.StartsAt == nil {
		r.ActivatedAt = &now
	}
	bindings := []DelegationBinding{}
	for _, action := range in.Actions {
		key := "delegated_" + action
		role, e := m.roleIDOn(ctx, tx, tenant, key)
		if e != nil {
			return out, e
		}
		if in.StartsAt == nil {
			if _, _, e = m.delegations.bindings.PutOn(ctx, tx, tenant, in.ProjectID, role, in.GranteePrincipalID, core.BindingAllow, r.EndsAt); e != nil {
				return out, e
			}
		}
		bindings = append(bindings, DelegationBinding{RoleKey: key, RoleID: role})
	}
	encoded, e := json.Marshal(bindings)
	if e != nil {
		return out, e
	}
	r.IAMBindings = string(encoded)
	if e = q.WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	kind := "delegation.granted"
	if pendingDelegation(r) {
		kind = "delegation.scheduled"
	}
	if e = m.delegationEventOn(ctx, tx, r, kind, in.Reason, ""); e != nil {
		return out, e
	}
	return delegationView(r)
}

func (m *Module) endDelegationRowOn(ctx context.Context, tx *gorm.DB, r *model.Delegation, state, reason, note string, authority ...string) error {
	if m.delegations.grants.RemoveEntityRoleBinding == nil {
		return ErrUnavailable
	}
	if r.State != "ACTIVE" {
		return ErrVersion
	}
	var bindings []DelegationBinding
	if json.Unmarshal([]byte(r.IAMBindings), &bindings) != nil || len(bindings) == 0 {
		return ErrUnavailable
	}
	now := m.seed.now()
	actor := authn.PrincipalIDFromContext(ctx)
	var endedBy *guid.ID
	if actor > 0 {
		endedBy = &actor
	}
	q := query.BindDelegations(tx, m.seed.names)
	changed, e := q.WithContext(ctx).Where(q.ID.Eq(r.ID), q.TenantID.Eq(r.TenantID), q.State.Eq("ACTIVE"), q.Version.Eq(r.Version)).Updates(map[string]any{"state": state, "end_reason": reason, "end_note": note, "ended_at": now, "ended_by": endedBy, "active_key": nil, "version": r.Version + 1, "updated_at": now})
	if e != nil {
		return e
	}
	if changed.RowsAffected != 1 {
		return ErrVersion
	}
	for _, binding := range bindings {
		if pendingDelegation(r) {
			continue
		}
		if _, e := m.delegations.grants.RemoveEntityRoleBinding(ctx, tx, r.TenantID, r.ProjectID, binding.RoleID, r.GranteePrincipalID); e != nil {
			return e
		}
	}
	r.State = state
	r.EndReason = &reason
	r.EndNote = &note
	r.EndedAt = &now
	r.EndedBy = endedBy
	r.ActiveKey = nil
	r.Version++
	kind := "delegation.revoked"
	if state == "EXPIRED" {
		kind = "delegation.expired"
	}
	return m.delegationEventOn(ctx, tx, r, kind, note, reason, authority...)
}

func (m *Module) delegationEventOn(ctx context.Context, tx *gorm.DB, r *model.Delegation, kind, note, endReason string, authority ...string) error {
	if m.seed.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	actorKind, source := "HUMAN", ""
	if len(authority) > 0 && actor > 0 {
		source = authority[0]
	} else if actor == 0 {
		actorKind, source = "SYSTEM", "SYSTEM:governance.delegation_expiry"
		if kind == "delegation.activated" {
			source = "SYSTEM:governance.delegation_activation"
		}
	} else {
		x, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: r.TenantID, EntityID: r.ProjectID, PrincipalID: actor, Permission: "delegation_grant"})
		if e != nil {
			return e
		}
		a, e := m.seed.roleAuthoritySource(ctx, tx, r.TenantID, "delegation_grant", x)
		if e != nil {
			return e
		}
		if a.source == "" {
			return ErrForbidden
		}
		source = a.source
	}
	detail := map[string]any{"reason": note, "end_reason": endReason, "project_id": r.ProjectID.String(), "grantee_principal_id": r.GranteePrincipalID.String(), "authority_source": source, "actor_kind": actorKind, "evidence_refs": []string{}}
	if e := m.seed.audit(ctx, tx, audit.DomainEvent{TenantID: r.TenantID, PrincipalID: authn.ActorIDFromContext(ctx), EventType: "governance." + strings.ReplaceAll(kind, ".", "_"), TargetType: "delegation", TargetID: r.ID, Detail: detail}); e != nil {
		return e
	}
	_, e := m.publisher.Publish(ctx, tx, events.Event{TenantID: r.TenantID, ProjectID: r.ProjectID, Type: kind, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "delegation", ID: r.ID}, SubjectVersion: int64(r.Version), Data: detail})
	return e
}

func (m *Module) expireDelegations(ctx context.Context, job jobs.Job) error {
	if job.TenantID != 0 {
		return jobs.Permanent(ErrInvalidArgument)
	}
	db, e := m.writer()
	if e != nil {
		return e
	}
	// Bound each transaction; an overdue backlog is drained by subsequent ticks.
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.BindDelegations(tx, m.seed.names)
		rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.State.Eq("ACTIVE"), q.EndsAt.Lte(m.seed.now())).Order(q.EndsAt, q.ID).Limit(100).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			if e = m.endDelegationRowOn(ctx, tx, r, "EXPIRED", "WINDOW_ENDED", "Delegation window ended"); e != nil {
				return e
			}
		}
		return nil
	})
}

func (m *Module) revokeDelegationOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, note string) (DelegationView, error) {
	var out DelegationView
	if id <= 0 || utf8.RuneCountInString(strings.TrimSpace(note)) < 5 || utf8.RuneCountInString(note) > 2000 {
		return out, ErrInvalidArgument
	}
	if tx == nil || tx.Statement == nil {
		return out, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return out, ErrForbidden
	}
	if m == nil || m.seed == nil {
		return out, ErrUnavailable
	}
	q := query.BindDelegations(tx, m.seed.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = m.delegationAuthorityOn(ctx, tx, tenant, r.ProjectID, "delegation_grant"); e != nil {
		return out, e
	}
	r, e = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if e != nil {
		return out, e
	}
	if r.State == "REVOKED" {
		return delegationView(r)
	}
	if r.State != "ACTIVE" {
		return out, ErrVersion
	}
	if e = m.endDelegationRowOn(ctx, tx, r, "REVOKED", "MANUAL", note); e != nil {
		return out, e
	}
	return delegationView(r)
}

// validateDelegationGrantOn is shared by issuance and delayed activation; no saved authorization decision is reused.
func (m *Module) validateDelegationGrantOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in DelegationGrantInput) (guid.ID, error) {
	// Lock participating principals in numeric order before any project/row lock.
	actor := authn.PrincipalIDFromContext(ctx)
	ids := []guid.ID{actor, in.GranteePrincipalID}
	slices.Sort(ids)
	if m.principals.Lock == nil {
		return 0, ErrUnavailable
	}
	for _, id := range slices.Compact(ids) {
		if e := m.principals.Lock(ctx, tx, id); e != nil {
			return 0, e
		}
	}
	if e := m.delegationAuthorityOn(ctx, tx, tenant, in.ProjectID, "delegation_grant"); e != nil {
		return 0, e
	}
	p, e := m.delegations.projects.InspectOn(ctx, tx, tenant, in.ProjectID)
	if e != nil {
		return 0, e
	}
	if p.Stage == "CLOSED" || p.Stage == "CANCELLED" { //nolint:misspell // Frozen state.
		return 0, ErrInvalidArgument
	}
	settings, e := m.readSettingsOn(ctx, tx, tenant)
	if e != nil {
		return 0, e
	}
	if in.DurationMinutes > settings.DelegationMaxDays*1440 || in.CostCapMicros > settings.DelegationMaxCostCapMicros {
		return 0, ErrInvalidArgument
	}
	member, e := m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, in.GranteePrincipalID)
	if e != nil {
		return 0, e
	}
	if !member {
		return 0, ErrForbidden
	}
	agent, found, e := m.delegations.agents.ByPrincipalOn(ctx, tx, tenant, in.GranteePrincipalID)
	if e != nil {
		return 0, e
	}
	if !found || agent.ID <= 0 || agent.PrincipalID != in.GranteePrincipalID || agent.TenantID != tenant || agent.State != "ACTIVE" || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
		return 0, ErrForbidden
	}
	for _, action := range in.Actions {
		if action == "intake_convert" {
			if p.Mode != "AUTONOMOUS" || (!slices.Contains(agent.Roles, "COORDINATOR") && !slices.Contains(agent.Roles, "REVIEWER")) {
				return 0, ErrInvalidArgument
			}
		} else if !slices.Contains(agent.Roles, "REVIEWER") {
			return 0, ErrForbidden
		}
		if e := m.delegationAuthorityOn(ctx, tx, tenant, in.ProjectID, action); e != nil {
			return 0, e
		}
	}
	return agent.ID, nil
}
