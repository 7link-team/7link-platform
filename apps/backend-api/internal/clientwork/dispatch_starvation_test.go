package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestDispatchStarvationScanUsesRealPagedFactsAndRollsBack(t *testing.T) {
	m, db, _, _ := harness(t)
	calls := 0
	m.dispatchStarvation.ListOn = func(_ context.Context, tx *gorm.DB, tenant, after guid.ID, at time.Time) ([]delivery.DispatchStarvedWork, error) {
		calls++
		require.EqualValues(t, 1, tenant)
		require.Zero(t, after)
		require.False(t, at.IsZero())
		return []delivery.DispatchStarvedWork{{ProjectID: 3, WorkItemID: 11, Version: 7}}, nil
	}
	publisher := m.events
	m.events = failPublisher{}
	require.Error(t, m.scanDispatchStarvation(t.Context(), 1, now()))
	var n int64
	require.NoError(t, db.Table("audit_probe").Count(&n).Error)
	require.Zero(t, n)
	m.events = publisher
	require.NoError(t, m.scanDispatchStarvation(t.Context(), 1, now()))
	require.Equal(t, 2, calls)
	require.NoError(t, db.Table("audit_probe").Where("kind='clientwork.dispatch_starved'").Count(&n).Error)
	require.EqualValues(t, 1, n)
	m.dispatchStarvation.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]delivery.DispatchStarvedWork, error) {
		return nil, errors.New("facts failed")
	}
	require.Error(t, m.scanDispatchStarvation(t.Context(), 1, now()))
	require.Error(t, m.scanDispatchStarvation(t.Context(), 0, now()))
}
