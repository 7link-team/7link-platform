package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func fanoutHarness(t *testing.T, enumerate func(context.Context, guid.ID, int) ([]guid.ID, error), payload any) (*Module, *gorm.DB, model.Job) {
	t.Helper()
	base, db := queueHarness(t, 1000)
	base.activeTenants = enumerate
	m := scheduleOwner(t, base, Schedule{Name: "jobs.tenants", Kind: "jobs.work", Interval: time.Hour, TenantScope: ScopePerTenant, Payload: payload})
	require.NoError(t, m.reconcileSchedules(t.Context()))
	dueSchedule(t, db, "jobs.tenants", time.Second)
	count, err := m.fireDueSchedules(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, count)
	plan := storedSchedule(t, db, "jobs.tenants")
	source := storedJob(t, db, *plan.LastJobID)
	require.Equal(t, fanoutKind, source.Kind)
	require.Equal(t, "jobs.work", *source.FanoutKind)
	require.Equal(t, *plan.LastFiredAtMS, *source.FanoutFireMS)
	rows, err := m.claim(t.Context(), "default", "dispatcher", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, source.ID, rows[0].ID)
	return m, db, rows[0]
}

func TestFanoutExecutesFrozenFullSizePayloadAfterDefinitionChange(t *testing.T) {
	payload := json.RawMessage(`{"data":"` + strings.Repeat("x", 64*1024-len(`{"data":""}`)) + `"}`)
	m, db, source := fanoutHarness(t, func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{1, 2}, nil }, payload)
	next := scheduleOwner(t, m, Schedule{Name: "jobs.tenants", Kind: "jobs.other", Interval: time.Hour, TenantScope: ScopePerTenant, Payload: map[string]string{"changed": "new"}})
	require.NoError(t, next.reconcileSchedules(t.Context()))
	changed, err := m.executeClaim(t.Context(), source, nil)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "SUCCEEDED", storedJob(t, db, source.ID).State)
	var children []model.Job
	require.NoError(t, db.Table("j_background_jobs").Where("tenant_id > ?", 0).Order("tenant_id").Find(&children).Error)
	require.Len(t, children, 2)
	for i, child := range children {
		require.Equal(t, guid.ID(i+1), child.TenantID)
		require.Equal(t, "jobs.work", child.Kind)
		require.JSONEq(t, string(payload), child.Payload)
		require.Len(t, child.Payload, 64*1024)
		require.Equal(t, "jobs.tenants", *child.ConcurrencyKey)
		require.Equal(t, fanoutKey("jobs.tenants", *source.FanoutFireMS, child.TenantID), *child.IdempotencyKey)
		require.Nil(t, child.FanoutKind)
		require.Nil(t, child.FanoutFireMS)
	}
}

func TestFanoutRetriesCompletedPagesWithoutDuplicatesAtQuota(t *testing.T) {
	fail := true
	transient := errors.New("tenant page unavailable")
	m, db, source := fanoutHarness(t, func(_ context.Context, after guid.ID, limit int) ([]guid.ID, error) {
		require.Equal(t, 500, limit)
		if after == 0 {
			ids := make([]guid.ID, 500)
			for i := range ids {
				ids[i] = guid.ID(i + 1)
			}
			return ids, nil
		}
		require.Equal(t, guid.ID(500), after)
		if fail {
			return nil, transient
		}
		return []guid.ID{501}, nil
	}, nil)
	m.config.MaxPendingPerTenant = 1
	job := Job{ID: source.ID}
	require.ErrorIs(t, m.fanOut(t.Context(), job), transient)
	require.EqualValues(t, 501, queueCount(t, db))
	var original model.Job
	require.NoError(t, db.Table("j_background_jobs").Where("tenant_id = ?", 1).Take(&original).Error)
	fail = false
	require.NoError(t, m.fanOut(t.Context(), job))
	require.EqualValues(t, 502, queueCount(t, db))
	require.NoError(t, m.fanOut(t.Context(), job))
	require.EqualValues(t, 502, queueCount(t, db))
	require.Equal(t, original, storedJob(t, db, original.ID))
}

func TestFanoutPageFailureRollsBackEarlierChildrenInThatPage(t *testing.T) {
	m, db, source := fanoutHarness(t, func(context.Context, guid.ID, int) ([]guid.ID, error) { return []guid.ID{1, 2}, nil }, nil)
	m.config.MaxPendingPerTenant = 1
	_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 2, Kind: "jobs.work", IdempotencyKey: "full"})
	require.NoError(t, err)
	require.ErrorIs(t, m.fanOut(t.Context(), Job{ID: source.ID}), ErrQueueFull)
	require.EqualValues(t, 2, queueCount(t, db))
	var count int64
	require.NoError(t, db.Table("j_background_jobs").Where("tenant_id = ?", 1).Count(&count).Error)
	require.Zero(t, count)
	_, err = m.Cancel(t.Context(), nil, 2, "jobs.work", "full")
	require.NoError(t, err)
	require.NoError(t, m.fanOut(t.Context(), Job{ID: source.ID}))
	require.EqualValues(t, 4, queueCount(t, db))
}

