package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchScannerPaginatesAndRefusesUnavailableOrNonAdvancingSources(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx, at := t.Context(), now()
	queue := &forecastQueueProbe{}
	m.forecastQueue = queue
	reads := 0
	m.dispatchSchedule.ProjectsOn = func(_ context.Context, _ *gorm.DB, tenant, after guid.ID) ([]guid.ID, error) {
		reads++
		if after == 0 {
			ids := make([]guid.ID, 100)
			for i := range ids {
				ids[i] = guid.ID(i + 1)
			}
			return ids, nil
		}
		require.EqualValues(t, 100, after)
		return nil, nil
	}
	require.NoError(t, m.scanDispatch(ctx, 1, at))
	require.Equal(t, 2, reads)
	require.Len(t, queue.requests, 100)
	failure := errors.New("project scan failed")
	m.dispatchSchedule.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return nil, failure }
	require.ErrorIs(t, m.scanDispatch(ctx, 1, at), failure)
	m.dispatchSchedule.ProjectsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error) { return []guid.ID{0}, nil }
	require.ErrorIs(t, m.scanDispatch(ctx, 1, at), ErrUnavailable)
	m.forecastQueue = nil
	require.ErrorIs(t, m.scanDispatch(ctx, 1, at), ErrUnavailable)
	m.dispatchSchedule.ProjectsOn = nil
	require.ErrorIs(t, m.scanDispatch(ctx, 1, at), ErrUnavailable)
	require.Error(t, m.runDispatchCycle(ctx, 1, 0, at))
	require.ErrorIs(t, m.runDispatchCycle(ctx, 1, 3, at), ErrUnavailable)
	m.dispatchSchedule.ReadyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (delivery.DispatchScheduleBatch, error) {
		return delivery.DispatchScheduleBatch{}, failure
	}
	require.ErrorIs(t, m.runDispatchCycle(ctx, 1, 3, at), failure)
	require.NoError(t, m.dispatchScheduleEventOn(ctx, nil, events.Envelope{}))
}

func TestDispatchSchedulingDurableDebounceAndFallback(t *testing.T) {
	m, db, _, _ := harness(t)
	queue := &forecastQueueProbe{}
	m.forecastQueue = queue
	at := time.Date(2026, 10, 21, 0, 0, 1, 0, time.UTC)
	for _, offset := range []time.Duration{0, 10 * time.Second} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.dispatchScheduleEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, OccurredAt: at.Add(offset)})
		}))
	}
	require.Len(t, queue.requests, 2)
	require.Equal(t, queue.requests[0].IdempotencyKey, queue.requests[1].IdempotencyKey)
	require.Equal(t, "dispatch:3", queue.requests[0].ConcurrencyKey)
	require.Equal(t, 30*time.Second, queue.requests[0].Delay)
	m.dispatchSchedule.ProjectsOn = func(_ context.Context, tx *gorm.DB, tenant, after guid.ID) ([]guid.ID, error) {
		_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, ok)
		require.EqualValues(t, 1, tenant)
		require.Zero(t, after)
		return []guid.ID{3, 4}, nil
	}
	require.NoError(t, m.scanDispatch(t.Context(), 1, at))
	require.Len(t, queue.requests, 4)
	require.Equal(t, "dispatch:4", queue.requests[3].ConcurrencyKey)
	require.Equal(t, 5*time.Minute, m.JobSchedules()[2].Interval)
	for _, h := range m.dispatchHandlers() {
		require.Error(t, h.Run(t.Context(), jobs.Job{}))
	}
}

func TestDispatchCycleTenAttemptsContinueAfterFailureAndUseCurrentLead(t *testing.T) {
	m, _, _ := dispatchFixture(t)
	m.dispatchSchedule.ReadyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (delivery.DispatchScheduleBatch, error) {
		items := []delivery.DispatchScheduledWork{}
		for id := guid.ID(100); id < 112; id++ {
			items = append(items, delivery.DispatchScheduledWork{ID: id, Version: 1})
		}
		return delivery.DispatchScheduleBatch{PrincipalID: 25, Items: items}, nil
	}
	m.dispatchFleet.PrincipalsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{25}, nil }
	attempts := []guid.ID{}
	m.dispatch.AutoLockOn = func(ctx context.Context, _ *gorm.DB, tenant, project, work guid.ID, _ []guid.ID) error {
		require.EqualValues(t, 25, authn.PrincipalIDFromContext(ctx))
		require.EqualValues(t, 1, authn.TenantIDFromContext(ctx))
		require.EqualValues(t, 3, project)
		attempts = append(attempts, work)
		return errors.New("current lead permission revoked")
	}
	require.Error(t, m.runDispatchCycle(t.Context(), 1, 3, now()))
	require.Len(t, attempts, 10)
	require.EqualValues(t, 109, attempts[9])
	m.dispatchSchedule.ReadyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (delivery.DispatchScheduleBatch, error) {
		return delivery.DispatchScheduleBatch{}, nil
	}
	require.NoError(t, m.runDispatchCycle(t.Context(), 1, 3, now()))
	require.Len(t, attempts, 10, "no active lead must not synthesize an actor")
}
