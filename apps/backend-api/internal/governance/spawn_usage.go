package governance

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SpawnGrantUsage must run in the Agent owner's creation/lifecycle transaction.
// Reservations validate persisted child identities; this is not an HTTP quota API.
type SpawnGrantUsage struct {
	Reserve func(context.Context, *gorm.DB, guid.ID, guid.ID, AuthorityRequest) error
	Release func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
}

func (m *Module) checkSpawnAuthority(ctx context.Context, tx *gorm.DB, req AuthorityRequest) (AuthorityDecision, error) {
	return m.checkSpawnAuthorityUsage(ctx, tx, req, false)
}

func (m *Module) checkSpawnAuthorityUsage(ctx context.Context, tx *gorm.DB, req AuthorityRequest, reserved bool) (AuthorityDecision, error) {
	result := AuthorityDecision{Steps: []AuthorityStep{{Step: "S0", Outcome: "PASS"}}}
	deny := func(reason string) (AuthorityDecision, error) { result.ReasonCode = reason; return result, nil }
	if req.ActorKind != "AGENT" || req.Spawn == nil || req.ProjectID != 0 || req.Facts != nil || req.Resource.ID != 0 || req.Environment == "PRODUCTION" {
		return deny("INVALID_REQUEST")
	}
	if tx == nil {
		db, e := m.writer()
		if e != nil {
			return result, e
		}
		var out AuthorityDecision
		e = db.WithContext(ctx).Transaction(func(work *gorm.DB) error { var err error; out, err = m.checkSpawnAuthority(ctx, work, req); return err })
		return out, e
	}
	if tx.Statement == nil {
		return result, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return result, ErrInvalidArgument
	}
	if m.spawnDirectory.AgentOn == nil || m.principals.LockActiveRow == nil || m.delegations.members == nil || m.explain == nil {
		return result, ErrUnavailable
	}
	_, active, e := m.principals.LockActiveRow(ctx, tx, req.PrincipalID)
	if e != nil {
		return result, e
	}
	if !active {
		return deny("PRINCIPAL_INACTIVE")
	}
	active, e = m.delegations.members.IsMemberActiveOn(ctx, tx, req.TenantID, req.PrincipalID)
	if e != nil {
		return result, e
	}
	if !active {
		return deny("PRINCIPAL_INACTIVE")
	}
	agent, found, e := m.spawnDirectory.AgentOn(ctx, tx, req.TenantID, req.Spawn.SupervisorAgentID)
	if e != nil {
		return result, e
	}
	if !found || agent.PrincipalID != req.PrincipalID || agent.TenantID != req.TenantID || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
		return deny("SPAWN_NOT_LEAD")
	}
	x, e := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, PrincipalID: req.PrincipalID, Permission: "agent_spawn"})
	if e != nil {
		return result, e
	}
	result.PolicyRevision = x.Revision
	if x.Reason == "explicit_deny" || x.Reason == "administrator_scope_denied" {
		return deny("EXPLICIT_DENY")
	}
	// agent_spawn is PolicyOnly: role allows never authorize a creation.
	q := query.BindSpawnGrants(tx, m.seed.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(req.TenantID), q.GranteeAgentID.Eq(agent.ID), q.State.Eq("ACTIVE"), q.StartsAt.Lte(m.seed.now()), q.EndsAt.Gt(m.seed.now())).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return deny("SPAWN_GRANT_MISSING")
	}
	if e != nil {
		return result, e
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(row.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
		return result, ErrUnavailable
	}
	usage := *row
	if reserved {
		usage.ActiveCount, usage.SpawnedToday, usage.SpentMicros = 0, 0, 0
	} else {
		pending, err := m.pendingAgentSpawnRequestsOn(ctx, tx, req.TenantID, agent.ID, row.ID)
		if err != nil {
			return result, err
		}
		usage.ActiveCount += pending
		if usage.SpawnedDay != m.seed.now().UTC().Format("2006-01-02") {
			usage.SpawnedDay = m.seed.now().UTC().Format("2006-01-02")
			usage.SpawnedToday = 0
		}
		usage.SpawnedToday += pending
	}
	if reason := spawnRequestReason(scope, &usage, req.Spawn, m.seed.now().UTC().Format("2006-01-02")); reason != "" {
		return deny(reason)
	}
	if _, e = m.spawnScopeOn(ctx, tx, req.TenantID, scope); e != nil {
		return result, e
	}
	listed, e := m.spawnDirectory.ModelOn(ctx, tx, req.TenantID, req.Spawn.MachineID, req.Spawn.Runtime, req.Spawn.Model)
	if e != nil {
		return result, e
	}
	if !listed {
		return deny("SPAWN_GRANT_SCOPE")
	}
	result.Allowed = true
	result.AuthoritySource = "POLICY:spawn_grant"
	result.Steps = append(result.Steps, AuthorityStep{Step: "S7", Outcome: "MATCH"})
	return result, nil
}

