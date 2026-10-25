package execution

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTeamLoopExecutionsUseFrozenAuthorAndAcceptedStart(t *testing.T) {
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	v := f.create(t)
	start, end := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	read := func(agent guid.ID) ([]LoopExecutionFact, error) {
		var rows []LoopExecutionFact
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = f.m.loopAgentExecutionFactsOn(t.Context(), tx, 7, agent, start, end)
			return err
		})
		return rows, err
	}
	rows, err := read(21)
	require.NoError(t, err)
	require.Empty(t, rows)
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{Kind: "ACCEPTED", DispatchEpoch: 1, Seq: 1})
	require.NoError(t, err)
	rows, err = read(21)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(9), rows[0].ProjectID)
	require.Equal(t, guid.ID(6), rows[0].AssignmentID)
	require.NotNil(t, rows[0].StartedAt)
	rows, err = read(11)
	require.NoError(t, err)
	require.Empty(t, rows, "initiator is not the immutable executor")
	q := query.BindExecutions(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, err)
	clone := *row
	clone.ID += 1000
	clone.ProjectID = 10
	clone.AssignmentID = 61
	require.NoError(t, q.WithContext(t.Context()).Create(&clone))
	receipts := query.BindReceipts(f.db, f.m.names)
	receipt, err := receipts.WithContext(t.Context()).Where(receipts.ExecutionID.Eq(v.ID), receipts.Kind.Eq("ACCEPTED")).First()
	require.NoError(t, err)
	copied := *receipt
	copied.ID += 1000
	copied.ExecutionID = clone.ID
	require.NoError(t, receipts.WithContext(t.Context()).Create(&copied))
	rows, err = read(21)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, guid.ID(10), rows[1].ProjectID)
	start = *rows[0].StartedAt
	end = start.Add(time.Millisecond)
	rows, err = read(21)
	require.NoError(t, err)
	require.Len(t, rows, 2, "start inclusive")
	end = start
	start = start.Add(-time.Millisecond)
	rows, err = read(21)
	require.NoError(t, err)
	require.Empty(t, rows, "end exclusive")
	start, end = time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(clone.ID)).Update(q.PlanJSON, "corrupt")
	require.NoError(t, err)
	_, err = read(21)
	require.ErrorIs(t, err, ErrUnavailable)
	plan := v.Plan
	plan.ExecutorPrincipalID = 0
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(clone.ID)).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)})
	require.NoError(t, err)
	_, err = read(21)
	require.ErrorIs(t, err, ErrUnavailable, "unknown frozen authorship cannot become guessed zero")
	_, err = f.m.loopAgentExecutionFactsOn(t.Context(), f.db, 7, 21, start, end)
	require.ErrorIs(t, err, ErrInvalidArgument)
}
