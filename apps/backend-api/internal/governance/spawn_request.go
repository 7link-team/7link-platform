package governance

import (
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
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentSpawnRequest struct {
	Facts        SpawnRequestFacts       `json:"facts"`
	Registration fleet.AgentRegistration `json:"registration"`
	Reason       string                  `json:"reason" minLength:"10" maxLength:"2000"`
}

// Grant identity is resolved by the checker and persisted privately. The caller
// cannot retarget a pending request by replacing the supervisor's grant.
type agentSpawnPayload struct {
	AgentSpawnRequest
	GrantID guid.ID `json:"grant_id"`
}

// A pending proposal never grants an Agent identity, quota or IAM privileges.
// Human adoption consumes the existing source registration; the registered
// Agent is correctly HUMAN_CREATED, not a fabricated autonomous child.
func (m *Module) createAgentSpawnRequestOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in AgentSpawnRequest) (model.SpawnRequest, error) {
	var empty model.SpawnRequest
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil {
		return empty, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrInvalidArgument
	}
	if tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || actor <= 0 {
		return empty, ErrForbidden
	}
	if !utf8.ValidString(in.Reason) || len([]rune(strings.TrimSpace(in.Reason))) < 10 || utf8.RuneCountInString(in.Reason) > 2000 || len(in.Registration.Roles) != 1 || in.Registration.Roles[0] != in.Facts.Role || in.Registration.Runtime != in.Facts.Runtime || in.Registration.Model != in.Facts.Model || in.Registration.MachineID != in.Facts.MachineID || in.Registration.PrincipalID <= 0 || in.Registration.PrincipalID == actor {
		return empty, ErrInvalidArgument
	}
	if m.seed == nil || m.seed.ids == nil || m.spawnFence == nil {
		return empty, ErrUnavailable
	}
	a, err := m.Check(ctx, tx, AuthorityRequest{TenantID: tenant, PrincipalID: actor, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &in.Facts})
	if err != nil {
		return empty, err
	}
	if !a.Allowed {
		return empty, ErrForbidden
	}
	id, err := m.seed.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	g := query.BindSpawnGrants(tx, m.seed.names)
	grant, err := g.WithContext(ctx).Where(g.TenantID.Eq(tenant), g.GranteeAgentID.Eq(in.Facts.SupervisorAgentID), g.State.Eq("ACTIVE"), g.StartsAt.Lte(m.seed.now()), g.EndsAt.Gt(m.seed.now())).First()
	if err != nil {
		return empty, err
	}
	payload, err := json.Marshal(agentSpawnPayload{AgentSpawnRequest: in, GrantID: grant.ID})
	if err != nil {
		return empty, err
	}
	row := model.SpawnRequest{ID: guid.ID(id), TenantID: tenant, RequesterID: actor, SupervisorID: in.Facts.SupervisorAgentID, State: "REQUESTED", Payload: string(payload), Reason: in.Reason, Version: 1, CreatedAt: m.seed.now(), ExpiresAt: m.seed.now().Add(24 * time.Hour)}
	if err = query.BindSpawnRequests(tx, m.seed.names).WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	if err = m.spawnRequestEvent(ctx, tx, &row, "REQUESTED", "AGENT", a.AuthoritySource); err != nil {
		return empty, err
	}
	return row, nil
}

