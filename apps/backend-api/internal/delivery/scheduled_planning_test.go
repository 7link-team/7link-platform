package delivery

import (
	"testing"
	"time"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestScheduledPlanningFactsUseHistoricalStageAndSystemTransaction(t *testing.T) {
	m, db, user, _ := workHarness(t)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(1)), q.ProjectID.Eq(guid.ID(99))).Updates(map[string]any{"stage": "CLOSED", "created_at": start.Add(-time.Hour), "stage_changed_at": start.Add(time.Hour)})
	require.NoError(t, err)
	tq := query.BindStageTransitions(db, m.service.names)
	require.NoError(t, tq.WithContext(t.Context()).Create(&model.ProjectStageTransition{TenantID: 1, ID: 99991, ProjectID: 99, FromStage: "ACTIVE", ToStage: "CLOSED", OccurredAt: start.Add(time.Hour), CriteriaSnapshot: "{}", Reason: "Complete", ActorKind: "HUMAN", ActorID: 11, AuthoritySource: "ROLE:delivery_lead", VersionAfter: 2}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.scheduledPlanningProjectsOn(user, tx, 1, start, end); return err }), ErrWorkForbidden)
	require.ErrorIs(t, func() error { _, err := m.scheduledPlanningProjectsOn(t.Context(), db, 1, start, end); return err }(), ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		projects, err := m.scheduledPlanningProjectsOn(t.Context(), tx, 1, start, end)
		require.NoError(t, err)
		require.Contains(t, projects, guid.ID(99), "CLOSED now does not erase eligible stage during period")
		txTransitions := query.BindStageTransitions(tx, m.service.names)
		_, err = txTransitions.WithContext(t.Context()).Where(txTransitions.TenantID.Eq(guid.ID(1)), txTransitions.ID.Eq(guid.ID(99991))).Update(txTransitions.OccurredAt, end.Add(time.Hour))
		require.NoError(t, err)
		projects, err = m.scheduledPlanningProjectsOn(t.Context(), tx, 1, start, end)
		require.NoError(t, err)
		require.Contains(t, projects, guid.ID(99), "first later closure preserves the preceding active period")
		projects, err = m.scheduledPlanningProjectsOn(t.Context(), tx, 2, start, end)
		require.NoError(t, err)
		require.NotContains(t, projects, guid.ID(99), "tenant isolation")
		facts, err := m.scheduledPlanningFactsOn(t.Context(), tx, 1, 99, 11, start, end)
		require.NoError(t, err)
		require.Contains(t, facts.MissingSources, "AUTOMATIC_VERIFICATION")
		_, err = m.scheduledPlanningFactsOn(user, tx, 1, 99, 11, start, end)
		require.ErrorIs(t, err, ErrWorkForbidden)
		return nil
	}))
}
