package execution

import (
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestWatchdogHandlerScansBeyondFreshCommandsAndIsTenantIsolated(t *testing.T) {
	f, _ := executionRoutesFixture(t)
	v := f.create(t)
	f.pull(t, v.ID)
	past := time.Now().Add(-time.Minute)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Update("lease_expires_at", past).Error)
	var original model.Execution
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Take(&original).Error)
	// A full first batch of healthy unleased commands must not hide the later
	// expired command. Each fixture has a distinct workspace and a held slot.
	rows := make([]*model.Execution, 100)
	for i := range rows {
		row := original
		row.ID, row.LeaseExpiresAt, row.Version = guid.ID(i+1), nil, 1
		plan := v.Plan
		plan.WorkdirID = guid.ID(i + 10)
		encoded, err := json.Marshal(plan)
		require.NoError(t, err)
		row.PlanJSON, row.PlanHash = string(encoded), hash(encoded)
		rows[i] = &row
		require.NoError(t, f.db.Exec("INSERT INTO slots(execution_id,active) VALUES (?,1)", row.ID).Error)
	}
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&rows).Error)
	other := original
	other.ID, other.TenantID = 500, 8
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Create(&other).Error)
	handler := f.m.JobHandlers()[0]
	require.Equal(t, watchdogJob, handler.Kind)
	require.Equal(t, time.Minute, handler.Timeout)
	f.pub.fail = true
	require.ErrorIs(t, handler.Run(t.Context(), jobs.Job{TenantID: 7}), ErrUnavailable)
	require.Equal(t, "CREATED", f.read(t, v.ID).State, "outbox failure must roll back watchdog state and slot release")
	var active int
	require.NoError(t, f.db.Table("slots").Select("active").Where("execution_id = ?", v.ID).Scan(&active).Error)
	require.Equal(t, 1, active)
	f.pub.fail = false
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 7}))
	expired := f.read(t, v.ID)
	require.Equal(t, "INTERRUPTED", expired.State)
	require.Equal(t, "UNKNOWN", expired.ResultState)
	require.Equal(t, "LEASE_EXPIRED", expired.StateReason)
	var fresh int64
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("tenant_id = ? AND state = ?", 7, "CREATED").Count(&fresh).Error)
	require.EqualValues(t, 100, fresh, "healthy unleased commands stay unchanged")
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", other.ID).Take(&other).Error)
	require.Equal(t, "CREATED", other.State, "a tenant job must never sweep another tenant")
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 7}))
	require.Equal(t, expired.Version, f.read(t, v.ID).Version, "repeated sweep must not publish another transition")
	require.NoError(t, handler.Run(t.Context(), jobs.Job{TenantID: 9}), "empty tenants complete normally")
}

func TestWatchdogHandlerRejectsInvalidTenantAndUnavailableDatabase(t *testing.T) {
	m := &Module{}
	handler := m.JobHandlers()[0]
	require.ErrorIs(t, handler.Run(t.Context(), jobs.Job{}), ErrInvalidArgument)
	require.ErrorIs(t, handler.Run(t.Context(), jobs.Job{TenantID: 7}), ErrUnavailable)
}

func TestExpiryIsIdempotentAndNeverRedispatches(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	leased := f.pull(t, v.ID)
	f.pub.fail = true
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.ExpireOn(t.Context(), tx, 7, v.ID, leased.LeaseExpiresAt.Add(time.Second))
	}), ErrUnavailable)
	require.Equal(t, "CREATED", f.read(t, v.ID).State)
	f.pub.fail = false
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.ExpireOn(t.Context(), tx, 7, v.ID, leased.LeaseExpiresAt.Add(time.Second))
	}))
	expired := f.read(t, v.ID)
	require.Equal(t, "INTERRUPTED", expired.State)
	require.Equal(t, "LEASE_EXPIRED", expired.StateReason)
	require.Equal(t, "UNKNOWN", expired.ResultState)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ExpireOn(t.Context(), tx, 7, v.ID, time.Now().Add(time.Hour)) }))
	require.Equal(t, expired.Version, f.read(t, v.ID).Version)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, e := f.m.PullOn(f.runner, tx, 7, f.fence)
		require.Empty(t, rows)
		return e
	}))
	schedule := f.m.JobSchedules()
	require.Len(t, schedule, 1)
	require.Equal(t, 5*time.Second, schedule[0].Interval)
	require.Len(t, f.m.JobHandlers(), 1)
}