// Pending proposals share the same principal fence as spawn grant consumption.
// Count source-bound unresolved proposals without claiming a child reservation.
func (m *Module) pendingAgentSpawnRequestsOn(ctx context.Context, tx *gorm.DB, tenant, supervisor, grant guid.ID) (int, error) {
	q := query.BindSpawnRequests(tx, m.seed.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.SupervisorID.Eq(supervisor), q.State.In("REQUESTED", "UNAVAILABLE"), q.ExpiresAt.Gt(m.seed.now())).Find()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		var payload agentSpawnPayload
		if json.Unmarshal([]byte(row.Payload), &payload) != nil || payload.GrantID <= 0 {
			return 0, ErrUnavailable
		}
		if payload.GrantID == grant {
			count++
		}
	}
	return count, nil
}
func (m *Module) spawnRequestHumanOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, lock bool) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if authn.TenantIDFromContext(ctx) != tenant || actor <= 0 {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.principals.ActiveHumanByID == nil || m.delegations.members == nil || m.explain == nil {
		return ErrUnavailable
	}
	var p identity.Principal
	var active bool
	var err error
	if lock {
		p, active, err = m.principals.LockActiveRow(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !active || p.ID != actor {
			return ErrForbidden
		}
	}
	p, active, err = m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || p.ID != actor {
		return ErrForbidden
	}
	active, err = m.delegations.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrForbidden
	}
	x, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: actor, Permission: "agent_manage"})
	if err != nil {
		return err
	}
	if !x.Allowed {
		return ErrForbidden
	}
	return nil
}
func (m *Module) changeAgentSpawnRequestOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, action, reason string) (model.SpawnRequest, error) {
	var empty model.SpawnRequest
	if tx == nil || tx.Statement == nil {
		return empty, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return empty, ErrInvalidArgument
	}
	if !slices.Contains([]string{"CREATE", "ADOPT", "REJECT", "ORPHAN"}, action) || len([]rune(strings.TrimSpace(reason))) < 10 || utf8.RuneCountInString(reason) > 2000 {
		return empty, ErrInvalidArgument
	}
	if err := m.spawnRequestHumanOn(ctx, tx, tenant, false); err != nil {
		return empty, err
	}
	q := query.BindSpawnRequests(tx, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if row.Version != version || (row.State != "REQUESTED" && row.State != "UNAVAILABLE") {
		return empty, ErrVersion
	}
	var in AgentSpawnRequest
	if json.Unmarshal([]byte(row.Payload), &in) != nil {
		return empty, ErrUnavailable
	}
	// Lock every participating principal in stable order before the request or
	// canonical registration can lock any business row.
	if m.principals.Lock == nil {
		return empty, ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), row.RequesterID}
	if action == "ADOPT" {
		if m.spawnDirectory.MachineOn == nil {
			return empty, ErrUnavailable
		}
		machine, err := m.spawnDirectory.MachineOn(ctx, tx, tenant, in.Registration.MachineID)
		if err != nil {
			return empty, err
		}
		ids = append(ids, in.Registration.PrincipalID, machine.PrincipalID)
	}
	slices.Sort(ids)
	for _, principal := range slices.Compact(ids) {
		if principal <= 0 {
			return empty, ErrUnavailable
		}
		if err = m.principals.Lock(ctx, tx, principal); err != nil {
			return empty, err
		}
	}
	if err = m.spawnRequestHumanOn(ctx, tx, tenant, true); err != nil {
		return empty, err
	}
	row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if err != nil {
		return empty, err
	}
	if row.Version != version || (row.State != "REQUESTED" && row.State != "UNAVAILABLE") {
		return empty, ErrVersion
	}
	if !m.seed.now().Before(row.ExpiresAt) {
		row.State = "EXPIRED"
	} else {
		if m.spawnDirectory.AgentOn == nil {
			return empty, ErrUnavailable
		}
		supervisor, exists, err := m.spawnDirectory.AgentOn(ctx, tx, tenant, row.SupervisorID)
		if err != nil {
			return empty, err
		}
		orphan := !exists || supervisor.TenantID != tenant || supervisor.PrincipalID != row.RequesterID || supervisor.State != "ACTIVE"
		switch {
		case orphan:
			row.State = "ORPHANED"
		case action == "ORPHAN":
			return empty, ErrInvalidArgument // Current active supervisor cannot be labeled orphaned.
		case action == "REJECT":
			row.State = "REJECTED"
		case action == "CREATE":
			row.State = "UNAVAILABLE" // Canonical registration is Human-only; no autonomous origin command exists.
		case action == "ADOPT":
			if m.spawnRegistration.RegisterOn == nil {
				return empty, ErrUnavailable
			}
			child, err := m.spawnRegistration.RegisterOn(ctx, tx, tenant, in.Registration)
			if err != nil {
				return empty, err
			}
			if child.ID <= 0 || child.PrincipalID != in.Registration.PrincipalID || child.MachineID != in.Registration.MachineID {
				return empty, ErrUnavailable
			}
			row.AgentID = &child.ID
			row.State = "ADOPTED"
		}
	}
	row.Reason = reason
	row.Version++
	info, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(version)).Updates(map[string]any{"state": row.State, "reason": row.Reason, "agent_id": row.AgentID, "version": row.Version})
	if err != nil {
		return empty, err
	}
	if info.RowsAffected != 1 {
		return empty, ErrVersion
	}
	if err = m.spawnRequestEvent(ctx, tx, row, action, "HUMAN", "IAM"); err != nil {
		return empty, err
	}
	return *row, nil
}

