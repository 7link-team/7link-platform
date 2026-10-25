package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestCalendarLoopActivationAndActualFireCreateNextReminderAtomically(t *testing.T) {
	h := loopHarness(t)
	def := testLoopDefinition()
	kind := "DAY"
	def.ScheduleKind = "PERIOD"
	def.IntervalMinutes = nil
	def.PeriodKind = &kind
	next := guid.ID(500)
	var due time.Time
	fail := false
	h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
		require.Zero(t, in.IntervalMinutes)
		require.NotNil(t, in.DueAt)
		require.True(t, in.DueAt.After(h.m.now()))
		due = *in.DueAt
		next++
		if e := tx.Exec("INSERT INTO scheduler_probe VALUES (?,?)", next, "ACTIVE").Error; e != nil {
			return 0, e
		}
		if fail {
			return 0, errors.New("scheduler unavailable")
		}
		return next, nil
	}
	var contract LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		contract, e = h.m.createLoopOn(claims(t, 2), tx, 1, def, true)
		return e
	}))
	require.Equal(t, guid.ID(501), *contract.ReminderID)
	at := due
	h.m.now = func() time.Time { return at }
	fail = true
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 501, 1, at); return e }))
	var n int64
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Count(&n).Error)
	require.Zero(t, n)
	fail = false
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		skip, e := h.m.openLoopReminderOn(t.Context(), tx, 1, 501, 1, at)
		require.False(t, skip)
		return e
	}))
	c, e := h.m.loopOn(t.Context(), h.db, 1, contract.ID)
	require.NoError(t, e)
	require.Equal(t, guid.ID(503), *c.ReminderID)
	require.Equal(t, 1, c.ReminderFireOffset)
	require.True(t, due.After(at))
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
}
