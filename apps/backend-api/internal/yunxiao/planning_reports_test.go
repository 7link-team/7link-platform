package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestPlanningCarryoverReaderKeepsExactTenantProjectPrincipalPeriod(t *testing.T) {
	m, db, ctx := workHarness(t)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	for i, dimensions := range [][3]guid.ID{{1, 99, 11}, {2, 99, 11}, {1, 98, 11}, {1, 99, 12}, {1, 99, 11}} {
		when := start
		if i == 4 {
			when = end
		}
		row := &model.PlanningCarryover{ID: guid.ID(i + 1), TenantID: dimensions[0], ProjectID: dimensions[1], PrincipalID: dimensions[2], WorkItemID: guid.ID(i + 77), FromIterationID: 200, CarriedAt: when}
		require.NoError(t, query.BindPlanningCarryovers(db, m.names).WithContext(ctx).Create(row))
	}
	rows, err := m.planningCarryoversOn(ctx, db, 1, 99, 11, start, end)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(77), rows[0].WorkItemID)
	rows, err = m.planningCarryoversOn(ctx, db, 1, 99, 0, start, end)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	_, err = m.planningCarryoversOn(context.Background(), (*gorm.DB)(nil), 1, 99, 11, start, end)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestPlanningCarryoverReaderRefusesTruncatedAndFailedSource(t *testing.T) {
	m, db, ctx := workHarness(t)
	start := m.now()
	end := start.Add(time.Hour)
	rows := make([]*model.PlanningCarryover, 501)
	for i := range rows {
		rows[i] = &model.PlanningCarryover{ID: guid.ID(i + 1), TenantID: 1, ProjectID: 99, PrincipalID: 11, WorkItemID: guid.ID(i + 1000), FromIterationID: 200, CarriedAt: start}
	}
	require.NoError(t, query.BindPlanningCarryovers(db, m.names).WithContext(ctx).Create(rows...))
	facts, e := m.planningCarryoversOn(ctx, db, 1, 99, 11, start, end)
	require.ErrorIs(t, e, ErrUnavailable)
	require.Nil(t, facts)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("carry_report_storage", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.PlanningCarryovers.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	defer db.Callback().Query().Remove("carry_report_storage")
	facts, e = m.planningCarryoversOn(ctx, db, 1, 99, 11, start, end)
	require.ErrorIs(t, e, ErrUnavailable)
	require.Nil(t, facts)
}