func TestFanoutRejectsInvalidPagesAndMissingSnapshots(t *testing.T) {
	for _, page := range [][]guid.ID{{0}, {2, 1}, {1, 1}, make([]guid.ID, 501)} {
		m, db, source := fanoutHarness(t, func(context.Context, guid.ID, int) ([]guid.ID, error) { return page, nil }, nil)
		require.ErrorIs(t, m.fanOut(t.Context(), Job{ID: source.ID}), ErrInvalidRequest)
		require.EqualValues(t, 1, queueCount(t, db))
	}
	m, db, source := fanoutHarness(t, func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, nil }, nil)
	require.NoError(t, m.fanOut(t.Context(), Job{ID: source.ID}))
	require.ErrorIs(t, m.fanOut(t.Context(), Job{ID: source.ID, TenantID: 1}), ErrInvalidRequest)
	require.ErrorIs(t, m.fanOut(t.Context(), Job{}), ErrInvalidRequest)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, m.fanOut(canceled, Job{ID: source.ID}), context.Canceled)
	require.NoError(t, db.Table("j_background_jobs").Where("id = ?", source.ID).Updates(map[string]any{"fanout_kind": nil, "fanout_fire_ms": nil}).Error)
	require.ErrorIs(t, m.fanOut(t.Context(), Job{ID: source.ID}), ErrInvalidRequest)
	require.ErrorIs(t, NewModule(nil, Config{}).fanOut(t.Context(), Job{}), ErrUnavailable)
}

func TestFanoutBindingAndBoundedKeys(t *testing.T) {
	m := catalogModule()
	enumerate := func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, nil }
	require.ErrorIs(t, m.bindTenantFanout(nil), ErrInvalidRequest)
	require.NoError(t, m.bindTenantFanout(enumerate))
	require.ErrorIs(t, m.bindTenantFanout(enumerate), ErrInvalidRequest)
	require.NoError(t, m.Initialize(t.Context()))
	require.ErrorIs(t, m.bindTenantFanout(enumerate), ErrCatalogSealed)
	conflicting := catalogModule()
	require.NoError(t, conflicting.AddHandlers("jobs", catalogHandler(fanoutKind)))
	require.ErrorIs(t, conflicting.bindTenantFanout(enumerate), ErrInvalidDeclaration)
	require.Equal(t, "jobs.hourly:123:1", fanoutKey("jobs.hourly", 123, 1))
	key := fanoutKey(strings.Repeat("a", 128), 123, 1)
	require.True(t, validKey(key))
	require.Equal(t, key, fanoutKey(strings.Repeat("a", 128), 123, 1))
	require.NotEqual(t, key, fanoutKey(strings.Repeat("a", 128), 123, 2))
	require.NotEqual(t, key, fanoutKey(strings.Repeat("a", 128), 124, 1))
}

func TestFanoutSnapshotFailureRollsBackDispatchAndPlanProgress(t *testing.T) {
	base, db := queueHarness(t, 10)
	base.activeTenants = func(context.Context, guid.ID, int) ([]guid.ID, error) { return nil, nil }
	m := scheduleOwner(t, base, Schedule{Name: "jobs.tenants", Kind: "jobs.work", Interval: time.Hour, TenantScope: ScopePerTenant})
	require.NoError(t, m.reconcileSchedules(t.Context()))
	before := dueSchedule(t, db, "jobs.tenants", time.Second)
	require.NoError(t, db.Exec("CREATE TRIGGER reject_snapshot BEFORE UPDATE OF fanout_kind ON j_background_jobs BEGIN SELECT RAISE(ABORT, 'snapshot rejected'); END").Error)
	fired, err := m.fireSchedule(t.Context(), before.ID)
	require.ErrorContains(t, err, "snapshot rejected")
	require.False(t, fired)
	require.Zero(t, queueCount(t, db))
	require.Equal(t, before, storedSchedule(t, db, before.Name))
	require.NoError(t, db.Exec("DROP TRIGGER reject_snapshot").Error)
	fired, err = m.fireSchedule(t.Context(), before.ID)
	require.NoError(t, err)
	require.True(t, fired)
}
