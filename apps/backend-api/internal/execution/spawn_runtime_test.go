package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/yunxiao"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnExecutionConnectorIsOptionalAndBindsOwnerReader(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindSpawnCosts(s))
	s = module.NewServices()
	require.NoError(t, module.Provide[governance.SpawnCostConnector](s, governance.SpawnCostConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.bindSpawnCosts(s), ErrUnavailable)
	s = module.NewServices()
	called := false
	require.NoError(t, module.Provide[governance.SpawnCostConnector](s, governance.SpawnCostConnector{BindExecution: func(read func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, bool, error)) error {
		called = read != nil
		return nil
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindSpawnCosts(s))
	require.True(t, called)
}

func providerExecution(t *testing.T) (*executionFixture, ExecutionView) {
	t.Helper()
	f := executionHarness(t)
	f.m.work.AssignedAgentOn = func(context.Context, *gorm.DB, WorkLink) (guid.ID, error) { return 21, nil }
	f.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{PrincipalID: 21, State: "ACTIVE", RuntimeKind: "CODEX", ModelID: "observed-model", ModelFamily: "source-family", Roles: []string{"EXECUTOR"}}, true, nil
	}
	return f, f.create(t)
}

func TestEnsembleExecutionReadsFrozenAuthorAndChecksPlanHash(t *testing.T) {
	f, v := providerExecution(t)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		fact, err := f.m.ensembleExecutionOn(t.Context(), tx, 7, 9, v.ID)
		require.NoError(t, err)
		require.Equal(t, v.ID, fact.ID)
		require.Equal(t, v.WorkItemID, fact.WorkItemID)
		require.EqualValues(t, 21, fact.PrincipalID, "frozen executor differs from initiating Human 11 and runner 101")
		require.Equal(t, v.State, fact.State)
		require.Equal(t, v.ResultState, fact.ResultState)
		for _, ref := range [][3]guid.ID{{8, 9, v.ID}, {7, 10, v.ID}, {7, 9, v.ID + 1}} {
			_, err = f.m.ensembleExecutionOn(t.Context(), tx, ref[0], ref[1], ref[2])
			require.ErrorIs(t, err, fleet.ErrMachineNotFound)
		}
		return nil
	}))
	_, err := f.m.ensembleExecutionOn(t.Context(), f.db, 7, 9, v.ID)
	require.ErrorIs(t, err, fleet.ErrInvalidArgument)
	q := query.BindExecutions(f.db, f.m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.PlanJSON, "{}")
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.ensembleExecutionOn(t.Context(), tx, 7, 9, v.ID)
		require.ErrorIs(t, err, ErrUnavailable, "plan JSON with a mismatched persisted hash must not supply authorship")
		return nil
	}))
}

func TestRuntimeUsesExactCommittedReceiptIntervalAndTerminalState(t *testing.T) {
	f, v := providerExecution(t)
	read := func(tenant, project, id guid.ID) (yunxiao.RuntimeFact, bool, error) {
		var fact yunxiao.RuntimeFact
		var found bool
		err := f.db.Transaction(func(tx *gorm.DB) error {
			var err error
			fact, found, err = f.m.runtimeOn(t.Context(), tx, tenant, project, id)
			return err
		})
		return fact, found, err
	}
	_, found, err := read(7, 9, v.ID)
	require.NoError(t, err)
	require.False(t, found, "created time is not runtime")
	f.pull(t, v.ID)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, found, err = read(7, 9, v.ID)
	require.NoError(t, err)
	require.False(t, found, "running intervals are not finished runtime")
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "FAILED", Content: "Observed runtime failure"})
	require.NoError(t, err)
	receipts := query.BindReceipts(f.db, f.m.names)
	start, err := receipts.WithContext(t.Context()).Where(receipts.ExecutionID.Eq(v.ID), receipts.Kind.Eq("ACCEPTED")).First()
	require.NoError(t, err)
	finish, err := receipts.WithContext(t.Context()).Where(receipts.ExecutionID.Eq(v.ID), receipts.Kind.Eq("FAILED")).First()
	require.NoError(t, err)
	require.Len(t, start.ContentHash, 64)
	require.Len(t, finish.ContentHash, 64)
	// Deliberately distinct fixture timestamps catch accidental use of the
	// execution aggregate's creation/update metadata instead of receipts.
	started := time.Date(2040, 2, 3, 4, 5, 6, 123000000, time.UTC)
	finished := started.Add(37*time.Second + 456*time.Millisecond)
	_, err = receipts.WithContext(t.Context()).Where(receipts.ID.Eq(start.ID)).Update(receipts.CreatedAt, started)
	require.NoError(t, err)
	_, err = receipts.WithContext(t.Context()).Where(receipts.ID.Eq(finish.ID)).Update(receipts.CreatedAt, finished)
	require.NoError(t, err)
	fact, found, err := read(7, 9, v.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, v.ID, fact.ExecutionID)
	require.EqualValues(t, 9, fact.ProjectID)
	require.Equal(t, v.WorkItemID, fact.WorkItemID)
	require.EqualValues(t, 21, fact.PrincipalID)
	require.True(t, started.Equal(fact.StartedAt))
	require.True(t, finished.Equal(fact.FinishedAt))
	require.Equal(t, 37456*time.Millisecond, fact.FinishedAt.Sub(fact.StartedAt))
	for _, ref := range [][3]guid.ID{{8, 9, v.ID}, {7, 10, v.ID}, {7, 9, v.ID + 1}} {
		_, found, err = read(ref[0], ref[1], ref[2])
		require.NoError(t, err)
		require.False(t, found)
	}
	_, err = receipts.WithContext(t.Context()).Where(receipts.ID.Eq(finish.ID)).Update(receipts.State, "SUCCEEDED")
	require.NoError(t, err)
	_, found, err = read(7, 9, v.ID)
	require.NoError(t, err)
	require.False(t, found, "terminal receipt must agree with actual execution state")
	_, err = receipts.WithContext(t.Context()).Where(receipts.ID.Eq(finish.ID)).Updates(map[string]any{"state": "FAILED", "created_at": started.Add(-time.Millisecond)})
	require.NoError(t, err)
	_, found, err = read(7, 9, v.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, found)
	_, err = receipts.WithContext(t.Context()).Where(receipts.ID.Eq(start.ID)).Delete()
	require.NoError(t, err)
	_, found, err = read(7, 9, v.ID)
	require.NoError(t, err)
	require.False(t, found, "no accepted receipt means unavailable rather than guessed start")
}

