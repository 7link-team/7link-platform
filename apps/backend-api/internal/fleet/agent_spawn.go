package fleet

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentSpawnInput uses an existing real IAM account; this command never grants
// roles or credentials merely because an Agent proposed a child identity.
type AgentSpawnInput struct {
	SupervisorAgentID  guid.ID           `json:"supervisor_agent_id"`
	ProjectIDs         []guid.ID         `json:"project_ids" maxItems:"200"`
	Effort             string            `json:"effort" maxLength:"16"`
	EstimatedCapMicros int64             `json:"estimated_cap_micros" minimum:"1"`
	Reason             string            `json:"reason" minLength:"10" maxLength:"2000"`
	Registration       AgentRegistration `json:"registration"`
}

type AgentSpawnGrant struct {
	GrantID, OwnerPrincipalID guid.ID
	ExpiresAt                 time.Time
}

// AgentSpawnPolicy is bound by the grant owner; every callback participates in
// Fleet's transaction. Replay checks skip allocation, never current authority.
type AgentSpawnPolicy struct {
	FenceOn     func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, AgentSpawnInput, bool) (AgentSpawnGrant, error)
	ReserveOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, AgentSpawnInput) error
	RetireOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, bool) error
}
type AgentSpawnConnector struct{ Bind func(AgentSpawnPolicy) error }
type agentSpawnRegistration struct {
	Input AgentSpawnInput
	Grant AgentSpawnGrant
}

func (m *Module) provideAgentSpawn(s *module.Services) error {
	return module.Provide[AgentSpawnConnector](s, AgentSpawnConnector{Bind: func(p AgentSpawnPolicy) error {
		if p.FenceOn == nil || p.AuthorizeOn == nil || p.ReserveOn == nil || p.RetireOn == nil || m.spawnPolicy.AuthorizeOn != nil {
			return ErrUnavailable
		}
		m.spawnPolicy = p
		return nil
	}})
}

func validAgentSpawn(in AgentSpawnInput) bool {
	return in.SupervisorAgentID > 0 && in.Registration.PrincipalID > 0 && in.Registration.MachineID > 0 && len(in.Registration.Roles) == 1 && slices.Contains([]string{"EXECUTOR", "COORDINATOR"}, in.Registration.Roles[0]) && slices.Contains([]string{"", "READ_ONLY", "ON_REQUEST"}, in.Registration.AuthorizationMode) && in.EstimatedCapMicros > 0 && in.Effort != "" && len(in.Effort) <= 16 && utf8.ValidString(in.Reason) && utf8.RuneCountInString(strings.TrimSpace(in.Reason)) >= 10 && utf8.RuneCountInString(in.Reason) <= 2000
}

func (m *Module) agentSpawnAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentSpawnInput, replay bool) (AgentSpawnGrant, error) {
	var out AgentSpawnGrant
	actor := authn.PrincipalIDFromContext(ctx)
	if !validAgentSpawn(in) || fleetTransaction(tx, tenant, actor) != nil || authn.TenantIDFromContext(ctx) != tenant || actor == in.Registration.PrincipalID {
		return out, ErrInvalidArgument
	}
	if m.principals.Lock == nil || m.accounts == nil || m.spawnPolicy.FenceOn == nil || m.spawnPolicy.AuthorizeOn == nil || m.spawnPolicy.ReserveOn == nil {
		return out, ErrUnavailable
	}
	machine, err := m.MachineByIDOn(ctx, tx, tenant, in.Registration.MachineID)
	if err != nil {
		return out, err
	}
	principals := []guid.ID{actor, in.Registration.PrincipalID, machine.PrincipalID}
	slices.Sort(principals)
	for _, id := range slices.Compact(principals) {
		if id <= 0 {
			return out, ErrMachineForbidden
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return out, err
		}
	}
	active, err := m.accounts(ctx, tx, tenant, actor, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if !active {
		return out, ErrMachineForbidden
	}
	if err = m.spawnPolicy.FenceOn(ctx, tx, tenant, in.SupervisorAgentID); err != nil {
		return out, err
	}
	if _, err = m.LockMachineOn(ctx, tx, tenant, in.Registration.MachineID); err != nil {
		return out, err
	}
	q := query.BindAgents(tx, m.names)
	supervisor, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(in.SupervisorAgentID), q.PrincipalID.Eq(actor)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrMachineForbidden
	}
	if err != nil {
		return out, err
	}
	if supervisor.State != "ACTIVE" || supervisor.ProcessStatus != "RUNNING" || supervisor.OrphanedAt != nil || (supervisor.Origin == "AGENT_SPAWNED" && supervisor.AdoptedAt == nil) {
		return out, ErrMachineForbidden
	}
	return m.spawnPolicy.AuthorizeOn(ctx, tx, tenant, in, replay)
}

