package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"testing"
	"time"
)

func TestPausedLoopResumesFromNowAndSchedulerContinuesPastRevocation(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Review", DueAt: now.Add(time.Minute), IntervalMinutes: 15, MaxFires: 20, Checks: "Check", BudgetMinor: 1, Escalation: "Stop", OutputFormat: "Evidence"}
	var first, second ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.reminderOn(ctx, tx, 1, 7, in)
		require.NoError(t, e)
		second, e = m.reminderOn(ctx, tx, 1, 7, in)
		require.NoError(t, e)
		_, e = m.changeReminderOn(ctx, tx, 1, 7, first.ID, 1, "PAUSED")
		return e
	}))
	now = now.Add(24 * time.Hour)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.changeReminderOn(ctx, tx, 1, 7, first.ID, 2, "ACTIVE")
		require.NoError(t, e)
		require.Equal(t, now.Add(15*time.Minute), v.DueAt)
		require.Zero(t, v.FireCount)
		return nil
	}))
	// Inaccessible low-ID reminders must not starve later due reminders.
	q := query.BindReminder(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Updates(map[string]any{"thread_id": 5, "next_fire_at": now.Add(-time.Minute)})
	require.NoError(t, e)
	handler := m.JobHandlers()[0]
	require.Error(t, handler.Run(ctx, jobs.Job{TenantID: 1}))
	rows, e := q.WithContext(ctx).Order(q.ID).Find()
	require.NoError(t, e)
	require.Len(t, rows, 2)
	require.Zero(t, rows[0].FireCount)
	require.Positive(t, rows[1].FireCount)
	inbox, e := m.inboxHTTP(ctx, &InboxInput{AgentPath: AgentPath{TenantID: "1", AgentPrincipalID: "7"}, Limit: 50})
	require.NoError(t, e)
	require.Len(t, inbox.Body.Data.Items, 1)
	require.Equal(t, second.ID, inbox.Body.Data.Items[0].SubjectID)
	require.Error(t, handler.Run(ctx, jobs.Job{}))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, handler.Run(canceled, jobs.Job{TenantID: 1}), context.Canceled)
	require.Equal(t, 30*time.Second, m.JobSchedules()[0].Interval)
	m.runtime = nil
	require.ErrorIs(t, handler.Run(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
}

func TestReminderValidationPaginationAndTerminalStates(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Once", DueAt: now.Add(time.Minute), MaxFires: 1}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, modify := range []func(*ReminderInput){func(v *ReminderInput) { v.Title = "" }, func(v *ReminderInput) { v.DueAt = now.Add(-time.Minute) }, func(v *ReminderInput) { v.IntervalMinutes = 1 }, func(v *ReminderInput) { v.IntervalMinutes = 15 }, func(v *ReminderInput) { v.BudgetMinor = -1 }} {
			bad := in
			modify(&bad)
			_, e := m.reminderOn(ctx, tx, 1, 7, bad)
			require.ErrorIs(t, e, ErrInvalid)
		}
		a, e := m.reminderOn(ctx, tx, 1, 7, in)
		require.NoError(t, e)
		b, e := m.reminderOn(ctx, tx, 1, 7, in)
		require.NoError(t, e)
		rows, more, e := m.remindersOn(ctx, tx, 1, 7, 0, 1)
		require.NoError(t, e)
		require.True(t, more)
		require.Equal(t, a.ID, rows[0].ID)
		rows, more, e = m.remindersOn(ctx, tx, 1, 7, a.ID, 1)
		require.NoError(t, e)
		require.False(t, more)
		require.Equal(t, b.ID, rows[0].ID)
		_, _, e = m.remindersOn(ctx, tx, 1, 7, 0, 0)
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.changeReminderOn(ctx, tx, 1, 7, a.ID, 0, "PAUSED")
		require.ErrorIs(t, e, ErrInvalid)
		require.NoError(t, m.fireReminderOn(ctx, tx, 1, a.ID, now.Add(time.Minute)))
		_, e = m.changeReminderOn(ctx, tx, 1, 7, a.ID, 2, "ACTIVE")
		require.ErrorIs(t, e, ErrConflict)
		require.NoError(t, m.fireReminderOn(ctx, tx, 1, a.ID, now.Add(time.Hour)))
		_, e = m.changeReminderOn(ctx, tx, 1, 7, b.ID, 1, reminderCancelled)
		require.NoError(t, e)
		return nil
	}))
	m.threads.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error) {
		return 0, ErrForbidden
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reminderOn(ctx, tx, 1, 7, in)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
}