// Pending requests are current source rows, never an event-derived approval.
// Expired requests and requests without a current supervisor are not actionable.
func (m *Module) agentSpawnRequestInbox(ctx context.Context, req InboxRequest) ([]InboxItem, error) {
	out := []InboxItem{}
	if req.Kind != "AGENT_SPAWN_APPROVAL" || req.Limit < 1 || req.Limit > 101 || req.ProjectID < 0 {
		return nil, ErrInvalidArgument
	}
	if req.TenantID != authn.TenantIDFromContext(ctx) || req.PrincipalID != authn.PrincipalIDFromContext(ctx) {
		return nil, ErrForbidden
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.spawnRequestHumanOn(ctx, tx, req.TenantID, true); errors.Is(err, ErrForbidden) {
			return nil
		} else if err != nil {
			return err
		}
		if m.spawnDirectory.AgentOn == nil {
			return ErrUnavailable
		}
		q := query.BindSpawnRequests(tx, m.seed.names)
		stmt := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.State.In("REQUESTED", "UNAVAILABLE"), q.ExpiresAt.Gt(m.seed.now()))
		for offset := 0; ; offset += 100 {
			rows, err := stmt.Order(q.ExpiresAt, q.CreatedAt, q.ID).Limit(100).Offset(offset).Find()
			if err != nil {
				return err
			}
			for _, row := range rows {
				var payload agentSpawnPayload
				if json.Unmarshal([]byte(row.Payload), &payload) != nil || payload.GrantID <= 0 {
					return ErrUnavailable
				}
				g := query.BindSpawnGrants(tx, m.seed.names)
				grant, err := g.WithContext(ctx).Where(g.TenantID.Eq(req.TenantID), g.ID.Eq(payload.GrantID), g.GranteeAgentID.Eq(row.SupervisorID), g.GranteePrincipalID.Eq(row.RequesterID), g.State.Eq("ACTIVE"), g.StartsAt.Lte(m.seed.now()), g.EndsAt.Gt(m.seed.now())).First()
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				var scope SpawnGrantScope
				if json.Unmarshal([]byte(grant.ScopeJSON), &scope) != nil || !validSpawnGrantScope(scope) {
					return ErrUnavailable
				}
				if grant.SpentMicros >= scope.BudgetCapMicros {
					continue
				}
				readable := len(scope.ProjectIDs) == 0 && req.ProjectID == 0
				for _, project := range payload.Facts.ProjectIDs {
					if (len(scope.ProjectIDs) > 0 && !slices.Contains(scope.ProjectIDs, project)) || (req.ProjectID > 0 && req.ProjectID != project) {
						continue
					}
					authority, err := m.explain(ctx, tx, core.AuthorizationExplanationRequest{TenantID: req.TenantID, EntityID: project, PrincipalID: req.PrincipalID, Permission: "project_read"})
					if err != nil {
						return err
					}
					if authority.Allowed {
						readable = true
						break
					}
				}
				if !readable {
					continue
				}
				supervisor, found, err := m.spawnDirectory.AgentOn(ctx, tx, req.TenantID, row.SupervisorID)
				if err != nil {
					return err
				}
				if !found || supervisor.TenantID != req.TenantID || supervisor.PrincipalID != row.RequesterID || supervisor.State != "ACTIVE" {
					continue
				}
				due := row.ExpiresAt
				item := InboxItem{InboxKey: InboxKey{Priority: "NORMAL", DueAt: &due, CreatedAt: row.CreatedAt, Kind: req.Kind, ID: row.ID}, Title: row.Reason, ObjectType: "spawn_request", ObjectID: row.ID, TenantID: req.TenantID, RecipientID: req.PrincipalID, Visibility: "HUMAN"}
				if req.After != nil && CompareInboxKeys(item.InboxKey, *req.After) <= 0 {
					continue
				}
				out = append(out, item)
				if len(out) == req.Limit {
					return nil
				}
			}
			if len(rows) < 100 {
				return nil
			}
		}
	})
	return out, err
}
func (m *Module) spawnRequestEvent(ctx context.Context, tx *gorm.DB, row *model.SpawnRequest, action, kind, authority string) error {
	if m.seed.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	a := audit.NewDomainEvent(ctx, row.TenantID, "governance.spawn_request_changed", "spawn_request", row.ID)
	a.Detail["authority_source"] = authority
	a.Detail["action"] = action
	a.Detail["state"] = row.State
	if err := m.seed.audit(ctx, tx, a); err != nil {
		return err
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: row.TenantID, Type: "governance.spawn_request_changed", Actor: events.Actor{Kind: kind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "spawn_request", ID: row.ID}, SubjectVersion: int64(row.Version), Data: map[string]any{"state": row.State, "action": action, "supervisor_id": row.SupervisorID, "requester_id": row.RequesterID, "agent_id": row.AgentID}})
	return err
}