func (m *Module) SpawnAgentOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentSpawnInput) (RegisteredAgent, error) {
	if !validAgentSpawn(in) {
		return RegisteredAgent{}, ErrInvalidArgument
	}
	in.Reason = strings.TrimSpace(in.Reason)
	registration := in.Registration
	registration.AuthorizationMode = "ON_REQUEST"
	return m.registerAgentOn(ctx, tx, tenant, registration, &agentSpawnRegistration{Input: in}, m.agentRoles)
}

// RetireSpawnedAgentOn handles an idle child only. Active processes and live
// executions need the full retirement/handover flow and cannot be called done.
func (m *Module) RetireSpawnedAgentOn(ctx context.Context, tx *gorm.DB, tenant, child guid.ID, version int, reason string) (RegisteredAgent, error) {
	var out RegisteredAgent
	if version < 1 || !utf8.ValidString(reason) || utf8.RuneCountInString(strings.TrimSpace(reason)) < 10 || utf8.RuneCountInString(reason) > 2000 {
		return out, ErrInvalidArgument
	}
	row, err := m.agentSpawnRetirementAuthorityOn(ctx, tx, tenant, child)
	if err != nil {
		return out, err
	}
	if row.Version != version {
		return out, ErrVersionConflict
	}
	if row.State == "RETIRED" || row.State == "RETIRING" || slices.Contains([]string{"RUNNING", "STARTING", "STOPPING", "UNREACHABLE"}, row.ProcessStatus) {
		return out, ErrAgentRebindState
	}
	if m.agentRebindQuiet == nil || m.revokeAccount == nil || m.audit == nil || m.events == nil || m.ids == nil {
		return out, ErrUnavailable
	}
	quiet, err := m.agentRebindQuiet(ctx, tx, tenant, row.PrincipalID)
	if err != nil {
		return out, err
	}
	if !quiet {
		return out, ErrAgentRunningExecutions
	}
	if err = m.revokeAccount(ctx, tx, tenant, row.PrincipalID); err != nil {
		return out, err
	}
	if err = m.spawnPolicy.RetireOn(ctx, tx, tenant, *row.ParentAgentID, row.ID, *row.SpawnGrantID, true); err != nil {
		return out, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row.MachineID != nil {
		id, err := m.ids.Next()
		if err != nil {
			return out, err
		}
		if id <= 0 {
			return out, ErrUnavailable
		}
		if err = query.BindAgentMachineCommands(tx, m.names).WithContext(ctx).Create(&model.AgentMachineCommand{ID: guid.ID(id), TenantID: tenant, AgentPrincipalID: row.PrincipalID, MachineID: *row.MachineID, ConfigRevision: row.ConfigRevision, Kind: "CLEANUP", State: "QUEUED", CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}); err != nil {
			return out, err
		}
	}
	q := query.BindAgents(tx, m.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(child), q.Version.Eq(version)).Updates(map[string]any{"state": "RETIRED", "retired_at": now, "pending_cleanup_machine_id": row.MachineID, "machine_id": nil, "version": version + 1, "updated_at": now, "updated_by": authn.PrincipalIDFromContext(ctx)})
	if err != nil {
		return out, err
	}
	if changed.RowsAffected != 1 {
		return out, ErrVersionConflict
	}
	roles := query.BindAgentRoles(tx, m.names)
	if _, err = roles.WithContext(ctx).Where(roles.TenantID.Eq(tenant), roles.AgentID.Eq(child), roles.RevokedAt.IsNull()).Updates(map[string]any{"revoked_at": now, "updated_at": now}); err != nil {
		return out, err
	}
	e := audit.NewDomainEvent(ctx, tenant, "fleet.agent_retired", "agent", child)
	e.Detail["reason"], e.Detail["authority_source"], e.Detail["actor_kind"] = strings.TrimSpace(reason), "POLICY:spawn_grant", "AGENT"
	if err = m.audit(ctx, tx, e); err != nil {
		return out, err
	}
	eventID, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "agent.retired", Subject: events.Subject{Kind: "agent", ID: row.PrincipalID}, SubjectVersion: int64(version + 1), Actor: events.Actor{Kind: "AGENT", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "POLICY:spawn_grant"}, Data: map[string]any{"agent_id": child, "principal_id": row.PrincipalID, "state": "RETIRED", "reason": "SUPERVISOR_RETIRED_CHILD", "pending_cleanup_machine_id": row.MachineID}})
	if err == nil {
		err = m.notifyRetirementOn(ctx, tx, tenant, row.PrincipalID, eventID)
	}
	return RegisteredAgent{ID: child, PrincipalID: row.PrincipalID, State: "RETIRED", ProcessStatus: row.ProcessStatus, ModelFamily: row.ModelFamily, Version: version + 1}, err
}