func TestRuntimeRejectsMissingAuthorshipOrCorruptPlan(t *testing.T) {
	f, v := providerExecution(t)
	f.pull(t, v.ID)
	_, err := f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 1, Kind: "ACCEPTED"})
	require.NoError(t, err)
	_, err = f.receipt(v.ID, ReceiptInput{DispatchEpoch: 1, Seq: 2, Kind: "FAILED", Content: "Observed failure"})
	require.NoError(t, err)
	q := query.BindExecutions(f.db, f.m.names)
	plan := v.Plan
	plan.ExecutorPrincipalID = 0
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Updates(map[string]any{"plan_json": string(encoded), "plan_hash": hash(encoded)})
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, found, err := f.m.runtimeOn(t.Context(), tx, 7, 9, v.ID)
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.PlanJSON, "{}")
	require.NoError(t, err)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, found, err := f.m.runtimeOn(t.Context(), tx, 7, 9, v.ID)
		require.ErrorIs(t, err, ErrUnavailable)
		require.False(t, found)
		return nil
	}))
	_, _, err = f.m.runtimeOn(t.Context(), f.db, 7, 9, v.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestSpawnExecutionUsesPersistedScopedAssignment(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	_, _, err := f.m.spawnExecutionOn(t.Context(), f.db, 7, 9, v.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		assignment, found, err := f.m.spawnExecutionOn(t.Context(), tx, 7, 9, v.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.EqualValues(t, 6, assignment)
		for _, ref := range [][3]guid.ID{{8, 9, v.ID}, {7, 10, v.ID}, {7, 9, v.ID + 1}} {
			_, found, err = f.m.spawnExecutionOn(t.Context(), tx, ref[0], ref[1], ref[2])
			require.NoError(t, err)
			require.False(t, found)
		}
		return nil
	}))
}

func TestBindSpawnCostsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindSpawnCosts(module.NewServices()), module.ErrServicesProviding)
}

func TestBindExecutionConsumersRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindExecutionConsumers(module.NewServices()), module.ErrServicesProviding)
}

func TestExecutionConsumerBindingsRejectMissingAndFailedCallbacks(t *testing.T) {
	for _, fault := range []string{"runtime_check", "runtime_bind", "execution_missing", "execution_bind", "consumer_missing", "consumer_bind", "none"} {
		t.Run(fault, func(t *testing.T) {
			f := executionHarness(t)
			s := module.NewServices()
			failure := errors.New("consumer binding rejected")
			ensemble := fleet.EnsembleConnector{CheckBranchOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error {
				return nil
			}, BindRuntime: func(fleet.EnsembleRuntime) error {
				if fault == "runtime_bind" {
					return failure
				}
				return nil
			}, BindExecution: func(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.EnsembleExecution, error)) error {
				if fault == "execution_bind" {
					return failure
				}
				return nil
			}}
			if fault == "runtime_check" {
				ensemble.CheckBranchOn = nil
			}
			if fault == "execution_missing" {
				ensemble.BindExecution = nil
			}
			require.NoError(t, module.Provide(s, ensemble))
			consumer := yunxiao.RuntimeConnector{BindExecution: func(yunxiao.RuntimeReader) error {
				if fault == "consumer_bind" {
					return failure
				}
				return nil
			}}
			if fault == "consumer_missing" {
				consumer.BindExecution = nil
			}
			require.NoError(t, module.Provide(s, consumer))
			require.NoError(t, s.Seal())
			e := f.m.bindExecutionConsumers(s)
			if fault == "none" {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
		})
	}
}
