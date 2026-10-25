package notification

import (
	"context"
	"encoding/json"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCancelledDependencyUsesCurrentRelationAndExecutor(t *testing.T) {
	f := newFixture(t)
	facts := []delivery.DependencyNotificationFact{{ProjectID: 9, WorkItemID: 81, ExecutorID: 12}}
	f.m.decisionFacts.CancelledDependentsOn = func(_ context.Context, _ *gorm.DB, tenant, pred guid.ID) ([]delivery.DependencyNotificationFact, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, pred)
		return facts, nil
	}
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, Type: "work_item.state_changed", SubjectKind: "work_item", Data: json.RawMessage(`{"to":"CANCELLED","notify_principal_ids":[11]}`)} //nolint:misspell // Canonical domain status.
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(dependencyCanceledType)).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, 1, rows[0].MergeCount)
	require.Equal(t, "depc:81:91", *rows[0].DedupeKey)
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.True(t, visible)
	facts[0].ExecutorID = 13
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
	facts = nil
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible, "removing the dependency or canceling its successor suppresses the notice")
	require.Equal(t, "OFF", defaultEmail(rows[0].Type, Info))
}

func TestDependencyProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.decisionFacts.CancelledDependentsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]delivery.DependencyNotificationFact, error) {
				if mode == "source" {
					return nil, ErrUnavailable
				}
				return []delivery.DependencyNotificationFact{{ProjectID: 81, WorkItemID: 80, ExecutorID: 11}}, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "work_item", Type: "work_item.state_changed", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.decisionFacts.CancelledDependentsOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectCancelledDependentsOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