func spawnRequestReason(s SpawnGrantScope, row *model.SpawnGrant, f *SpawnRequestFacts, today string) string {
	if !slices.Contains(s.AllowedRoles, f.Role) || !slices.Contains(s.AllowedMachineIDs, f.MachineID) || !slices.Contains(s.AllowedModels, SpawnAllowedModel{Runtime: f.Runtime, Model: f.Model}) || spawnEffortRank(f.Effort) < 0 || spawnEffortRank(f.Effort) > spawnEffortRank(s.MaxEffort) || f.EstimatedCapMicros <= 0 || f.EstimatedCapMicros > s.PerAgentCapMicros {
		return "SPAWN_GRANT_SCOPE"
	}
	if f.Runtime == "CLAUDE_CODE" && !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, f.Effort) {
		return "SPAWN_GRANT_SCOPE"
	}
	seen := map[guid.ID]bool{}
	for _, id := range f.ProjectIDs {
		if id <= 0 || seen[id] || (len(s.ProjectIDs) > 0 && !slices.Contains(s.ProjectIDs, id)) {
			return "SPAWN_GRANT_SCOPE"
		}
		seen[id] = true
	}
	if row.ActiveCount >= s.MaxActiveAgents {
		return "SPAWN_QUOTA_EXCEEDED"
	}
	if row.SpawnedDay == today && row.SpawnedToday >= s.MaxSpawnsPerDay {
		return "SPAWN_DAILY_LIMIT"
	}
	if row.SpentMicros >= s.BudgetCapMicros {
		return "SPAWN_BUDGET_EXHAUSTED"
	}
	return ""
}

func (m *Module) reserveSpawnOn(ctx context.Context, tx *gorm.DB, tenant, child guid.ID, req AuthorityRequest) error {
	if tx == nil || tx.Statement == nil || child <= 0 || tenant != req.TenantID || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != req.PrincipalID {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if req.Spawn == nil || req.ActorKind != "AGENT" || req.Code != "agent_spawn" {
		return ErrInvalidArgument
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	prior, e := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(child)).First()
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		return e
	}
	reserved := e == nil && prior.State == "ACTIVE"
	decision, e := m.checkSpawnAuthorityUsage(ctx, tx, req, reserved)
	if e != nil {
		return e
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	q := query.BindSpawnGrants(tx, m.seed.names)
	row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.GranteeAgentID.Eq(req.Spawn.SupervisorAgentID), q.State.Eq("ACTIVE"), q.EndsAt.Gt(m.seed.now())).First()
	if e != nil {
		return e
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(row.ScopeJSON), &scope) != nil {
		return ErrUnavailable
	}
	old, e := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(child)).First()
	if e == nil {
		if old.GrantID == row.ID && old.State == "ACTIVE" {
			return nil
		}
		return ErrSpawnQuota
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return e
	}
	identity, found, e := m.spawnDirectory.AgentOn(ctx, tx, tenant, child)
	if e != nil {
		return e
	}
	if !found || identity.Origin != "AGENT_SPAWNED" || identity.ParentAgentID == nil || *identity.ParentAgentID != row.GranteeAgentID || identity.AdoptedAt != nil || identity.State != "ACTIVE" || len(identity.Roles) != 1 || identity.Roles[0] != req.Spawn.Role || identity.RuntimeKind != req.Spawn.Runtime || identity.ModelID != req.Spawn.Model {
		return ErrSpawnScope
	}
	today := m.seed.now().UTC().Format("2006-01-02")
	switch spawnRequestReason(scope, row, req.Spawn, today) {
	case "":
	case "SPAWN_BUDGET_EXHAUSTED":
		return ErrSpawnBudget
	default:
		return ErrSpawnQuota
	}
	now := m.seed.now()
	count := row.SpawnedToday
	if row.SpawnedDay != today {
		count = 0
	}
	changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"active_count": row.ActiveCount + 1, "spawned_day": today, "spawned_today": count + 1, "version": row.Version + 1, "updated_at": now})
	if e != nil {
		return e
	}
	if changed.RowsAffected != 1 {
		return ErrSpawnQuota
	}
	id, e := m.seed.ids.Next()
	if e != nil {
		return e
	}
	if id <= 0 {
		return ErrUnavailable
	}
	return members.WithContext(ctx).Create(&model.SpawnGrantMember{ID: guid.ID(id), TenantID: tenant, GrantID: row.ID, AgentID: child, State: "ACTIVE", PerAgentCapMicros: scope.PerAgentCapMicros, ReservedAt: now})
}

func (m *Module) releaseSpawnOn(ctx context.Context, tx *gorm.DB, tenant, child guid.ID, reason string) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || child <= 0 || !slices.Contains([]string{"RETIRED", "ADOPTED", "GRANT_ENDED"}, reason) {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	member, e := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(child)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	grants := query.BindSpawnGrants(tx, m.seed.names)
	grant, e := grants.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(grants.TenantID.Eq(tenant), grants.ID.Eq(member.GrantID)).First()
	if e != nil {
		return e
	}
	member, e = members.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(child)).First()
	if e != nil {
		return e
	}
	if member.State == "RELEASED" {
		return nil
	}
	if grant.ActiveCount < 1 {
		return ErrUnavailable
	}
	if _, e = members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ID.Eq(member.ID), members.State.Eq("ACTIVE")).Updates(map[string]any{"state": "RELEASED", "release_reason": reason, "released_at": m.seed.now()}); e != nil {
		return e
	}
	changed, e := grants.WithContext(ctx).Where(grants.TenantID.Eq(tenant), grants.ID.Eq(grant.ID), grants.Version.Eq(grant.Version)).Updates(map[string]any{"active_count": grant.ActiveCount - 1, "version": grant.Version + 1, "updated_at": m.seed.now()})
	if e != nil {
		return e
	}
	if changed.RowsAffected != 1 {
		return ErrVersion
	}
	return nil
}
