package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestLoopReminderCapabilityUpdatesCurrentVersionAndTerminalState(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	s := module.NewServices()
	require.NoError(t, m.provideLoopReminders(s))
	require.Error(t, m.provideLoopReminders(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[ReminderLoops](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrConflict)
	require.NoError(t, c.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error) { return false, nil }))
	require.ErrorIs(t, c.Bind(m.loopObserver), ErrConflict)
	now := m.now()
	m.now = func() time.Time { return now }
	in := LoopReminderInput{AgentID: 7, ProjectID: new(guid.ID(3)), ThreadID: 4, Name: "Loop", IntervalMinutes: 15}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		id, e = c.UpsertOn(ctx, tx, 1, in)
		return e
	}))
	q := query.BindReminder(db, m.names)
	in.ReminderID, in.Name, in.IntervalMinutes = id, "Changed loop", 30
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		updated, e := c.UpsertOn(ctx, tx, 1, in)
		require.Equal(t, id, updated)
		return e
	}))
	r, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "Changed loop", r.Title)
	require.Equal(t, 2, r.Version)
	require.Equal(t, now.Add(30*time.Minute), *r.NextFireAt)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return c.ChangeOn(ctx, tx, 1, 7, id, "PAUSED") }))
	r, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "PAUSED", r.State)
	require.Equal(t, 3, r.Version)
	sentinel := errors.New("caller rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, c.ChangeOn(ctx, tx, 1, 7, id, "ACTIVE"))
		return sentinel
	}), sentinel)
	r, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "PAUSED", r.State)
	require.Equal(t, 3, r.Version)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return c.ChangeOn(ctx, tx, 1, 7, id, reminderCancelled) }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := c.UpsertOn(ctx, tx, 1, in); return e }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return c.ChangeOn(ctx, tx, 1, 7, id, "ACTIVE") }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return c.ChangeOn(ctx, tx, 1, 7, 0, "PAUSED") }), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return c.ChangeOn(ctx, tx, 1, 7, id+1, "PAUSED") }), gorm.ErrRecordNotFound)
	in.ReminderID = id + 1
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := c.UpsertOn(ctx, tx, 1, in); return e }), gorm.ErrRecordNotFound)
}

func TestLoopReminderRejectsNontransactionAndCurrentAccessRevocation(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	in := LoopReminderInput{AgentID: 7, ProjectID: new(guid.ID(3)), ThreadID: 4, Name: "Loop", IntervalMinutes: 15}
	_, err := m.upsertLoopReminderOn(ctx, nil, 1, in)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.upsertLoopReminderOn(ctx, db, 1, in)
	require.ErrorIs(t, err, ErrInvalid)
	m.loopObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error) { return false, nil }
	in.IntervalMinutes = 14
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(ctx, tx, 1, in); return e }), ErrInvalid)
	in.IntervalMinutes = 15
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(agentContext(t, 9), tx, 1, in); return e }), ErrForbidden)
	in.ThreadID = 99
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(ctx, tx, 1, in); return e }), ErrForbidden)
	in.ThreadID = 4
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(ctx, tx, 1, in); return e }), ErrUnavailable)
	m.loopObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error) {
		return false, gorm.ErrRecordNotFound
	}
	skip, err := m.observeLoopFireOn(ctx, db, 1, 7, 1, m.now())
	require.NoError(t, err)
	require.False(t, skip)
}

func TestLoopReminderObserverSharesFireRollbackAndSkip(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	in := LoopReminderInput{AgentID: 7, ProjectID: new(guid.ID(3)), ThreadID: 4, Name: "Periodic check", IntervalMinutes: 15}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(ctx, tx, 1, in); return e }), ErrUnavailable)
	require.NoError(t, db.Exec("CREATE TABLE loop_probe (fire INT NOT NULL)").Error)
	failure := true
	skip := false
	m.loopObserver = func(_ context.Context, tx *gorm.DB, tenant, id guid.ID, fire int, _ time.Time) (bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Positive(t, id)
		if e := tx.Exec("INSERT INTO loop_probe VALUES (?)", fire).Error; e != nil {
			return false, e
		}
		if failure {
			return false, errors.New("observer failure")
		}
		return skip, nil
	}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; id, e = m.upsertLoopReminderOn(ctx, tx, 1, in); return e }))
	now = now.Add(16 * time.Minute)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, id, now) }))
	q := query.BindReminder(db, m.names)
	r, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Zero(t, r.FireCount)
	var count int64
	require.NoError(t, db.Table("loop_probe").Count(&count).Error)
	require.Zero(t, count)
	failure = false
	skip = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, id, now) }))
	require.NoError(t, db.Table("loop_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	inbox := query.BindInboxEvent(db, m.names)
	count, e = inbox.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
	now = now.Add(15 * time.Minute)
	skip = false
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, id, now) }))
	count, e = inbox.WithContext(ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, count)
}
func TestCalendarLoopReminderPersistsExactDueAndFiresOnce(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.loopObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error) { return false, nil }
	due := now.Add(24 * time.Hour)
	in := LoopReminderInput{AgentID: 7, ProjectID: new(guid.ID(3)), ThreadID: 4, Name: "Calendar loop", DueAt: &due}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; id, e = m.upsertLoopReminderOn(ctx, tx, 1, in); return e }))
	q := query.BindReminder(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, due, *row.NextFireAt)
	require.Equal(t, 1, row.MaxFires)
	require.Zero(t, row.IntervalMinutes)
	now = due
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, id, now) }))
	row, e = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, "DONE", row.State)
	require.Equal(t, 1, row.FireCount)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, id, now) }))
	row, e = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, e)
	require.Equal(t, 1, row.FireCount)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.upsertLoopReminderOn(ctx, tx, 1, in); return e }), ErrInvalid)
}
func TestTeamLoopReminderPersistsNullScopeAndRechecksOnFire(t *testing.T) {
	m, db, _ := agentHarness(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	allowed := true
	m.threads.CheckOn = func(_ context.Context, _ *gorm.DB, tenant, project, thread, target guid.ID) (int64, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Zero(t, project, "team thread scope is never an entity")
		require.Equal(t, guid.ID(4), thread)
		require.Equal(t, guid.ID(7), target)
		if !allowed {
			return 0, ErrForbidden
		}
		return 3, nil
	}
	fires := 0
	m.loopObserver = func(context.Context, *gorm.DB, guid.ID, guid.ID, int, time.Time) (bool, error) {
		fires++
		return false, nil
	}
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.upsertLoopReminderOn(agentContext(t, 2), tx, 1, LoopReminderInput{AgentID: 7, ThreadID: 4, Name: "Team check", IntervalMinutes: 15})
		return err
	}))
	q := query.BindReminder(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Nil(t, row.ProjectID)
	allowed = false
	now = now.Add(15 * time.Minute)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(t.Context(), tx, 1, id, now) }), ErrForbidden)
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Zero(t, row.FireCount)
	require.Zero(t, fires)
	allowed = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(t.Context(), tx, 1, id, now) }))
	require.Equal(t, 1, fires)
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Nil(t, row.ProjectID)
	require.Equal(t, 1, row.FireCount)
	sentinel := errors.New("rollback team pause")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.changeLoopReminderOn(agentContext(t, 2), tx, 1, 7, id, "PAUSED"))
		return sentinel
	}), sentinel)
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
}
