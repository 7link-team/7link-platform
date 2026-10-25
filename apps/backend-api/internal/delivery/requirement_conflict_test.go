package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRequirementConflictRejectsIncompatibleWorkWithoutMutatingIt(t *testing.T) {
	for _, scenario := range []string{"not_draft", "executing", "other_escalation", "invalid_issues", "resolve_absent", "duplicate_raise", "outbox"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work, _ := readinessHarness(t)
			m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{AllowAll: true}, nil
			}
			changes := map[string]any{}
			action, want := "RAISE", ErrResultState
			failure := errors.New("outbox unavailable")
			switch scenario {
			case "not_draft":
				changes["state"] = "READY"
			case "executing":
				changes["current_execution_id"] = 81
			case "other_escalation":
				changes["escalation_reason"] = "BUDGET"
			case "invalid_issues":
				changes["readiness_issues"] = `[1]`
				want = ErrUnavailable
			case "resolve_absent":
				action = "RESOLVE"
			case "duplicate_raise":
				changes["readiness_issues"] = `[{"code":"REQUIREMENT_CONFLICT"}]`
				changes["escalation_reason"] = "REQUIREMENT_CONFLICT"
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
				want = failure
			}
			if len(changes) > 0 {
				require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id=?", work.ID).Updates(changes).Error)
			}
			q := query.BindWorkItems(db, m.service.names)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
			require.NoError(t, err)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, err := m.requirementConflictOn(ctx, tx, 1, 99, work.ID, 11, 11, requirementConflictBody{Version: work.Version, Action: action, Reason: "Human reviews incompatible requirements"})
				return err
			})
			require.ErrorIs(t, err, want)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after, "a rejected or unpublishable conflict must be atomic")
		})
	}
}

func TestRequirementConflictLifecycleKeepsDraftAndRollsBack(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		require.Contains(t, []string{"work_item_write", "scope_change"}, permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	m.service.events = realDeliveryPublisher(t, db)
	call := func(version int, action string) (WorkView, error) {
		var out WorkView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.requirementConflictOn(ctx, tx, 1, 99, work.ID, 11, 11, requirementConflictBody{Version: version, Action: action, Reason: "Resolve incompatible acceptance requirements"})
			return err
		})
		return out, err
	}
	raised, err := call(work.Version, "RAISE")
	require.NoError(t, err)
	require.Equal(t, "DRAFT", raised.State)

	q := query.BindWorkItems(db, m.service.names)
	current, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "REQUIREMENT_CONFLICT", *current.EscalationReason)
	require.False(t, current.ReadinessOK)
	fact, found, err := func() (DecisionNotificationFact, bool, error) {
		var f DecisionNotificationFact
		var ok bool
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			f, ok, e = m.decisionNotificationFactsOn(ctx, tx, 1, work.ID, "work_item")
			return e
		})
		return f, ok, err
	}()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, raised.Version, fact.ConflictVersion)
	_, err = call(work.Version, "RESOLVE")
	require.ErrorIs(t, err, ErrWorkVersion)
	original := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = call(raised.Version, "RESOLVE")
	require.Error(t, err)
	current, err = q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Equal(t, raised.Version, current.Version)
	require.NotNil(t, current.EscalationReason)
	m.service.audit = original
	resolved, err := call(raised.Version, "RESOLVE")
	require.NoError(t, err)
	require.Equal(t, "DRAFT", resolved.State)

	current, err = q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
	require.NoError(t, err)
	require.Nil(t, current.EscalationReason)
	require.False(t, current.ReadinessOK)
	require.NotContains(t, current.ReadinessIssues, "REQUIREMENT_CONFLICT")
	var count int64
	require.NoError(t, db.Table("domain_events").Where("event_type = ?", "work_item.escalated").Count(&count).Error)
	require.EqualValues(t, 1, count)
}
