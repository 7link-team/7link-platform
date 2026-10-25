package governance

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SpawnCostConnector binds immutable owner facts without reversing the module
// dependency graph. A missing execution or assignment is never an Agent cost.
type SpawnCostConnector struct {
	BindExecution  func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error
	BindAssignment func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error
}

type spawnCostSources struct {
	execution, assignment func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)
}

func (m *Module) provideSpawnCosts(s *module.Services) error {
	return module.Provide[SpawnCostConnector](s, SpawnCostConnector{
		BindExecution: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error {
			if read == nil || m.spawnCosts.execution != nil {
				return ErrInvalidArgument
			}
			m.spawnCosts.execution = read
			return nil
		},
		BindAssignment: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error {
			if read == nil || m.spawnCosts.assignment != nil {
				return ErrInvalidArgument
			}
			m.spawnCosts.assignment = read
			return nil
		},
	})
}

// consumeSpawnSettlement uses the currently published actual settlement event.
// Until execution publishes live cumulative updates this accounts settled cost
// only; it never estimates unreported running cost.
func (m *Module) consumeSpawnSettlement(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if tx == nil || tx.Statement == nil || event.EventID <= 0 || event.TenantID <= 0 || event.ProjectID <= 0 || event.EventType != "budget.settled" {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	var data struct {
		Source   string  `json:"source"`
		SourceID guid.ID `json:"source_id"`
		Actual   *int64  `json:"actual_micros"`
	}
	if json.Unmarshal(event.Data, &data) != nil {
		return ErrInvalidArgument
	}
	if data.Source != "EXECUTION" {
		return nil
	}
	if data.SourceID <= 0 || data.Actual == nil || *data.Actual < 0 {
		return ErrInvalidArgument
	}
	if m.spawnCosts.execution == nil || m.spawnCosts.assignment == nil {
		return ErrUnavailable
	}
	assignment, found, err := m.spawnCosts.execution(ctx, tx, event.TenantID, event.ProjectID, data.SourceID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	agent, found, err := m.spawnCosts.assignment(ctx, tx, event.TenantID, event.ProjectID, assignment)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if agent == 0 {
		return nil
	}
	if err = m.recordSpawnCostOn(ctx, tx, event.TenantID, agent, data.SourceID, *data.Actual); err != nil {
		return err
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.TenantID.Eq(event.TenantID), members.AgentID.Eq(agent), members.State.Eq("ACTIVE")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	grants := query.BindSpawnGrants(tx, m.seed.names)
	grant, err := grants.WithContext(ctx).Where(grants.TenantID.Eq(event.TenantID), grants.ID.Eq(member.GrantID)).First()
	if err != nil {
		return err
	}
	if grant.State != "EXHAUSTED" {
		return nil
	}
	_, err = m.recordEscalationOn(ctx, tx, EscalationFact{TenantID: event.TenantID, ProjectID: event.ProjectID, SubjectID: event.ProjectID, TriggerEventID: event.EventID, Kind: "SPAWN_BUDGET", SubjectKind: "PROJECT", Reason: "Spawn grant budget exhausted", Facts: map[string]any{"spawn_grant_id": grant.ID.String(), "agent_id": agent.String(), "execution_id": data.SourceID.String(), "spent_micros": fmtInt64(grant.SpentMicros)}, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "SYSTEM:spawn_settlement"}})
	return err
}

func (m *Module) recordSpawnCostOn(ctx context.Context, tx *gorm.DB, tenant, agent, execution guid.ID, cumulative int64) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || agent <= 0 || execution <= 0 || cumulative < 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	grants := query.BindSpawnGrants(tx, m.seed.names)
	grant, err := grants.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(grants.TenantID.Eq(tenant), grants.ID.Eq(member.GrantID)).First()
	if err != nil {
		return err
	}
	member, err = members.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(agent)).First()
	if err != nil {
		return err
	}
	if member.State != "ACTIVE" {
		return nil
	}
	cursors := query.BindSpawnCostCursors(tx, m.seed.names)
	cursor, err := cursors.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(cursors.TenantID.Eq(tenant), cursors.ExecutionID.Eq(execution)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		id, err := m.seed.ids.Next()
		if err != nil {
			return err
		}
		if id <= 0 {
			return ErrUnavailable
		}
		cursor = &model.SpawnCostCursor{ID: guid.ID(id), TenantID: tenant, AgentID: agent, ExecutionID: execution, UpdatedAt: m.seed.now()}
		if err = cursors.WithContext(ctx).Create(cursor); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if cursor.AgentID != agent {
		return ErrInvalidArgument
	}
	delta := cumulative - cursor.LastCumulativeMicros
	if delta <= 0 {
		return nil
	}
	if grant.SpentMicros > math.MaxInt64-delta || member.SpentMicros > math.MaxInt64-delta {
		return ErrInvalidArgument
	}
	var scope SpawnGrantScope
	if json.Unmarshal([]byte(grant.ScopeJSON), &scope) != nil {
		return ErrUnavailable
	}
	spent, memberSpent := grant.SpentMicros+delta, member.SpentMicros+delta
	// ceil(80% of cap), without multiplication overflow. The locked monotonic
	// cumulative cursor makes the crossing atomic and replay safe.
	threshold := scope.BudgetCapMicros - scope.BudgetCapMicros/5
	warn := grant.SpentMicros < threshold && spent >= threshold
	capReached := !member.CapNotified && memberSpent >= member.PerAgentCapMicros
	if _, err = cursors.WithContext(ctx).Where(cursors.ID.Eq(cursor.ID), cursors.TenantID.Eq(tenant)).Updates(map[string]any{"last_cumulative_micros": cumulative, "updated_at": m.seed.now()}); err != nil {
		return err
	}
	if _, err = members.WithContext(ctx).Where(members.ID.Eq(member.ID), members.TenantID.Eq(tenant)).Updates(map[string]any{"spent_micros": memberSpent, "cap_notified": member.CapNotified || capReached}); err != nil {
		return err
	}
	kind := "spawn_grant.updated"
	if grant.State == "ACTIVE" && spent >= scope.BudgetCapMicros {
		grant.State = "EXHAUSTED"
		kind = "spawn_grant.exhausted"
	}
	changed, err := grants.WithContext(ctx).Where(grants.TenantID.Eq(tenant), grants.ID.Eq(grant.ID), grants.Version.Eq(grant.Version)).Updates(map[string]any{"spent_micros": spent, "state": grant.State, "version": grant.Version + 1, "updated_at": m.seed.now()})
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrVersion
	}
	grant.SpentMicros, grant.Version = spent, grant.Version+1
	if warn {
		if err = m.spawnEventOn(ctx, tx, grant, "spawn_grant.budget_threshold", "Actual settled cost reached 80% of the spawn budget", 0); err != nil {
			return err
		}
	}
	reason := "Actual execution settlement recorded"
	if capReached {
		reason = "Member cost cap reached: " + agent.String()
		return m.spawnEventOn(ctx, tx, grant, kind, reason, 0, agent)
	}
	return m.spawnEventOn(ctx, tx, grant, kind, reason, 0)
}
