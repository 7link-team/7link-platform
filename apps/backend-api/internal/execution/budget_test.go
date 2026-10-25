package execution

import (
	"7link.com/platform/internal/fleet"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestBudgetUnknownFreeCeilingAndOverflow(t *testing.T) {
	ceiling := int64(10)
	budget := BudgetFact{CapMicros: &ceiling, SpentMicros: 8, ReservedMicros: 1}
	_, _, err := CheckBudget(budget, 1, 1, nil)
	require.ErrorIs(t, err, ErrPriceUnknown)
	cost, known, err := CheckBudget(budget, 1, 1, &fleet.TokenPrice{InputMicros: 1, OutputMicros: 1})
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, int64(1), cost)
	_, _, err = CheckBudget(budget, 1000000, 0, &fleet.TokenPrice{InputMicros: 2})
	require.ErrorIs(t, err, ErrBudgetExceeded)
	cost, known, err = CheckBudget(budget, math.MaxInt64, math.MaxInt64, &fleet.TokenPrice{})
	require.NoError(t, err)
	require.True(t, known)
	require.Zero(t, cost)
	ceiling = math.MaxInt64
	_, _, err = CheckBudget(BudgetFact{CapMicros: &ceiling, SpentMicros: math.MaxInt64, ReservedMicros: 1}, 0, 0, &fleet.TokenPrice{})
	require.ErrorIs(t, err, ErrBudgetExceeded)
	_, known, err = CheckBudget(BudgetFact{}, 1, 1, nil)
	require.NoError(t, err)
	require.False(t, known)
}

func TestActualCostUsesPinnedPriceAndPreservesUnknown(t *testing.T) {
	plan := ExecutionPlan{PriceSnapshot: &ExecutionPrice{InputMicros: 3, OutputMicros: 7}}
	cost, err := actualExecutionCost(plan, &ExecutionUsage{InputTokens: 500000, OutputTokens: 500000})
	require.NoError(t, err)
	require.Equal(t, int64(5), *cost)
	cost, err = actualExecutionCost(plan, nil)
	require.NoError(t, err)
	require.Nil(t, cost)
	cost, err = actualExecutionCost(ExecutionPlan{}, &ExecutionUsage{InputTokens: 1})
	require.NoError(t, err)
	require.Nil(t, cost)
	_, err = actualExecutionCost(plan, &ExecutionUsage{InputTokens: -1})
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = actualExecutionCost(ExecutionPlan{PriceSnapshot: &ExecutionPrice{InputMicros: math.MaxInt64}}, &ExecutionUsage{InputTokens: math.MaxInt64})
	require.Error(t, err)
}
