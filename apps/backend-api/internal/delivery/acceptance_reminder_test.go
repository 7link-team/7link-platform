package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestAcceptanceReminderProjectRiskPreservesOtherReasonsAndRejectsTerminal(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ProjectID.Eq(guid.ID(99))).UpdateSimple(q.HealthReasons.Value(`[{"code":"OTHER_RISK"}]`))
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			active, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 99, 10, true)
			require.True(t, active)
			return e
		}))
	}
	row, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "RED", row.Health)
	require.JSONEq(t, `[{"code":"OTHER_RISK"},{"code":"ACCEPTANCE_OVERDUE","package_id":"10"}]`, row.HealthReasons)
	for _, stage := range []string{"CLOSED", "CANCELLED"} { //nolint:misspell // Preserve the published domain or serialized contract spelling.
		_, err = q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ProjectID.Eq(guid.ID(99))).Update(q.Stage, stage)
		require.NoError(t, err)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			active, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 99, 11, true)
			require.False(t, active)
			return e
		}))
	}
}

func TestAcceptanceReminderProjectFailuresDoNotPublishRisk(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	injected := errors.New("project risk storage unavailable")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		active, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 98, 10, true)
		require.False(t, active)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		active, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 99, 10, false)
		require.True(t, active)
		return e
	}))
	for _, fault := range []string{"query", "update", "corrupt"} {
		if fault == "corrupt" {
			require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("health_reasons", "{}").Error)
		} else {
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == m.service.names.Projects.String() {
					tx.AddError(injected)
				}
			}
			if fault == "query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("risk-failure", callback))
			} else {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("risk-failure", callback))
			}
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			active, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 99, 10, true)
			require.False(t, active)
			return e
		})
		if fault == "corrupt" {
			require.ErrorIs(t, err, ErrUnavailable)
		} else {
			require.ErrorIs(t, err, injected)
		}
		require.NoError(t, db.Callback().Query().Remove("risk-failure"))
		require.NoError(t, db.Callback().Update().Remove("risk-failure"))
		row, e := query.BindProjects(db, m.service.names).WithContext(ctx).Take()
		require.NoError(t, e)
		require.NotEqual(t, "RED", row.Health)
	}
	_, e := m.acceptanceReminderProjectOn(ctx, db, 1, 99, 10, true)
	require.ErrorIs(t, e, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.acceptanceReminderProjectOn(ctx, tx, 1, 99, 0, true); return e }), ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := (&Module{}).acceptanceReminderProjectOn(ctx, tx, 1, 99, 10, true)
		return e
	}), ErrUnavailable)
}
