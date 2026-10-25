package governance

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSpawnScope  = errors.New("governance: invalid spawn grant scope")
	ErrSpawnQuota  = errors.New("governance: spawn quota exceeded")
	ErrSpawnBudget = errors.New("governance: spawn budget exhausted")
	ErrSpawnActive = errors.New("governance: supervisor already has a live grant")
)

type SpawnAllowedModel struct {
	Runtime string `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	Model   string `json:"model" minLength:"1" maxLength:"160"`
}
type SpawnGrantScope struct {
	ProjectIDs        []guid.ID           `json:"project_ids" maxItems:"200"`
	MaxActiveAgents   int                 `json:"max_active_agents" minimum:"1" maximum:"10"`
	MaxSpawnsPerDay   int                 `json:"max_spawns_per_day" minimum:"1" maximum:"20"`
	AllowedRoles      []string            `json:"allowed_roles" minItems:"1" maxItems:"2"`
	AllowedMachineIDs []guid.ID           `json:"allowed_machine_ids" minItems:"1" maxItems:"100"`
	AllowedModels     []SpawnAllowedModel `json:"allowed_models" minItems:"1" maxItems:"20"`
	MaxEffort         string              `json:"max_effort" enum:"minimal,low,medium,high,xhigh,max,ultra,persistent"`
	BudgetCapMicros   int64               `json:"budget_cap_micros" minimum:"1000000"`
	PerAgentCapMicros int64               `json:"per_agent_cap_micros" minimum:"1"`
	AgentTTLHours     int                 `json:"agent_ttl_hours" minimum:"24" maximum:"720"`
	IdleRetireHours   int                 `json:"idle_retire_hours" minimum:"4" maximum:"336"`
	ApprovalMode      string              `json:"approval_mode" enum:"REQUIRE_APPROVAL,AUTO"`
}
type SpawnGrantInput struct {
	SpawnGrantScope
	GranteeAgentID     guid.ID  `json:"grantee_agent_id"`
	DurationHours      int      `json:"duration_hours" minimum:"2" maximum:"2160"`
	Reason             string   `json:"reason" minLength:"10" maxLength:"2000"`
	ReplacesGrantID    *guid.ID `json:"replaces_grant_id,omitempty"`
	TeamScopeConfirmed bool     `json:"team_scope_confirmed"`
	ReauthProof        string   `json:"reauth_proof" maxLength:"256"`
}
type SpawnGrantView struct {
	ID                 guid.ID `json:"id"`
	GranteeAgentID     guid.ID `json:"grantee_agent_id"`
	GranteePrincipalID guid.ID `json:"grantee_principal_id"`
	GrantedBy          guid.ID `json:"granted_by"`
	SpawnGrantScope
	State                 string    `json:"state" enum:"ACTIVE,EXHAUSTED,REVOKED,EXPIRED"`
	EffectiveState        string    `json:"effective_state" enum:"ACTIVE,EXHAUSTED,REVOKED,EXPIRED"`
	EffectiveApprovalMode string    `json:"effective_approval_mode" enum:"AUTO,REQUIRE_APPROVAL"`
	ActiveCount           int       `json:"active_count"`
	SpawnedToday          int       `json:"spawned_today"`
	SpentMicros           string    `json:"spent_micros"`
	Reason                string    `json:"reason"`
	StartsAt              time.Time `json:"starts_at"`
	EndsAt                time.Time `json:"ends_at"`
	EndReason             *string   `json:"end_reason,omitempty"`
	Version               int       `json:"version"`
}

func validSpawnGrantScope(s SpawnGrantScope) bool {
	if s.MaxActiveAgents < 1 || s.MaxActiveAgents > 10 || s.MaxSpawnsPerDay < 1 || s.MaxSpawnsPerDay > 20 || s.BudgetCapMicros < 1000000 || s.PerAgentCapMicros < 1 || s.PerAgentCapMicros > s.BudgetCapMicros || s.AgentTTLHours < 24 || s.AgentTTLHours > 720 || s.IdleRetireHours < 4 || s.IdleRetireHours > 336 || !slices.Contains([]string{"REQUIRE_APPROVAL", "AUTO"}, s.ApprovalMode) || spawnEffortRank(s.MaxEffort) < 0 {
		return false
	}
	if len(s.ProjectIDs) > 200 || len(s.AllowedRoles) < 1 || len(s.AllowedRoles) > 2 || len(s.AllowedMachineIDs) < 1 || len(s.AllowedMachineIDs) > 100 || len(s.AllowedModels) < 1 || len(s.AllowedModels) > 20 {
		return false
	}
	for _, ids := range [][]guid.ID{s.ProjectIDs, s.AllowedMachineIDs} {
		seen := map[guid.ID]bool{}
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	roles := map[string]bool{}
	for _, role := range s.AllowedRoles {
		if !slices.Contains([]string{"EXECUTOR", "COORDINATOR"}, role) || roles[role] {
			return false
		}
		roles[role] = true
	}
	models := map[SpawnAllowedModel]bool{}
	for _, m := range s.AllowedModels {
		if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, m.Runtime) || m.Model == "" || utf8.RuneCountInString(m.Model) > 160 || strings.ContainsAny(m.Model, "\r\n\t ") || models[m] {
			return false
		}
		models[m] = true
	}
	return len(s.ProjectIDs) > 0 || s.ApprovalMode == "REQUIRE_APPROVAL"
}
func spawnEffortRank(e string) int {
	return slices.Index([]string{"minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}, e)
}

func (m *Module) spawnScopeOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, s SpawnGrantScope) (string, error) {
	if !validSpawnGrantScope(s) {
		return "", ErrSpawnScope
	}
	if m.spawnDirectory.ModelOn == nil || m.spawnDirectory.MachineOn == nil || m.delegations.projects.InspectOn == nil {
		return "", ErrUnavailable
	}
	mode := s.ApprovalMode
	for _, project := range s.ProjectIDs {
		p, e := m.delegations.projects.InspectOn(ctx, tx, tenant, project)
		if e != nil {
			return "", e
		}
		if p.Stage == "CLOSED" || p.Stage == "CANCELLED" { //nolint:misspell // Frozen state.
			return "", ErrSpawnScope
		}
		if p.Mode != "AUTONOMOUS" {
			mode = "REQUIRE_APPROVAL"
		}
	}
	for _, machine := range s.AllowedMachineIDs {
		fact, e := m.spawnDirectory.MachineOn(ctx, tx, tenant, machine)
		if e != nil {
			return "", e
		}
		if fact.State == "REVOKED" {
			return "", ErrSpawnScope
		}
	}
	for _, model := range s.AllowedModels {
		listed := false
		for _, machine := range s.AllowedMachineIDs {
			ok, e := m.spawnDirectory.ModelOn(ctx, tx, tenant, machine, model.Runtime, model.Model)
			if e != nil {
				return "", e
			}
			listed = listed || ok
		}
		if !listed {
			return "", ErrSpawnScope
		}
	}
	return mode, nil
}

func (m *Module) spawnGrantViewOn(ctx context.Context, tx *gorm.DB, r *model.SpawnGrant) (SpawnGrantView, error) {
	var s SpawnGrantScope
	if r == nil || json.Unmarshal([]byte(r.ScopeJSON), &s) != nil || !validSpawnGrantScope(s) {
		return SpawnGrantView{}, ErrUnavailable
	}
	state := r.State
	if (state == "ACTIVE" || state == "EXHAUSTED") && !r.EndsAt.After(m.seed.now()) {
		state = "EXPIRED"
	}
	mode := s.ApprovalMode
	// Effective approval mode uses current project facts; inventory outages must
	// not make an otherwise readable grant disappear.
	if mode == "AUTO" {
		if m.delegations.projects.InspectOn == nil {
			return SpawnGrantView{}, ErrUnavailable
		}
		for _, id := range s.ProjectIDs {
			p, e := m.delegations.projects.InspectOn(ctx, tx, r.TenantID, id)
			if e != nil {
				return SpawnGrantView{}, e
			}
			if p.Mode != "AUTONOMOUS" {
				mode = "REQUIRE_APPROVAL"
			}
		}
	}
	today := r.SpawnedToday
	if r.SpawnedDay != m.seed.now().UTC().Format("2006-01-02") {
		today = 0
	}
	return SpawnGrantView{ID: r.ID, GranteeAgentID: r.GranteeAgentID, GranteePrincipalID: r.GranteePrincipalID, GrantedBy: r.GrantedBy, SpawnGrantScope: s, State: r.State, EffectiveState: state, EffectiveApprovalMode: mode, ActiveCount: r.ActiveCount, SpawnedToday: today, SpentMicros: fmtInt64(r.SpentMicros), Reason: r.Reason, StartsAt: r.StartsAt, EndsAt: r.EndsAt, EndReason: r.EndReason, Version: r.Version}, nil
}

func (m *Module) grantSpawnOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in SpawnGrantInput, cookie string) (SpawnGrantView, error) {
	var out SpawnGrantView
	if !validSpawnGrantScope(in.SpawnGrantScope) || in.GranteeAgentID <= 0 || in.DurationHours < 2 || in.DurationHours > 2160 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || (len(in.ProjectIDs) == 0 && !in.TeamScopeConfirmed) {
		return out, ErrSpawnScope
	}
	if m == nil || m.seed == nil || m.spawnDirectory.AgentOn == nil || m.reauth == nil || m.principals.Lock == nil || m.spawnFence == nil {
		return out, ErrUnavailable
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
	agent, found, e := m.spawnDirectory.AgentOn(ctx, tx, tenant, in.GranteeAgentID)
	if e != nil {
		return out, e
	}
	if !found || agent.ID != in.GranteeAgentID || agent.TenantID != tenant || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
		return out, ErrSpawnScope
	}
	actor := authn.PrincipalIDFromContext(ctx)
	ids := []guid.ID{actor, agent.PrincipalID}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if e = m.principals.Lock(ctx, tx, id); e != nil {
			return out, e
		}
	}
	if e = m.delegationAuthorityOn(ctx, tx, tenant, 0, "spawn_grant_manage"); e != nil {
		return out, e
	}
	if e = m.spawnFence(ctx, tx, "governance:spawn-grants:"+tenant.String()); e != nil {
		return out, e
	}
	agent, found, e = m.spawnDirectory.AgentOn(ctx, tx, tenant, in.GranteeAgentID)
	if e != nil {
		return out, e
	}
	if !found || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
		return out, ErrSpawnScope
	}
	active, e := m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, agent.PrincipalID)
	if e != nil {
		return out, e
	}
	if !active {
		return out, ErrSpawnScope
	}
	mode, e := m.spawnScopeOn(ctx, tx, tenant, in.SpawnGrantScope)
	if e != nil {
		return out, e
	}
	if mode != in.ApprovalMode {
		return out, ErrSpawnScope
	}
	if e = m.reauth(ctx, tx, actor, cookie, in.ReauthProof, "spawn_grant_manage", "agent:"+in.GranteeAgentID.String()); e != nil {
		return out, e
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	// The Human principal lock serializes team-level grants by this owner;
	// supervisor locking plus a unique slot resolves competing grantors.
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.State.In("ACTIVE", "EXHAUSTED")).Find()
	if e != nil {
		return out, e
	}
	live := 0
	replaced := false
	for _, r := range rows {
		live++
		if r.GranteeAgentID == in.GranteeAgentID {
			if in.ReplacesGrantID == nil || *in.ReplacesGrantID != r.ID {
				return out, ErrSpawnActive
			}
			if e = m.endSpawnGrantOn(ctx, tx, r, "REVOKED", "REPLACED", in.Reason, actor); e != nil {
				return out, e
			}
			live--
			replaced = true
		} else if in.ReplacesGrantID != nil && *in.ReplacesGrantID == r.ID {
			return out, ErrSpawnScope
		}
	}
	if live >= 20 {
		return out, ErrSpawnQuota
	}
	if in.ReplacesGrantID != nil {
		if !replaced {
			return out, ErrSpawnScope
		}
	}
	id, e := m.seed.ids.Next()
	if e != nil {
		return out, e
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	encoded, e := json.Marshal(in.SpawnGrantScope)
	if e != nil {
		return out, e
	}
	now := m.seed.now()
	key := in.GranteeAgentID.String()
	r := &model.SpawnGrant{ID: guid.ID(id), TenantID: tenant, GranteeAgentID: in.GranteeAgentID, GranteePrincipalID: agent.PrincipalID, GrantedBy: actor, ScopeJSON: string(encoded), Reason: in.Reason, StartsAt: now, EndsAt: now.Add(time.Duration(in.DurationHours) * time.Hour), State: "ACTIVE", ActiveKey: &key, SpawnedDay: now.UTC().Format("2006-01-02"), ReplacesGrantID: in.ReplacesGrantID, Version: 1, CreatedAt: now, UpdatedAt: now}
	if e = q.WithContext(ctx).Create(r); e != nil {
		return out, e
	}
	if e = m.spawnEventOn(ctx, tx, r, "spawn_grant.granted", in.Reason, actor); e != nil {
		return out, e
	}
	return m.spawnGrantViewOn(ctx, tx, r)
}

func (m *Module) spawnEventOn(ctx context.Context, tx *gorm.DB, r *model.SpawnGrant, kind, reason string, actor guid.ID, memberCap ...guid.ID) error {
	return m.spawnEventWithAuthorityOn(ctx, tx, r, kind, reason, actor, "", memberCap...)
}

// A takeover has already checked its distinct Human authority. Reusing that
// source avoids incorrectly requiring spawn_grant_manage for the same command.
func (m *Module) spawnEventWithAuthorityOn(ctx context.Context, tx *gorm.DB, r *model.SpawnGrant, kind, reason string, actor guid.ID, authority string, memberCap ...guid.ID) error {
	if m.publisher == nil || m.seed.audit == nil {
		return ErrUnavailable
	}
	actorKind, source := "SYSTEM", "SYSTEM:spawn_grant_reconcile"
	if actor > 0 && authority != "" {
		actorKind, source = "HUMAN", authority
	} else if actor > 0 {
		actorKind = "HUMAN"
		x, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: r.TenantID, PrincipalID: actor, Permission: "spawn_grant_manage"})
		if e != nil {
			return e
		}
		a, e := m.seed.roleAuthoritySource(ctx, tx, r.TenantID, "spawn_grant_manage", x)
		if e != nil {
			return e
		}
		if a.source == "" {
			return ErrForbidden
		}
		source = a.source
	}
	detail := map[string]any{"reason": reason, "evidence_refs": []string{}, "authority_source": source, "actor_kind": actorKind, "project_id": nil, "grant_id": r.ID.String(), "grantee_agent_id": r.GranteeAgentID.String(), "state": r.State, "version": r.Version, "scope": json.RawMessage(r.ScopeJSON), "active_count": r.ActiveCount, "spent_micros": fmtInt64(r.SpentMicros)}
	detail["end_reason"], detail["starts_at"], detail["ends_at"] = r.EndReason, r.StartsAt, r.EndsAt
	detail["granted_by"] = r.GrantedBy.String()
	if len(memberCap) == 1 && memberCap[0] > 0 {
		detail["member_cap_reached"] = memberCap[0].String()
	}
	auditKind := kind
	if kind == "spawn_grant.expired" {
		auditKind = "spawn_grant.revoked"
	}
	if e := m.seed.audit(ctx, tx, audit.DomainEvent{TenantID: r.TenantID, PrincipalID: actor, EventType: "governance." + strings.ReplaceAll(auditKind, ".", "_"), TargetType: "spawn_grant", TargetID: r.ID, Detail: detail}); e != nil {
		return e
	}
	_, e := m.publisher.Publish(ctx, tx, events.Event{TenantID: r.TenantID, Type: kind, Actor: events.Actor{Kind: actorKind, ID: actor, AuthoritySource: source}, Subject: events.Subject{Kind: "spawn_grant", ID: r.ID}, SubjectVersion: int64(r.Version), Data: detail})
	return e
}

func (m *Module) endSpawnGrantOn(ctx context.Context, tx *gorm.DB, r *model.SpawnGrant, state, reason, note string, actor guid.ID, authority ...string) error {
	if r.State != "ACTIVE" && r.State != "EXHAUSTED" {
		return ErrVersion
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	now := m.seed.now()
	expected := r.Version
	r.State, r.ActiveKey, r.EndReason, r.EndedAt, r.EndNote, r.UpdatedAt, r.ActiveCount, r.Version = state, nil, &reason, &now, &note, now, 0, expected+1
	if actor > 0 {
		r.EndedBy = &actor
	}
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID), q.Version.Eq(expected)).Updates(map[string]any{"state": state, "active_key": nil, "active_count": 0, "end_reason": reason, "ended_at": now, "end_note": note, "ended_by": r.EndedBy, "updated_at": now, "version": r.Version})
	if e != nil {
		return e
	}
	if changed.RowsAffected != 1 {
		return ErrVersion
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	if _, e = members.WithContext(ctx).Where(members.TenantID.Eq(r.TenantID), members.GrantID.Eq(r.ID), members.State.Eq("ACTIVE")).Updates(map[string]any{"state": "RELEASED", "release_reason": "GRANT_ENDED", "released_at": now}); e != nil {
		return e
	}
	kind := "spawn_grant.revoked"
	if state == "EXPIRED" {
		kind = "spawn_grant.expired"
	}
	source := ""
	if len(authority) > 0 {
		source = authority[0]
	}
	return m.spawnEventWithAuthorityOn(ctx, tx, r, kind, note, actor, source)
}

// fmtInt64 preserves micros precisely across JSON/browser boundaries.
func fmtInt64(v int64) string { return strconv.FormatInt(v, 10) }
