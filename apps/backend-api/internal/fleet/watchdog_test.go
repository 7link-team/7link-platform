package fleet

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWatchdogDoesNotRevisitCancelledEnrollments(t *testing.T) {
	m, db, ctx, _, _ := enrollmentHarness(t)
	start := startTestEnrollment(t, m, ctx)
	require.NoError(t, m.CancelEnrollment(ctx, 7, start.ID, start.Device.DeviceCode))
	q := query.BindEnrollments(db, m.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(start.ID)).Update(q.ExpiresAt, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	m.enrollmentLease = func(context.Context, string, time.Duration, func(string, error)) (*dlock.Lock, error) {
		t.Error("terminal cancellation must not reacquire a saga lease")
		return nil, ErrUnavailable
	}
	require.NoError(t, m.JobHandlers()[0].Run(t.Context(), jobs.Job{TenantID: 7}))
}

func TestWatchdogScheduleAndUnavailable(t *testing.T) {
	m := &Module{}
	schedules := m.JobSchedules()
	require.Len(t, schedules, 1)
	require.Equal(t, 5*time.Second, schedules[0].Interval)
	require.Equal(t, jobs.ScopePerTenant, schedules[0].TenantScope)
	handlers := m.JobHandlers()
	require.Len(t, handlers, 1)
	require.Error(t, handlers[0].Run(t.Context(), jobs.Job{}))
	require.ErrorIs(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 7}), ErrUnavailable)
}

func TestWatchdogScansOnlyExpiredMachinesInJobTenantAndPreservesSlots(t *testing.T) {
	m, db, ctx, _, publisher := enrollmentHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.OpenSessionOn(ctx, tx, 7, 1, "watchdog")
		if err != nil {
			return err
		}
		_, err = m.ReserveSlotOn(ctx, tx, 7, 1, 900)
		return err
	}))
	old := time.Now().UTC().Add(-time.Minute)
	fresh := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Machines.String()).Where("id = ?", 1).Update("last_seen_at", old).Error)
	seedFleetMachine(t, m, db, 2, 7, 102, "ONLINE", &fresh)
	seedFleetMachine(t, m, db, 3, 8, 103, "ONLINE", &old)
	run := m.JobHandlers()[0].Run
	publisher.fail = true
	require.Error(t, run(t.Context(), jobs.Job{TenantID: 7}))
	row, err := query.BindMachines(db, m.names).WithContext(ctx).Where(query.BindMachines(db, m.names).ID.Eq(guid.ID(1))).First()
	require.NoError(t, err)
	require.Equal(t, "ONLINE", row.State)
	session, err := query.BindSessions(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", session.State)
	publisher.fail = false
	require.NoError(t, run(t.Context(), jobs.Job{TenantID: 7}))
	rows, err := query.BindMachines(db, m.names).WithContext(ctx).Order(query.BindMachines(db, m.names).ID).Find()
	require.NoError(t, err)
	require.Equal(t, "OFFLINE", rows[0].State)
	require.Equal(t, "ONLINE", rows[1].State)
	require.Equal(t, "ONLINE", rows[2].State, "another tenant's expired machine is untouched")
	session, err = query.BindSessions(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", session.State)
	require.NotNil(t, session.ClosedAt)
	slot, err := query.BindSlots(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Nil(t, slot.ReleasedAt, "watchdog cannot infer execution completion or release its slot")
	var before, after int64
	require.NoError(t, db.Table("test_fleet_effects").Count(&before).Error)
	require.NoError(t, run(t.Context(), jobs.Job{TenantID: 7}))
	require.NoError(t, db.Table("test_fleet_effects").Count(&after).Error)
	require.Equal(t, before, after, "replaying the watchdog cannot emit another expiry transition")
}
