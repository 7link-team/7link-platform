package budget

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestExecutionSettlementFactsRequireEveryActualSettlement(t *testing.T) {
	m, db := budgetHarness(t)
	ctx := t.Context()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 10, true) }))
	read := func(ids []guid.ID) (int64, bool, error) {
		var cost int64
		var settled bool
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			cost, settled, e = m.executionSettlementsOn(ctx, tx, 1, 10, ids)
			return e
		})
		return cost, settled, e
	}
	cost, settled, e := read([]guid.ID{20})
	require.NoError(t, e)
	require.Zero(t, cost)
	require.False(t, settled)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 7, true) }))
	cost, settled, e = read([]guid.ID{20, 20})
	require.NoError(t, e)
	require.EqualValues(t, 7, cost)
	require.True(t, settled)
	_, settled, e = read([]guid.ID{20, 999})
	require.NoError(t, e)
	require.False(t, settled)
	cost, settled, e = read([]guid.ID{})
	require.NoError(t, e)
	require.Zero(t, cost)
	require.True(t, settled)
	_, _, e = m.executionSettlementsOn(ctx, db, 1, 10, []guid.ID{20})
	require.ErrorIs(t, e, ErrInvalidArgument)
}
func TestExecutionSettlementFactsUseActualPriceAndRejectInvalidScope(t *testing.T) {
	m, db := budgetHarness(t)
	require.NoError(t, db.Exec("UPDATE b_delivery_projects SET budget_cap_micros=NULL WHERE tenant_id=1 AND project_id=10").Error)
	ctx := t.Context()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 0, false) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 7, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		cost, done, e := m.executionSettlementsOn(ctx, tx, 1, 10, []guid.ID{20})
		require.NoError(t, e)
		require.True(t, done)
		require.EqualValues(t, 7, cost)
		_, done, e = m.executionSettlementsOn(ctx, tx, 2, 10, []guid.ID{20})
		require.NoError(t, e)
		require.False(t, done)
		_, _, e = m.executionSettlementsOn(ctx, tx, 1, 10, []guid.ID{-1})
		require.ErrorIs(t, e, ErrInvalidArgument)
		_, _, e = m.executionSettlementsOn(ctx, tx, 0, 10, nil)
		require.ErrorIs(t, e, ErrInvalidArgument)
		return nil
	}))
	_, _, e := m.executionSettlementsOn(ctx, nil, 1, 10, nil)
	require.ErrorIs(t, e, ErrInvalidArgument)
}
