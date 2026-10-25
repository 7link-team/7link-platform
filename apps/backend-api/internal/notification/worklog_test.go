package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWorklogSourceFailuresAndMalformedCutoffFailClosed(t *testing.T) {
	for _, mode := range []string{"tenant", "missing", "database", "candidates", "order", "source", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.now = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) }
			f.m.worklogApprovalCount = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (int, error) {
				if mode == "source" {
					return 0, ErrUnavailable
				}
				return 1, nil
			}
			job := jobs.Job{TenantID: 7}
			switch mode {
			case "tenant":
				job.TenantID = 0
			case "missing":
				f.m.worklogApprovalCount = nil
			case "database":
				f.m.runtime = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.m.worklogWeeklyJob(t.Context(), job))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	f.m.worklogApprovalCount = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (int, error) { return 0, ErrUnavailable }
	for _, raw := range []string{"[]", `{"cutoff":"bad"}`} {
		yes, err := f.m.worklogAudienceOn(t.Context(), f.db, &model.Notification{Params: raw}, 11)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestWeeklyWorklogSummaryUsesCurrentApprovalsAndPermanentWeeklyReceipt(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	f.m.now = func() time.Time { return now }
	count := 3
	f.m.worklogApprovalCount = func(_ context.Context, _ *gorm.DB, tenant, actor guid.ID, cutoff time.Time) (int, error) {
		require.EqualValues(t, 7, tenant)
		require.False(t, cutoff.After(now))
		if actor == 12 {
			return count, nil
		}
		return 0, nil
	}
	for range 2 {
		require.NoError(t, f.m.worklogWeeklyJob(t.Context(), jobs.Job{TenantID: 7}))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("worklog.approval_pending")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	require.Equal(t, "OFF", defaultEmail(rows[0].Type, Info))
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.True(t, visible)
	require.Equal(t, 3, rows[0].MergeCount)
	count = 0
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible, "approving all rows or losing authority removes the summary")
	count = 2
	now = now.Add(24 * time.Hour)
	require.NoError(t, f.m.worklogWeeklyJob(t.Context(), jobs.Job{TenantID: 7}))
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "Tuesday cannot create another weekly summary")
	now = now.Add(6 * 24 * time.Hour)
	require.NoError(t, f.m.worklogWeeklyJob(t.Context(), jobs.Job{TenantID: 7}))
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	require.Equal(t, "0 0 * * 1", f.m.JobSchedules()[0].Cron)
}
