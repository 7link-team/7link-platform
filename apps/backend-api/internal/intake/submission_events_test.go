package intake

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubmissionDeliveryEventsAreCanonicalAuthorizedAndAtomic(t *testing.T) {
	m, db, ctx, allowed, publisher := changeSourceHarness(t)
	m.brief.explain = func(_ context.Context, _ *gorm.DB, in core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "delivery_send", in.Permission)
		return authz.Explanation{Allowed: *allowed}, nil
	}
	original := m.brief.audit
	m.brief.audit = func(ctx context.Context, tx *gorm.DB, e audit.DomainEvent) error {
		require.Equal(t, "intake.delivery_published", e.EventType)
		require.EqualValues(t, 10, e.TargetID)
		require.Equal(t, "intake", e.TargetType)
		require.NotContains(t, e.Detail, "content")
		return original(ctx, tx, e)
	}
	call := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.deliveryPublishedOn(ctx, tx, 7, 20, 10) })
	}
	*allowed = false
	require.ErrorIs(t, call(), ErrForbidden)
	*allowed = true
	require.NoError(t, call())
	var count int64
	require.NoError(t, db.Table("brief_probe").Count(&count).Error)
	require.EqualValues(t, 2, count)
	publisher.fail = true
	require.Error(t, call())
	require.NoError(t, db.Table("brief_probe").Count(&count).Error)
	require.EqualValues(t, 2, count, "audit and outbox must roll back together")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.deliveryPublishedOn(ctx, tx, 7, 21, 10) }))
}
