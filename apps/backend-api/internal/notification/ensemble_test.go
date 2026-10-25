package notification

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEnsembleNoticesUseCurrentRequestAndSuppressResolvedOrRevoked(t *testing.T) {
	f := newFixture(t)
	pending, eligible := true, true
	selection := false
	f.m.ensembleFacts = fleet.EnsembleDecisionFacts{
		ReadOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.EnsembleDecisionFact, bool, error) {
			require.EqualValues(t, 7, tenant)
			require.EqualValues(t, 91, id)
			return fleet.EnsembleDecisionFact{ProjectID: 9, WorkItemID: 20, Version: 2, ApprovalPending: pending && !selection, SelectionPending: pending && selection}, true, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
			return eligible && actor == 12, nil
		},
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "ensemble_plan", Type: "ensemble.changed", Data: []byte(`{"action":"REQUEST_APPROVAL"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("ensemble.approval_requested")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, 1, rows[0].MergeCount)
	require.Equal(t, "/admin/7/entities/9/heavy?work_item_id=20&plan_id=91", rows[0].LinkPath)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		yes, err := f.m.visibleOn(t.Context(), tx, rows[0], 12)
		require.NoError(t, err)
		require.True(t, yes)
		pending = false
		yes, err = f.m.visibleOn(t.Context(), tx, rows[0], 12)
		require.NoError(t, err)
		require.False(t, yes)
		pending = true
		eligible = false
		yes, err = f.m.visibleOn(t.Context(), tx, rows[0], 12)
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
	pending, eligible, selection = true, true, true
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("ensemble.selection_requested")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	f.m.roles = governance.SeededRoles{RoleIDOn: func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 11, nil }}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		yes, err := f.m.visibleOn(t.Context(), tx, rows[0], 12)
		require.NoError(t, err)
		require.False(t, yes, "assignment authority alone cannot receive selection notices")
		return nil
	}))
}

func TestEnsembleProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.ensembleFacts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.EnsembleDecisionFact, bool, error) {
				if mode == "source" {
					return fleet.EnsembleDecisionFact{}, false, ErrUnavailable
				}
				return fleet.EnsembleDecisionFact{ProjectID: 81, WorkItemID: 80, Version: 1, ApprovalPending: true}, true, nil
			}
			f.m.ensembleFacts.EligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "ensemble_plan", Type: "ensemble.changed", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.ensembleFacts.ReadOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEnsembleOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
