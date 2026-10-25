package governance

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SpawnGrantUpdate struct {
	ProjectIDs         *[]guid.ID           `json:"project_ids,omitempty" maxItems:"200"`
	MaxActiveAgents    *int                 `json:"max_active_agents,omitempty" minimum:"1" maximum:"10"`
	MaxSpawnsPerDay    *int                 `json:"max_spawns_per_day,omitempty" minimum:"1" maximum:"20"`
	AllowedRoles       *[]string            `json:"allowed_roles,omitempty" minItems:"1" maxItems:"2"`
	AllowedMachineIDs  *[]guid.ID           `json:"allowed_machine_ids,omitempty" minItems:"1" maxItems:"100"`
	AllowedModels      *[]SpawnAllowedModel `json:"allowed_models,omitempty" minItems:"1" maxItems:"20"`
	MaxEffort          *string              `json:"max_effort,omitempty"`
	BudgetCapMicros    *int64               `json:"budget_cap_micros,omitempty" minimum:"1000000"`
	PerAgentCapMicros  *int64               `json:"per_agent_cap_micros,omitempty" minimum:"1"`
	AgentTTLHours      *int                 `json:"agent_ttl_hours,omitempty" minimum:"24" maximum:"720"`
	IdleRetireHours    *int                 `json:"idle_retire_hours,omitempty" minimum:"4" maximum:"336"`
	ApprovalMode       *string              `json:"approval_mode,omitempty" enum:"REQUIRE_APPROVAL,AUTO"`
	EndsAt             *time.Time           `json:"ends_at,omitempty"`
	Reason             string               `json:"reason" minLength:"10" maxLength:"2000"`
	TeamScopeConfirmed bool                 `json:"team_scope_confirmed"`
	ReauthProof        string               `json:"reauth_proof,omitempty" maxLength:"256"`
}

func applySpawnGrantUpdate(s SpawnGrantScope, in SpawnGrantUpdate) SpawnGrantScope {
	if in.ProjectIDs != nil {
		s.ProjectIDs = *in.ProjectIDs
	}
	if in.MaxActiveAgents != nil {
		s.MaxActiveAgents = *in.MaxActiveAgents
	}
	if in.MaxSpawnsPerDay != nil {
		s.MaxSpawnsPerDay = *in.MaxSpawnsPerDay
	}
	if in.AllowedRoles != nil {
		s.AllowedRoles = *in.AllowedRoles
	}
	if in.AllowedMachineIDs != nil {
		s.AllowedMachineIDs = *in.AllowedMachineIDs
	}
	if in.AllowedModels != nil {
		s.AllowedModels = *in.AllowedModels
	}
	if in.MaxEffort != nil {
		s.MaxEffort = *in.MaxEffort
	}
	if in.BudgetCapMicros != nil {
		s.BudgetCapMicros = *in.BudgetCapMicros
	}
	if in.PerAgentCapMicros != nil {
		s.PerAgentCapMicros = *in.PerAgentCapMicros
	}
	if in.AgentTTLHours != nil {
		s.AgentTTLHours = *in.AgentTTLHours
	}
	if in.IdleRetireHours != nil {
		s.IdleRetireHours = *in.IdleRetireHours
	}
	if in.ApprovalMode != nil {
		s.ApprovalMode = *in.ApprovalMode
	}
	return s
}
func spawnScopeBroadened(old, next SpawnGrantScope) bool {
	if next.MaxActiveAgents > old.MaxActiveAgents || next.MaxSpawnsPerDay > old.MaxSpawnsPerDay || next.BudgetCapMicros > old.BudgetCapMicros || next.PerAgentCapMicros > old.PerAgentCapMicros || next.AgentTTLHours > old.AgentTTLHours || next.IdleRetireHours > old.IdleRetireHours || spawnEffortRank(next.MaxEffort) > spawnEffortRank(old.MaxEffort) || (next.ApprovalMode == "AUTO" && old.ApprovalMode != "AUTO") {
		return true
	}
	if len(old.ProjectIDs) > 0 {
		if len(next.ProjectIDs) == 0 {
			return true
		}
		for _, id := range next.ProjectIDs {
			if !slices.Contains(old.ProjectIDs, id) {
				return true
			}
		}
	}
	for _, id := range next.AllowedMachineIDs {
		if !slices.Contains(old.AllowedMachineIDs, id) {
			return true
		}
	}
	for _, role := range next.AllowedRoles {
		if !slices.Contains(old.AllowedRoles, role) {
			return true
		}
	}
	for _, model := range next.AllowedModels {
		if !slices.Contains(old.AllowedModels, model) {
			return true
		}
	}
	return false
}

