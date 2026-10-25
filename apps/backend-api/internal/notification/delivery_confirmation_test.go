package notification

import (
	"context"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryConfirmationNoticeUsesCurrentSourceOwnerAuthorityAndExactRequest(t *testing.T) {
	f := newFixture(t)
	pending, eligible := true, true
	f.m.deliveryConfirmations = delivery.DeliveryConfirmationFacts{
		ReadOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (delivery.DeliveryConfirmationFact, bool, error) {
			require.EqualValues(t, 7, tenant)
			require.EqualValues(t, 91, id)
			return delivery.DeliveryConfirmationFact{ID: 91, ProjectID: 9, PackageID: 81}, pending, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) {
			return eligible && actor == 12, nil
		},
	}
	f.m.entityPermission = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "delivery_confirmation", Type: "delivery.confirmation_requested", Data: []byte(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq(e.Type)).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, 1, rows[0].MergeCount)
	require.Equal(t, "/admin/7/entities/9/deliveries", rows[0].LinkPath)
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.True(t, yes, "source proves owner-scoped send authority")
	eligible = false
	yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, yes)
	eligible = true
	pending = false
	yes, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, yes)
}

func TestDeliveryConfirmationProjectionFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.deliveryConfirmations.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.DeliveryConfirmationFact, bool, error) {
				if mode == "source" {
					return delivery.DeliveryConfirmationFact{}, false, ErrUnavailable
				}
				return delivery.DeliveryConfirmationFact{ID: 91, ProjectID: 81, PackageID: 80}, true, nil
			}
			f.m.deliveryConfirmations.EligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "delivery_confirmation", Type: "delivery.confirmation_requested", Data: []byte(`{}`)}
			switch mode {
			case "missing":
				f.m.deliveryConfirmations.ReadOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectDeliveryConfirmationOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
