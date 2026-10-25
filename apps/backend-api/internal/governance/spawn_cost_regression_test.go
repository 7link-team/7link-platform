package governance

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnCostConnectorBindsEachActualOwnerOnce(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideSpawnCosts(s))
	require.Error(t, m.provideSpawnCosts(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[SpawnCostConnector](s)
	require.NoError(t, err)
	read := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
		return 0, false, nil
	}
	require.ErrorIs(t, c.BindExecution(nil), ErrInvalidArgument)
	require.ErrorIs(t, c.BindAssignment(nil), ErrInvalidArgument)
	require.NoError(t, c.BindExecution(read))
	require.NoError(t, c.BindAssignment(read))
	require.ErrorIs(t, c.BindExecution(read), ErrInvalidArgument)
	require.ErrorIs(t, c.BindAssignment(read), ErrInvalidArgument)
}

func TestSpawnSettlementSourceFailuresNeverCreateCostFacts(t *testing.T) {
	for _, fault := range []string{"outside transaction", "malformed", "execution error", "assignment error", "assignment missing", "human assignment", "no member"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, _ := spawnGrantHarness(t)
			event := events.Envelope{EventID: 1, EventType: "budget.settled", TenantID: 1, ProjectID: 99, Data: json.RawMessage(`{"source":"EXECUTION","source_id":"55","actual_micros":10}`)}
			m.spawnCosts.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
				return 66, true, nil
			}
			m.spawnCosts.assignment = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
				return 44, true, nil
			}
			switch fault {
			case "malformed":
				event.Data = json.RawMessage(`{`)
			case "execution error":
				m.spawnCosts.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
					return 0, false, ErrUnavailable
				}
			case "assignment error":
				m.spawnCosts.assignment = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
					return 0, false, ErrUnavailable
				}
			case "assignment missing":
				m.spawnCosts.assignment = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) {
					return 0, false, nil
				}
			case "human assignment":
				m.spawnCosts.assignment = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error) { return 0, true, nil }
			}
			var err error
			if fault == "outside transaction" {
				err = m.consumeSpawnSettlement(ctx, db, event)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { return m.consumeSpawnSettlement(ctx, tx, event) })
			}
			if fault == "human assignment" || fault == "no member" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Zero(t, seedCount(t, db, m.seed.names.SpawnCostCursors.String()))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
	m, db, ctx, _, _ := spawnGrantHarness(t)
	require.ErrorIs(t, m.consumeSpawnSettlement(ctx, nil, events.Envelope{}), ErrInvalidArgument)
	require.ErrorIs(t, m.recordSpawnCostOn(ctx, nil, 1, 44, 55, 10), ErrInvalidArgument)
	require.ErrorIs(t, m.recordSpawnCostOn(ctx, db, 1, 44, 55, 10), ErrInvalidArgument)
}

func TestSpawnCostUnavailableLedgerNeverCreatesPartialCost(t *testing.T) {
	for _, table := range []string{"members", "grants", "cursors", "id source", "invalid id"} {
		t.Run(table, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			_, err := grantSpawn(t, m, db, ctx, in)
			require.NoError(t, err)
			agentCtx := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 3, SubjectType: authn.SubjectTypePrincipal})
			req := AuthorityRequest{TenantID: 1, PrincipalID: 3, ActorKind: "AGENT", Code: "agent_spawn", Spawn: &SpawnRequestFacts{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Role: "EXECUTOR", MachineID: 5, Runtime: "CODEX", Model: "model", Effort: "medium", EstimatedCapMicros: 1000000}}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(agentCtx, tx, 1, 44, req) }))
			name := map[string]string{"members": m.seed.names.SpawnGrantMembers.String(), "grants": m.seed.names.SpawnGrants.String(), "cursors": m.seed.names.SpawnCostCursors.String()}[table]
			if name != "" {
				require.NoError(t, db.Migrator().RenameTable(name, "unavailable_ledger"))
			} else if table == "id source" {
				m.seed.ids = seedTestIDs{err: ErrUnavailable}
			} else {
				m.seed.ids = seedTestIDs{}
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.recordSpawnCostOn(ctx, tx, 1, 44, 55, 10) }))
			if name != "" {
				require.NoError(t, db.Migrator().RenameTable("unavailable_ledger", name))
			}
			require.Zero(t, seedCount(t, db, m.seed.names.SpawnCostCursors.String()))
			require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
		})
	}
}