func (m *Module) agentSpawnRetirementAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, child guid.ID) (*model.Agent, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if fleetTransaction(tx, tenant, child) != nil || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrInvalidArgument
	}
	if m.principals.Lock == nil || m.accounts == nil || m.spawnPolicy.FenceOn == nil || m.spawnPolicy.RetireOn == nil {
		return nil, ErrUnavailable
	}
	q := query.BindAgents(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(child)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineNotFound
	}
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{actor, row.PrincipalID}
	if row.MachineID != nil {
		machine, err := m.MachineByIDOn(ctx, tx, tenant, *row.MachineID)
		if err != nil {
			return nil, err
		}
		ids = append(ids, machine.PrincipalID)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if id <= 0 {
			return nil, ErrMachineForbidden
		}
		if err = m.principals.Lock(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	active, err := m.accounts(ctx, tx, tenant, actor, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrMachineForbidden
	}
	if row.Origin != "AGENT_SPAWNED" || row.AdoptedAt != nil || row.ParentAgentID == nil || row.SpawnGrantID == nil {
		return nil, ErrMachineForbidden
	}
	parent, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(*row.ParentAgentID), q.PrincipalID.Eq(actor)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMachineForbidden
	}
	if err != nil {
		return nil, err
	}
	if parent.State != "ACTIVE" || parent.ProcessStatus != "RUNNING" || parent.OrphanedAt != nil {
		return nil, ErrMachineForbidden
	}
	if err = m.spawnPolicy.FenceOn(ctx, tx, tenant, *row.ParentAgentID); err != nil {
		return nil, err
	}
	if row.MachineID != nil {
		if _, err = m.LockMachineOn(ctx, tx, tenant, *row.MachineID); err != nil {
			return nil, err
		}
	}
	if err = m.spawnPolicy.RetireOn(ctx, tx, tenant, *row.ParentAgentID, child, *row.SpawnGrantID, false); err != nil {
		return nil, err
	}
	row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(child)).First()
	if err != nil {
		return nil, err
	}
	if row.Origin != "AGENT_SPAWNED" || row.AdoptedAt != nil || row.ParentAgentID == nil || *row.ParentAgentID != parent.ID || row.SpawnGrantID == nil {
		return nil, ErrVersionConflict
	}
	return row, nil
}