func TestReminderWorkCalendarLifecycleCurrentAuthorityAndRollback(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	allowed := true
	m.reminderWork = delivery.ReminderWorkAccess{CheckOn: func(_ context.Context, _ *gorm.DB, tenant, project, work, actor guid.ID) (int64, error) {
		if !allowed || tenant != 1 || project != 3 || work != 44 || actor != 7 {
			return 0, ErrForbidden
		}
		return 27, nil
	}}
	m.reminderCalendar = func(_ context.Context, _ *gorm.DB, tenant guid.ID, kind string, after time.Time) (time.Time, string, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, "DAY", kind)
		return after.Add(24 * time.Hour), "America/Los_Angeles", nil
	}
	in := ReminderInput{AnchorKind: "WORK_ITEM", WorkItemID: new(guid.ID(44)), ProjectID: new(guid.ID(3)), ScheduleKind: "PERIOD", PeriodKind: "DAY", Title: "Work review", MaxFires: 3}
	var v ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; v, e = m.reminderOn(ctx, tx, 1, 7, in); return e }))
	require.Equal(t, "WORK_ITEM", v.AnchorKind)
	require.Equal(t, "America/Los_Angeles", v.Timezone)
	now = v.DueAt
	allowed = false
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, v.ID, now) }))
	q := query.BindReminder(db, m.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Zero(t, r.FireCount)
	allowed = true
	m.events = badPublisher{}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, v.ID, now) }))
	r, e = q.WithContext(ctx).Where(q.ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Zero(t, r.FireCount)
}

func TestReminderWorkPeriodFireCoalescesAndResumeUsesCurrentCalendar(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	now := m.now()
	m.now = func() time.Time { return now }
	m.reminderWork = delivery.ReminderWorkAccess{CheckOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error) { return 27, nil }}
	m.reminderCalendar = func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, after time.Time) (time.Time, string, error) {
		return after.Add(24 * time.Hour), "UTC", nil
	}
	var v ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.reminderOn(ctx, tx, 1, 7, ReminderInput{AnchorKind: "WORK_ITEM", WorkItemID: new(guid.ID(44)), ProjectID: new(guid.ID(3)), ScheduleKind: "PERIOD", PeriodKind: "DAY", Title: "Review", MaxFires: 3})
		return e
	}))
	now = now.Add(72 * time.Hour)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, v.ID, now) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, v.ID, now) }))
	rows, e := query.BindInboxEvent(db, m.names).WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "work_item:44", rows[0].StreamKey)
	require.EqualValues(t, 27, rows[0].StreamSeq)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.changeReminderOn(ctx, tx, 1, 7, v.ID, 2, "PAUSED"); return e }))
	now = now.Add(2 * time.Hour)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { v, e = m.changeReminderOn(ctx, tx, 1, 7, v.ID, 3, "ACTIVE"); return e }))
	require.Equal(t, now.Add(24*time.Hour), v.DueAt)
	m.reminderCalendar = func(context.Context, *gorm.DB, guid.ID, string, time.Time) (time.Time, string, error) {
		return time.Time{}, "", errors.New("calendar unavailable")
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, v.ID, v.DueAt) }))
	r, e := query.BindReminder(db, m.names).WithContext(ctx).Where(query.BindReminder(db, m.names).ID.Eq(v.ID)).First()
	require.NoError(t, e)
	require.Equal(t, 1, r.FireCount)
}

func TestReminderHTTPWorkPeriodIdempotencyAndReplayReauthorization(t *testing.T) {
	m, db, handler, _ := agentHTTPHarness(t)
	ctx := agentContext(t, 7)
	allowed := true
	m.reminderWork = delivery.ReminderWorkAccess{CheckOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (int64, error) {
		if !allowed {
			return 0, ErrForbidden
		}
		return 27, nil
	}}
	m.reminderCalendar = func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, after time.Time) (time.Time, string, error) {
		return after.Add(24 * time.Hour), "UTC", nil
	}
	body, e := json.Marshal(ReminderInput{AnchorKind: "WORK_ITEM", WorkItemID: new(guid.ID(44)), ProjectID: new(guid.ID(3)), ScheduleKind: "PERIOD", PeriodKind: "DAY", Title: "Calendar review", MaxFires: 4})
	require.NoError(t, e)
	path := "/tenants/1/agents/7/reminders"
	first := callAgent(t, handler, ctx, http.MethodPost, path, "reminder-calendar-one", string(body))
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	again := callAgent(t, handler, ctx, http.MethodPost, path, "reminder-calendar-one", string(body))
	require.Equal(t, http.StatusCreated, again.Code, again.Body.String())
	var firstEnvelope, replayEnvelope struct {
		Data ReminderView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstEnvelope))
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &replayEnvelope))
	require.Equal(t, firstEnvelope.Data, replayEnvelope.Data)
	count, e := query.BindReminder(db, m.names).WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
	allowed = false
	denied := callAgent(t, handler, ctx, http.MethodPost, path, "reminder-calendar-one", string(body))
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
}

