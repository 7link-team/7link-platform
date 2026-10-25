package notification

import (
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCatalogDefaultEmailAndExplicitMergeWindows(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct{ typ, email string }{
		{"machine.offline", "IMMEDIATE"}, {"machine.recovered", "IMMEDIATE"}, {"work_item.assigned", "DAILY_DIGEST"}, {"requirement.versioned", "DAILY_DIGEST"}, {"memory.rule_pending", "DAILY_DIGEST"},
		{"machine.revoked", "IMMEDIATE"}, {"machine.rotation_failed", "IMMEDIATE"}, {"execution.failed_exhausted", "IMMEDIATE"}, {"execution.interrupted", "IMMEDIATE"}, {"execution.cost_paused", "IMMEDIATE"}, {"governance.escalation_triggered", "IMMEDIATE"}, {"budget.threshold", "IMMEDIATE"}, {"fleet.agent_error_persisting", "IMMEDIATE"}, {"jobs.dead_lettered", "IMMEDIATE"}, {"events.parked", "IMMEDIATE"},
		{"work_item.review_ready", "OFF"}, {"work_item.rework", "OFF"}, {"work_item.accepted", "OFF"}, {"project.stage_changed", "OFF"}, {"channel.mentioned", "OFF"},
	} {
		preference, err := f.m.preferenceOn(t.Context(), f.db, 11, 7, test.typ)
		require.NoError(t, err)
		require.Equal(t, test.email, preference.Email, test.typ)
		enabled, due, err := f.m.externalPreferenceOn(t.Context(), f.db, 11, 7, test.typ, Info)
		require.NoError(t, err)
		require.Equal(t, test.email != "OFF", enabled, test.typ)
		if test.email == "DAILY_DIGEST" {
			require.True(t, due.After(f.m.now()), test.typ)
		} else {
			require.True(t, due.IsZero(), test.typ)
		}
	}
	for _, test := range []struct {
		typ           string
		before, after time.Duration
	}{
		{"machine.offline", 4 * time.Minute, 6 * time.Minute}, {"memory.rule_pending", 29 * time.Minute, 31 * time.Minute}, {"execution.cost_paused", 29 * time.Minute, 31 * time.Minute}, {"channel.mentioned", time.Minute, 3 * time.Minute}, {"work_item.review_ready", 4 * time.Minute, 6 * time.Minute},
	} {
		n := sample()
		n.Type = test.typ
		n.Severity = Info
		n.Channels = []ChannelName{}
		n.DedupeKey = test.typ
		start := f.m.now()
		first := f.notify(t, n)
		f.m.now = func() time.Time { return start.Add(test.before) }
		require.Equal(t, first, f.notify(t, n))
		f.m.now = func() time.Time { return start.Add(test.after) }
		require.NotEqual(t, first, f.notify(t, n))
	}
	// A frozen once-per-machine condition stays deduplicated after read and time.
	n := sample()
	n.Type = "machine.revoked"
	n.Severity = Info
	n.Channels = []ChannelName{}
	n.DedupeKey = "revoked:91"
	first := f.notify(t, n)
	q := query.BindNotifications(f.db, f.m.names)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(first[0])).Update(q.ReadAt, f.m.now())
	require.NoError(t, err)
	start := f.m.now()
	f.m.now = func() time.Time { return start.Add(48 * time.Hour) }
	require.Equal(t, first, f.notify(t, n))
}
