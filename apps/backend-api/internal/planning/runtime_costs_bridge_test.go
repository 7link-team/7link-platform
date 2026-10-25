package planning

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"

	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuntimeCostsAdapterRequiresObservedStartsAndActualSettlement(t *testing.T) {
	s := module.NewServices()
	tx := &gorm.DB{}
	var bound templates.RuntimeCosts
	var period RuntimeCosts
	at := time.Now().UTC()
	missing := false
	settled := false
	require.NoError(t, module.Provide[RuntimeCostsConnector](s, RuntimeCostsConnector{Bind: func(c RuntimeCosts) error { period = c; return nil }}))
	require.NoError(t, module.Provide[delivery.LoopAssignments](s, delivery.LoopAssignments{AgentsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (map[guid.ID]guid.ID, error) {
		return map[guid.ID]guid.ID{8: 9}, nil
	}}))
	require.NoError(t, module.Provide[templates.RuntimeCostsConnector](s, templates.RuntimeCostsConnector{Bind: func(c templates.RuntimeCosts) error { bound = c; return nil }}))
	require.NoError(t, module.Provide[execution.LoopExecutionFacts](s, execution.LoopExecutionFacts{ReadOn: func(_ context.Context, db *gorm.DB, tenant, project guid.ID, work []guid.ID, start, end time.Time) ([]execution.LoopExecutionFact, error) {
		require.Same(t, tx, db)
		require.Equal(t, []guid.ID{3}, work)
		fact := execution.LoopExecutionFact{ID: 5, AssignmentID: 8, StartedAt: &at}
		if missing {
			fact.StartedAt = nil
		}
		return []execution.LoopExecutionFact{fact}, nil
	}}))
	require.NoError(t, module.Provide[budget.SettlementFacts](s, budget.SettlementFacts{ExecutionsOn: func(_ context.Context, db *gorm.DB, tenant, project guid.ID, ids []guid.ID) (int64, bool, error) {
		require.Same(t, tx, db)
		require.Equal(t, []guid.ID{5}, ids)
		if !settled {
			return 0, false, nil
		}
		return 42, true, nil
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, (&runtimeCostsAdapter{}).InjectServices(s))
	_, known, err := bound.WorkOn(t.Context(), tx, 1, 2, []guid.ID{3})
	require.NoError(t, err)
	require.False(t, known)
	settled = true
	cost, known, err := bound.WorkOn(t.Context(), tx, 1, 2, []guid.ID{3})
	require.NoError(t, err)
	require.True(t, known)
	require.EqualValues(t, 42, cost)
	cost, known, err = period.PeriodOn(t.Context(), tx, 1, 2, 9, []guid.ID{3}, at.Add(-time.Hour), at.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, known)
	require.EqualValues(t, 42, cost)
	missing = true
	_, known, err = bound.WorkOn(t.Context(), tx, 1, 2, []guid.ID{3})
	require.NoError(t, err)
	require.False(t, known)
}

func TestRuntimeCostBridgePreservesUnknownAssignmentAndSourceFailures(t *testing.T) {
	for _, mode := range []string{"source", "assignments", "unknown", "foreign", "period_bind", "work_bind", "invalid_work"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			failure := errors.New("cost source unavailable")
			var period RuntimeCosts
			var work templates.RuntimeCosts
			settlements := 0
			require.NoError(t, module.Provide[execution.LoopExecutionFacts](s, execution.LoopExecutionFacts{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) ([]execution.LoopExecutionFact, error) {
				if mode == "source" {
					return nil, failure
				}
				at := time.Now()
				return []execution.LoopExecutionFact{{ID: 5, AssignmentID: 8, StartedAt: &at}, {ID: 6, AssignmentID: 8, StartedAt: &at}}, nil
			}}))
			require.NoError(t, module.Provide[budget.SettlementFacts](s, budget.SettlementFacts{ExecutionsOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
				settlements++
				require.Empty(t, ids, "other Agent executions must not be charged")
				return 0, true, nil
			}}))
			require.NoError(t, module.Provide[delivery.LoopAssignments](s, delivery.LoopAssignments{AgentsOn: func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (map[guid.ID]guid.ID, error) {
				require.Equal(t, []guid.ID{8}, ids)
				if mode == "assignments" {
					return nil, failure
				}
				if mode == "unknown" {
					return nil, nil
				}
				return map[guid.ID]guid.ID{8: 10}, nil
			}}))
			require.NoError(t, module.Provide[RuntimeCostsConnector](s, RuntimeCostsConnector{Bind: func(c RuntimeCosts) error {
				period = c
				if mode == "period_bind" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, module.Provide[templates.RuntimeCostsConnector](s, templates.RuntimeCostsConnector{Bind: func(c templates.RuntimeCosts) error {
				work = c
				if mode == "work_bind" {
					return failure
				}
				return nil
			}}))
			require.NoError(t, s.Seal())
			err := (&runtimeCostsAdapter{}).InjectServices(s)
			if mode == "period_bind" || mode == "work_bind" {
				require.ErrorIs(t, err, failure)
				return
			}
			require.NoError(t, err)
			if mode == "invalid_work" {
				for _, ids := range [][]guid.ID{nil, make([]guid.ID, 501)} {
					_, known, e := work.WorkOn(t.Context(), &gorm.DB{}, 1, 2, ids)
					require.ErrorIs(t, e, templates.ErrInvalid)
					require.False(t, known)
				}
				require.Zero(t, settlements)
				return
			}
			cost, known, err := period.PeriodOn(t.Context(), &gorm.DB{}, 1, 2, 9, []guid.ID{3}, time.Now().Add(-time.Hour), time.Now())
			require.Zero(t, cost)
			if mode == "source" || mode == "assignments" {
				require.ErrorIs(t, err, failure)
				require.False(t, known)
				require.Zero(t, settlements)
			} else if mode == "unknown" {
				require.NoError(t, err)
				require.False(t, known)
				require.Zero(t, settlements)
			} else {
				require.NoError(t, err)
				require.True(t, known)
				require.Equal(t, 1, settlements)
			}
		})
	}
}