func (m *Module) lockSpawnGrantOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.SpawnGrant, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	// Read the immutable principal reference first, then obey principal -> grant
	// locking order shared with reserve and replacement.
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), r.GranteePrincipalID}
	slices.Sort(ids)
	if m.principals.Lock == nil {
		return nil, ErrUnavailable
	}
	for _, principal := range slices.Compact(ids) {
		if e = m.principals.Lock(ctx, tx, principal); e != nil {
			return nil, e
		}
	}
	if e = m.delegationAuthorityOn(ctx, tx, tenant, 0, "spawn_grant_manage"); e != nil {
		return nil, e
	}
	return q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
}

func (m *Module) updateSpawnGrantOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, in SpawnGrantUpdate, cookie string) (SpawnGrantView, error) {
	var out SpawnGrantView
	if version < 1 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 10 || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrSpawnScope
	}
	row, e := m.lockSpawnGrantOn(ctx, tx, tenant, id)
	if e != nil {
		return out, e
	}
	if row.Version != version || !slices.Contains([]string{"ACTIVE", "EXHAUSTED"}, row.State) || !row.EndsAt.After(m.seed.now()) {
		return out, ErrVersion
	}
	var old SpawnGrantScope
	if json.Unmarshal([]byte(row.ScopeJSON), &old) != nil {
		return out, ErrUnavailable
	}
	next := applySpawnGrantUpdate(old, in)
	ends := row.EndsAt
	if in.EndsAt != nil {
		ends = in.EndsAt.UTC()
	}
	if !validSpawnGrantScope(next) || !ends.After(m.seed.now()) || ends.After(row.StartsAt.Add(90*24*time.Hour)) || (!ends.After(row.StartsAt.Add(time.Hour))) || (len(next.ProjectIDs) == 0 && len(old.ProjectIDs) > 0 && !in.TeamScopeConfirmed) {
		return out, ErrSpawnScope
	}
	today := row.SpawnedToday
	if row.SpawnedDay != m.seed.now().UTC().Format("2006-01-02") {
		today = 0
	}
	if next.MaxActiveAgents < row.ActiveCount || next.MaxSpawnsPerDay < today || next.BudgetCapMicros < row.SpentMicros {
		return out, ErrSpawnQuota
	}
	mode, e := m.spawnScopeOn(ctx, tx, tenant, next)
	if e != nil {
		return out, e
	}
	if mode != next.ApprovalMode {
		return out, ErrSpawnScope
	}
	if spawnScopeBroadened(old, next) || ends.After(row.EndsAt) {
		if m.reauth == nil {
			return out, ErrUnavailable
		}
		if e = m.reauth(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, in.ReauthProof, "spawn_grant_manage", "agent:"+row.GranteeAgentID.String()); e != nil {
			return out, e
		}
	}
	encoded, e := json.Marshal(next)
	if e != nil {
		return out, e
	}
	state := row.State
	if state == "EXHAUSTED" && next.BudgetCapMicros > row.SpentMicros {
		state = "ACTIVE"
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"scope_json": string(encoded), "ends_at": ends, "state": state, "version": version + 1, "updated_at": m.seed.now()})
	if e != nil {
		return out, e
	}
	if changed.RowsAffected != 1 {
		return out, ErrVersion
	}
	row.ScopeJSON, row.EndsAt, row.State, row.Version = string(encoded), ends, state, version+1
	if e = m.spawnEventOn(ctx, tx, row, "spawn_grant.updated", in.Reason, authn.PrincipalIDFromContext(ctx)); e != nil {
		return out, e
	}
	return m.spawnGrantViewOn(ctx, tx, row)
}

func (m *Module) revokeSpawnGrantOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, reason string) (SpawnGrantView, error) {
	if version < 1 || utf8.RuneCountInString(strings.TrimSpace(reason)) < 10 || utf8.RuneCountInString(reason) > 2000 {
		return SpawnGrantView{}, ErrSpawnScope
	}
	r, e := m.lockSpawnGrantOn(ctx, tx, tenant, id)
	if e != nil {
		return SpawnGrantView{}, e
	}
	if r.Version != version {
		return SpawnGrantView{}, ErrVersion
	}
	if e = m.endSpawnGrantOn(ctx, tx, r, "REVOKED", "MANUAL", reason, authn.PrincipalIDFromContext(ctx)); e != nil {
		return SpawnGrantView{}, e
	}
	return m.spawnGrantViewOn(ctx, tx, r)
}