// Reconcile only unresolved requests. Human-adopted identities have independent
// source ownership and must not be orphaned when the original supervisor leaves.
func (m *Module) reconcileAgentSpawnRequests(ctx context.Context, job jobs.Job) error {
	if job.TenantID != 0 {
		return jobs.Permanent(ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	if m.principals.Lock == nil || m.spawnDirectory.AgentOn == nil {
		return ErrUnavailable
	}
	var after guid.ID
	for {
		q := query.BindSpawnRequests(db, m.seed.names)
		rows, err := q.WithContext(ctx).Where(q.ID.Gt(after), q.State.In("REQUESTED", "UNAVAILABLE")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, candidate := range rows {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := m.principals.Lock(ctx, tx, candidate.RequesterID); err != nil {
					return err
				}
				q := query.BindSpawnRequests(tx, m.seed.names)
				row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(candidate.TenantID), q.ID.Eq(candidate.ID)).First()
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if row.State != "REQUESTED" && row.State != "UNAVAILABLE" {
					return nil
				}
				state, reason := "", ""
				if !m.seed.now().Before(row.ExpiresAt) {
					state, reason = "EXPIRED", "Agent request review window ended"
				} else {
					supervisor, found, err := m.spawnDirectory.AgentOn(ctx, tx, row.TenantID, row.SupervisorID)
					if err != nil {
						return err
					}
					if !found || supervisor.TenantID != row.TenantID || supervisor.PrincipalID != row.RequesterID || supervisor.State != "ACTIVE" {
						state, reason = "ORPHANED", "Agent request supervisor is no longer active"
					}
				}
				if state == "" {
					return nil
				}
				info, err := q.WithContext(ctx).Where(q.TenantID.Eq(row.TenantID), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": state, "reason": reason, "version": row.Version + 1})
				if err != nil {
					return err
				}
				if info.RowsAffected != 1 {
					return ErrVersion
				}
				row.State, row.Reason, row.Version = state, reason, row.Version+1
				return m.spawnRequestEvent(ctx, tx, row, "RECONCILED", "SYSTEM", "governance.spawn_request_reconcile")
			})
			if err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}
