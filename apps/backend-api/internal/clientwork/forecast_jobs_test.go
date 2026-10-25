package clientwork

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type forecastQueueProbe struct {
	jobs.Queue
	requests []jobs.EnqueueRequest
}

func (p *forecastQueueProbe) Enqueue(_ context.Context, _ *gorm.DB, in jobs.EnqueueRequest) (jobs.EnqueueResult, error) {
	p.requests = append(p.requests, in)
	return jobs.EnqueueResult{JobID: 1, Created: true}, nil
}

func TestForecastJobsDeclareHourlyScheduleAndDelayEventRefresh(t *testing.T) {
	m, db, _, _ := harness(t)
	queue := &forecastQueueProbe{}
	m.forecastQueue = queue
	at := time.Date(2026, 10, 20, 12, 0, 5, 0, time.UTC)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.forecastEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, OccurredAt: at})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.forecastEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3, OccurredAt: at.Add(30 * time.Second)})
	}))
	require.Len(t, queue.requests, 2)
	require.Equal(t, queue.requests[0].IdempotencyKey, queue.requests[1].IdempotencyKey)
	require.Equal(t, time.Minute, queue.requests[0].Delay)
	require.Equal(t, toleranceForecastJob, queue.requests[0].Kind)
	schedules := m.JobSchedules()
	require.Equal(t, time.Hour, schedules[1].Interval)
	require.Equal(t, jobs.ScopePerTenant, schedules[1].TenantScope)
	handler := m.forecastHandler()
	require.Error(t, handler.Run(t.Context(), jobs.Job{}))
	require.Error(t, handler.Run(t.Context(), jobs.Job{TenantID: 1, Payload: []byte("bad")}))
	require.ErrorIs(t, (&Module{}).forecastDue(t.Context(), guid.ID(1), 0, at), ErrUnavailable)
}

func TestForecastRefreshWithoutProjectOrQueueDoesNotInventMeasurements(t *testing.T) {
	m, db, _, _ := harness(t)
	queue := &forecastQueueProbe{}
	m.forecastQueue = queue
	for _, event := range []events.Envelope{{TenantID: 0, ProjectID: 3}, {TenantID: 1, ProjectID: 0}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.forecastEventOn(t.Context(), tx, event) }))
	}
	require.Empty(t, queue.requests, "tenant-wide events cannot be assigned a fabricated project")
	m.forecastQueue = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.forecastEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: 3})
	}), ErrUnavailable)
	for _, input := range []struct {
		tenant, project guid.ID
		at              time.Time
	}{{0, 3, now()}, {1, -1, now()}, {1, 3, time.Time{}}} {
		require.ErrorIs(t, m.forecastDue(t.Context(), input.tenant, input.project, input.at), ErrInvalid)
	}
	require.ErrorIs(t, m.forecastHandler().Run(t.Context(), jobs.Job{TenantID: 1, Payload: []byte(`{"project_id":3}`)}), ErrUnavailable)
	var count int64
	require.NoError(t, db.Table(m.names.Tolerance.String()).Count(&count).Error)
	require.Zero(t, count)
}
