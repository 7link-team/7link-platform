package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestReminderCalendarConnectorUsesActualTeamSettingsAndDST(t *testing.T) {
	h := loopHarness(t)
	s := module.NewServices()
	var calendar agents.ReminderCalendar
	require.NoError(t, module.Provide[agents.ReminderCalendarConnector](s, agents.ReminderCalendarConnector{Bind: func(fn agents.ReminderCalendar) error { calendar = fn; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, h.m.injectReminderCalendar(s))
	// Default team timezone and report clock are read from the same settings source as loops.
	after := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	due, zone, e := calendar(t.Context(), h.db, 1, "DAY", after)
	require.NoError(t, e)
	require.Equal(t, "Asia/Shanghai", zone)
	require.Equal(t, 10, due.Hour())
	settings := defaultPlanningSettings()
	settings.Timezone = "America/Los_Angeles"
	settings.DayReportAt = "09:00"
	before := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC)
	due, ok := nextScheduledReportAt("DAY", settings, before)
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 11, 2, 17, 0, 0, 0, time.UTC), due.UTC())
	_, _, e = calendar(t.Context(), h.db, 1, "CRON", after)
	require.ErrorIs(t, e, ErrInvalid)
}
