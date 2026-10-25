package governance

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnCostActualSettlementMonotonicAtomicExhaustion(t *testing.T) {
	m, db, ctx, pub, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
	req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(agentCtx, tx, 1, 44, req) }))
	m.spawnCosts.execution = func(_ context.Context, _ *gorm.DB, tenant, project, execution guid.ID) (guid.ID, bool, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.EqualValues(t, 55, execution)
		return 66, true, nil
	}
	m.spawnCosts.assignment = func(_ context.Context, _ *gorm.DB, tenant, project, assignment guid.ID) (guid.ID, bool, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 99, project)
		require.EqualValues(t, 66, assignment)
		return 44, true, nil
	}
	consume := func(amount int64) error {
		data, err := json.Marshal(map[string]any{"source": "EXECUTION", "source_id": "55", "actual_micros": amount})
		require.NoError(t, err)
		return db.Transaction(func(tx *gorm.DB) error {
			return m.consumeSpawnSettlement(t.Context(), tx, events.Envelope{EventID: 77, EventType: "budget.settled", TenantID: 1, ProjectID: 99, Data: data})
		})
	}
	require.NoError(t, consume(1000000))
	require.NoError(t, consume(1000000))
	require.NoError(t, consume(999999))
	var capDetail string
	require.NoError(t, db.Table("event_probe").Order("rowid DESC").Limit(1).Pluck("detail", &capDetail).Error)
	require.Contains(t, capDetail, `"member_cap_reached":"44"`)
	q := query.BindSpawnGrants(db, m.seed.names)
	read := func() int64 {
		row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
		require.NoError(t, err)
		return row.SpentMicros
	}
	require.EqualValues(t, 1000000, read())
	members := query.BindSpawnGrantMembers(db, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.AgentID.Eq(guid.ID(44))).First()
	require.NoError(t, err)
	require.True(t, member.CapNotified)
	pub.Fail = true
	require.Error(t, consume(2000000))
	require.EqualValues(t, 1000000, read())
	cursors := query.BindSpawnCostCursors(db, m.seed.names)
	cursor, err := cursors.WithContext(ctx).Where(cursors.ExecutionID.Eq(guid.ID(55))).First()
	require.NoError(t, err)
	require.EqualValues(t, 1000000, cursor.LastCumulativeMicros)
	pub.Fail = false
	require.NoError(t, consume(2000000))
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXHAUSTED", row.State)
	require.EqualValues(t, 2000000, row.SpentMicros)
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.EscalationIncidents.String()))
	require.NoError(t, consume(2000000))
	require.EqualValues(t, 1, seedCount(t, db, m.seed.names.EscalationIncidents.String()), "replay cannot duplicate the exhausted-budget incident")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.releaseSpawnOn(ctx, tx, 1, 44, "ADOPTED") }))
	require.NoError(t, consume(3000000))
	require.EqualValues(t, 2000000, read(), "released members no longer charge the grant")
}

func TestSpawnCostRejectsMissingSourcesAndForeignExecutionOwner(t *testing.T) {
	m, db, _, _, _ := spawnGrantHarness(t)
	event := events.Envelope{EventID: 1, EventType: "budget.settled", TenantID: 1, ProjectID: 99, Data: json.RawMessage(`{"source":"EXECUTION","source_id":"55","actual_micros":0}`)}
	consume := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.consumeSpawnSettlement(t.Context(), tx, event) })
	}
	require.ErrorIs(t, consume(), ErrUnavailable)
	m.spawnCosts.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
		return 0, false, nil
	}
	m.spawnCosts.assignment = m.spawnCosts.execution
	require.ErrorIs(t, consume(), ErrNotFound)
	event.Data = json.RawMessage(`{"source":"EXECUTION","source_id":"55","actual_micros":-1}`)
	require.ErrorIs(t, consume(), ErrInvalidArgument)
	event.Data = json.RawMessage(`{"source":"OTHER"}`)
	require.NoError(t, consume())
}

func TestSpawnCostEightyPercentCrossingRollbackReplayAndBoundary(t *testing.T) {
	m, db, ctx, pub, in := spawnGrantHarness(t)
	_, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
	req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(agentCtx, tx, 1, 44, req) }))
	record := func(cumulative int64) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.recordSpawnCostOn(ctx, tx, 1, 44, 55, cumulative) })
	}
	warnings := func() int64 {
		var n int64
		require.NoError(t, db.Table("event_probe").Where("detail LIKE ?", "%Actual settled cost reached 80%%").Count(&n).Error)
		return n
	}
	require.NoError(t, record(1599999))
	require.Zero(t, warnings())
	pub.Fail = true
	require.Error(t, record(1600000))
	require.Zero(t, warnings())
	pub.Fail = false
	require.NoError(t, record(1600000))
	require.EqualValues(t, 1, warnings())
	for _, cost := range []int64{1600000, 1599999, 1700000, 2000000} {
		require.NoError(t, record(cost))
		require.EqualValues(t, 1, warnings())
	}
}
