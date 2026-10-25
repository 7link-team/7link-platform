package performance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPeriodWindowCalendarBoundsAndExclusiveCutoff(t *testing.T) {
	for _, tc := range []struct {
		name, cadence, zone, anchor, start, last, end, cutoff string
	}{
		{"week_cross_year", "WEEK", "UTC", "2027-01-01T12:00:00Z", "2026-12-28", "2027-01-03", "2027-01-04T00:00:00Z", "2027-01-03T00:00:00Z"},
		{"leap_month", "MONTH", "Asia/Singapore", "2024-02-12T12:00:00Z", "2024-02-01", "2024-02-29", "2024-02-29T16:00:00Z", "2024-02-28T16:00:00Z"},
		{"quarter", "QUARTER", "UTC", "2026-11-22T12:00:00Z", "2026-10-01", "2026-12-31", "2027-01-01T00:00:00Z", "2026-12-31T00:00:00Z"},
		{"year", "YEAR", "UTC", "2024-02-29T12:00:00Z", "2024-01-01", "2024-12-31", "2025-01-01T00:00:00Z", "2024-12-31T00:00:00Z"},
		{"local_date", "MONTH", "Asia/Tokyo", "2026-12-31T16:00:00Z", "2027-01-01", "2027-01-31", "2027-01-31T15:00:00Z", "2027-01-30T15:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor, err := time.Parse(time.RFC3339, tc.anchor)
			require.NoError(t, err)
			window, err := periodWindow(tc.cadence, tc.zone, anchor)
			require.NoError(t, err)
			require.Equal(t, tc.start, window.PeriodStart)
			require.Equal(t, tc.last, window.PeriodEnd)
			require.Equal(t, tc.end, window.EndAt.Format(time.RFC3339))
			require.Equal(t, tc.cutoff, window.CutoffAt.Format(time.RFC3339))
			require.Equal(t, window.CutoffAt, window.TriggerAt)
			require.Equal(t, window.CutoffAt, window.UncoveredStartAt)
			require.Equal(t, window.EndAt, window.UncoveredEndAt)
			require.True(t, window.StartAt.Before(window.CutoffAt))
			require.True(t, window.CutoffAt.Before(window.EndAt))
			require.Equal(t, time.UTC, window.StartAt.Location())
		})
	}
}

func TestPeriodWindowDaylightSavingUsesCalendarDays(t *testing.T) {
	for _, tc := range []struct {
		anchor string
		hours  int
	}{
		{"2026-03-08T12:00:00Z", 167},
		{"2026-11-01T12:00:00Z", 169},
	} {
		anchor, err := time.Parse(time.RFC3339, tc.anchor)
		require.NoError(t, err)
		window, err := periodWindow("WEEK", "America/New_York", anchor)
		require.NoError(t, err)
		require.Equal(t, time.Duration(tc.hours)*time.Hour, window.EndAt.Sub(window.StartAt))
		require.Equal(t, time.Duration(tc.hours-144)*time.Hour, window.UncoveredEndAt.Sub(window.UncoveredStartAt))
	}
}

func TestNextPeriodWindowBeforeAtAndAfterTrigger(t *testing.T) {
	for _, cadence := range []string{"WEEK", "MONTH", "QUARTER", "YEAR"} {
		t.Run(cadence, func(t *testing.T) {
			window, err := periodWindow(cadence, "Asia/Singapore", time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
			require.NoError(t, err)
			before, err := nextPeriodWindow(cadence, window.Timezone, window.TriggerAt.Add(-time.Nanosecond))
			require.NoError(t, err)
			require.Equal(t, window, before)
			for _, now := range []time.Time{window.TriggerAt, window.TriggerAt.Add(time.Hour), window.EndAt} {
				next, err := nextPeriodWindow(cadence, window.Timezone, now)
				require.NoError(t, err)
				require.Equal(t, window.EndAt, next.StartAt)
				require.True(t, next.TriggerAt.After(now))
			}
		})
	}
}

func TestPeriodWindowRejectsInvalidInputsAndMissingMidnights(t *testing.T) {
	valid := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		cadence, zone string
		anchor        time.Time
	}{
		{"DAY", "UTC", valid}, {"week", "UTC", valid}, {"", "UTC", valid},
		{"WEEK", "", valid}, {"WEEK", "Local", valid}, {"WEEK", "Not/AZone", valid},
		{"WEEK", "UTC", time.Time{}},
		{"YEAR", "UTC", time.Date(0, 5, 1, 0, 0, 0, 0, time.UTC)},
		{"YEAR", "UTC", time.Date(9999, 5, 1, 0, 0, 0, 0, time.UTC)},
		// Cairo's April 30 cutoff skipped midnight in 2010.
		{"MONTH", "Africa/Cairo", time.Date(2010, 4, 15, 12, 0, 0, 0, time.UTC)},
		// Havana's April 1 period start skipped midnight in 2001.
		{"MONTH", "America/Havana", time.Date(2001, 4, 15, 12, 0, 0, 0, time.UTC)},
		// The preceding period must also reject that nonexistent end boundary.
		{"MONTH", "America/Havana", time.Date(2001, 3, 15, 12, 0, 0, 0, time.UTC)},
	} {
		_, err := periodWindow(tc.cadence, tc.zone, tc.anchor)
		require.ErrorIs(t, err, ErrInvalid)
		_, err = nextPeriodWindow(tc.cadence, tc.zone, tc.anchor)
		require.ErrorIs(t, err, ErrInvalid)
	}
	loc, err := time.LoadLocation("Pacific/Apia")
	require.NoError(t, err)
	_, err = periodMidnight(time.Date(2011, 12, 30, 0, 0, 0, 0, time.UTC), loc)
	require.ErrorIs(t, err, ErrInvalid, "the skipped calendar date cannot normalize to another day")
}
