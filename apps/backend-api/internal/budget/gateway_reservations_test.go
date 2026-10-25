package budget

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAcceptanceBindsActualSettlements(t *testing.T) {
	m, db := budgetHarness(t)
	s := module.NewServices()
	var read delivery.AcceptanceSettlements
	require.NoError(t, module.Provide[delivery.AcceptanceCostConnector](s, delivery.AcceptanceCostConnector{BindSettlements: func(f delivery.AcceptanceSettlements) error { read = f; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindGatewayReservations(s))
	require.NotNil(t, read)
	ctx := context.Background()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 10, true))
		_, known, err := read(ctx, tx, 1, 10, []guid.ID{20})
		require.NoError(t, err)
		require.False(t, known)
		require.NoError(t, m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 7, true))
		actual, known, err := read(ctx, tx, 1, 10, []guid.ID{20})
		require.NoError(t, err)
		require.True(t, known)
		require.EqualValues(t, 7, actual)
		return nil
	}))
}