func TestReminderQuotaCountsPausedAndReleasesTerminalCapacity(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Review", DueAt: m.now().Add(time.Hour), MaxFires: 1}
	var first ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < 50; i++ {
			v, e := m.reminderOn(ctx, tx, 1, 7, in)
			if e != nil {
				return e
			}
			if i == 0 {
				first = v
			}
		}
		_, e := m.changeReminderOn(ctx, tx, 1, 7, first.ID, 1, "PAUSED")
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.reminderOn(ctx, tx, 1, 7, in); return e }), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeReminderOn(ctx, tx, 1, 7, first.ID, 2, reminderCancelled)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.reminderOn(ctx, tx, 1, 7, in); return e }))
}

func TestReminderWorkAnchorUsesRecipientHeadAfterCallerCheck(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	var checked []guid.ID
	m.reminderWork = delivery.ReminderWorkAccess{CheckOn: func(_ context.Context, _ *gorm.DB, _, _, _, actor guid.ID) (int64, error) {
		checked = append(checked, actor)
		return int64(actor) * 10, nil
	}}
	seq, err := m.reminderAnchorOn(ctx, db, 1, 7, &model.Reminder{AnchorKind: "WORK_ITEM", WorkItemID: new(guid.ID(44)), ProjectID: new(guid.ID(3))})
	require.NoError(t, err)
	require.EqualValues(t, 70, seq)
	require.Equal(t, []guid.ID{2, 7}, checked)
}

func TestReminderCreationScheduleSourcesAndAuditFailuresNeverConsumeQuota(t *testing.T) {
	for _, mode := range []string{"anchor", "schedule", "missing-calendar", "calendar", "audit", "quota-query", "write"} {
		t.Run(mode, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 7)
			fault := errors.New("reminder source unavailable")
			project := guid.ID(3)
			in := ReminderInput{ProjectID: &project, ThreadID: 4, Title: "Review source", DueAt: m.now().Add(time.Hour), MaxFires: 1}
			switch mode {
			case "anchor":
				in.AnchorKind = "UNKNOWN"
			case "schedule":
				in.ScheduleKind = "UNKNOWN"
			case "missing-calendar", "calendar":
				in.ScheduleKind = "PERIOD"
				in.PeriodKind = "DAY"
				in.DueAt = time.Time{}
				if mode == "calendar" {
					m.reminderCalendar = func(context.Context, *gorm.DB, guid.ID, string, time.Time) (time.Time, string, error) {
						return time.Time{}, "", fault
					}
				}
			case "audit":
				m.audit = nil
			case "quota-query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reminder-storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Reminder.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("reminder-storage")
			case "write":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reminder-storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Reminder.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("reminder-storage")
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.reminderOn(ctx, tx, 1, 7, in); return e })
			require.Error(t, err)
			if mode == "quota-query" {
				require.NoError(t, db.Callback().Query().Remove("reminder-storage"))
			}
			count, e := query.BindReminder(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestReminderCalendarConnectorRejectsReplacementAndPersistsProviderDeadline(t *testing.T) {
	m, db, _ := agentHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideReminderCalendar(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ReminderCalendarConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrConflict)
	due := m.now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)
	calendar := ReminderCalendar(func(context.Context, *gorm.DB, guid.ID, string, time.Time) (time.Time, string, error) {
		return due, "UTC", nil
	})
	require.NoError(t, connector.Bind(calendar))
	require.ErrorIs(t, connector.Bind(calendar), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, e := m.reminderOn(agentContext(t, 7), tx, 1, 7, ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Actual period", ScheduleKind: "PERIOD", PeriodKind: "DAY", MaxFires: 2})
		require.NoError(t, e)
		require.Equal(t, due, view.DueAt)
		return nil
	}))
}
