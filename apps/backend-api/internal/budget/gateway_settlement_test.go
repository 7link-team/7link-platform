package budget

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestGatewayActualUsageUsesExistingReservationAndReplayFence(t *testing.T) {
	m, db := budgetHarness(t)
	s := module.NewServices()
	var settle fleet.GatewaySettlement
	require.NoError(t, module.Provide[fleet.GatewaySettlementConnector](s, fleet.GatewaySettlementConnector{Bind: func(p fleet.GatewaySettlement) error { settle = p; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindGatewaySettlement(s))
	require.NotNil(t, settle)
	ctx := t.Context()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "GATEWAY", 20, 50, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return settle(ctx, tx, 1, 10, 20, 7) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return settle(ctx, tx, 1, 10, 20, 7) }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return settle(ctx, tx, 1, 10, 20, 8) }), ErrConflict)
	fact, err := m.FactOn(ctx, db, 1, 10)
	require.Error(t, err)
	require.Zero(t, fact.SpentMicros)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, err := m.FactOn(ctx, tx, 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(7), fact.SpentMicros)
		require.Zero(t, fact.ReservedMicros)
		return nil
	}))
}
