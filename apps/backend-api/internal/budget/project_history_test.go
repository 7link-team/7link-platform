package budget

import (
	"testing"
	"time"

	"7link.com/platform/internal/budget/model"
	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectHistoryConnectorPublishesTenantScopedTrend(t *testing.T) {
	m, db := budgetHarness(t)
	s := module.NewServices()
	bound := false
	var read delivery.ProjectBudgetHistoryConnector
	read.Bind = func(history delivery.ProjectBudgetHistory) error {
		bound = true
		return db.Transaction(func(tx *gorm.DB) error {
			out, err := history(t.Context(), tx, 1, 10, 0)
			require.NoError(t, err)
			require.True(t, out.Available)
			require.Empty(t, out.Points)
			require.False(t, out.Truncated)
			return err
		})
	}
	require.NoError(t, module.Provide[delivery.ProjectBudgetHistoryConnector](s, read))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindProjectHistory(s))
	require.True(t, bound)
}

func TestProjectHistoryUsesActualSettlementsAndSnapshotBalance(t *testing.T) {
	m, db := budgetHarness(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for i := 0; i < 103; i++ {
		amount := int64(1)
		tenant := guid.ID(1)
		if i == 102 {
			tenant = 2
		}
		r := model.Reservation{ID: guid.ID(i + 1), TenantID: tenant, ProjectID: 10, Source: "EXECUTION", SourceID: guid.ID(i + 1), ReservedMicros: 1, ActualMicros: &amount, PriceKnown: true, State: "SETTLED", CreatedAt: at, SettledAt: &at}
		require.NoError(t, query.BindReservations(db, m.names).WithContext(t.Context()).Create(&r))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.projectHistoryOn(t.Context(), tx, 1, 10, 9007199254740993)
		require.NoError(t, e)
		require.True(t, out.Available)
		require.True(t, out.Truncated)
		require.Len(t, out.Points, 100)
		require.Equal(t, "9007199254740993", out.Points[99].SpentMicros)
		require.Equal(t, guid.ID(102), out.Points[99].ID)
		require.Equal(t, "9007199254740894", out.Points[0].SpentMicros)
		_, e = m.projectHistoryOn(t.Context(), tx, 1, 10, 1)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
	_, e := m.projectHistoryOn(t.Context(), db, 1, 10, 103)
	require.ErrorIs(t, e, ErrInvalidArgument)
}
