package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentErrorDelayReentryRecoveryAndCurrentOwner(t *testing.T) {
	f := newFixture(t)
	now := f.m.now()
	change := agents.ActivityTransition{TenantID: 7, AgentPrincipalID: 91, ResponsiblePrincipalID: 11, Activity: "ERROR", SessionEpoch: 2, OccurredAt: now}
	current := agents.ActivitySnapshot{TenantID: 7, AgentPrincipalID: 91, ResponsiblePrincipalID: 12, Name: "Builder", Activity: "ERROR", SessionEpoch: 2}
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return current, true, nil
	}
	observe := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }))
	}
	observe()
	queue := f.m.queue.(*fixtureQueue)
	require.Len(t, queue.requests, 1)
	require.Equal(t, now.Add(10*time.Minute), queue.requests[0].RunAt)
	job := func(i int) jobs.Job {
		payload, err := json.Marshal(queue.requests[i].Payload)
		require.NoError(t, err)
		return jobs.Job{TenantID: 7, Payload: payload}
	}
	require.Error(t, f.m.agentErrorJob(t.Context(), job(0)))
	f.m.now = func() time.Time { return now.Add(11 * time.Minute) }
	// A later ERROR entry has its own full delay and invalidates the first job.
	change.OccurredAt = now.Add(5 * time.Minute)
	observe()
	require.NoError(t, f.m.agentErrorJob(t.Context(), job(0)))
	require.Error(t, f.m.agentErrorJob(t.Context(), job(1)))
	f.m.now = func() time.Time { return now.Add(16 * time.Minute) }
	require.NoError(t, f.m.agentErrorJob(t.Context(), job(1)))
	require.NoError(t, f.m.agentErrorJob(t.Context(), job(1)))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(12), rows[0].RecipientPrincipalID)
	v, err := view(t.Context(), rows[0])
	require.NoError(t, err)
	require.Contains(t, v.Body, "Builder")
	visible, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.True(t, visible)
	current.ResponsiblePrincipalID = 11
	visible, err = f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, visible)
	change.Activity = "IDLE"
	observe()
	change.Activity = "ERROR"
	change.OccurredAt = now
	observe()
	current.Activity = "IDLE"
	require.NoError(t, f.m.agentErrorJob(t.Context(), job(2)))
	current.Activity = "ERROR"
	current.SessionEpoch++
	require.NoError(t, f.m.agentErrorJob(t.Context(), job(2)))
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestAgentErrorAtomicityAndFailures(t *testing.T) {
	f := newFixture(t)
	change := agents.ActivityTransition{TenantID: 7, AgentPrincipalID: 91, Activity: "ERROR", SessionEpoch: 2, OccurredAt: f.m.now().Add(-11 * time.Minute)}
	sentinel := errors.New("queue unavailable")
	queue := f.m.queue.(*fixtureQueue)
	queue.err = sentinel
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }), sentinel)
	q := query.BindAgentErrors(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	queue.err = nil
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }))
	payload, err := json.Marshal(queue.requests[0].Payload)
	require.NoError(t, err)
	job := jobs.Job{TenantID: 7, Payload: payload}
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{}, false, sentinel
	}
	require.ErrorIs(t, f.m.agentErrorJob(t.Context(), job), sentinel)
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{Activity: "ERROR", SessionEpoch: 2, ResponsiblePrincipalID: 11}, true, nil
	}
	f.err = sentinel
	require.ErrorIs(t, f.m.agentErrorJob(t.Context(), job), sentinel)
	row, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.False(t, row.Notified)
	f.err = nil
	require.NoError(t, f.m.agentErrorJob(t.Context(), job))
	row, err = q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.True(t, row.Notified)
	for _, invalid := range []jobs.Job{{}, {TenantID: 7, Payload: json.RawMessage(`{}`)}, {TenantID: 7, Payload: json.RawMessage(`bad`)}} {
		require.Error(t, f.m.agentErrorJob(t.Context(), invalid))
	}
	require.ErrorIs(t, f.m.activityChangedOn(t.Context(), f.db, change), ErrInvalid)
}

func TestAgentErrorAdmissionAndMissingCurrentStateNeverNotify(t *testing.T) {
	f := newFixture(t)
	change := agents.ActivityTransition{TenantID: 7, AgentPrincipalID: 91, Activity: "ERROR", SessionEpoch: 2, OccurredAt: f.m.now().Add(-time.Hour)}
	sentinel := errors.New("admission unavailable")
	f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return sentinel }
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }), sentinel)
	f.m.admission = nil
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }), ErrUnavailable)
	f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	f.ids.err = sentinel
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }), sentinel)
	f.ids.err = nil
	f.ids.zero = true
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }), ErrInvalid)
	f.ids.zero = false
	payload, _ := json.Marshal(agentErrorInput{AgentID: 91, EntryID: 1})
	job := jobs.Job{TenantID: 7, Payload: payload}
	require.NoError(t, f.m.agentErrorJob(t.Context(), job))
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }))
	payload, _ = json.Marshal(f.m.queue.(*fixtureQueue).requests[0].Payload)
	job.Payload = payload
	require.NoError(t, f.m.agentErrorJob(t.Context(), job)) // snapshot missing
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{Activity: "ERROR", SessionEpoch: 2}, true, nil
	}
	require.NoError(t, f.m.agentErrorJob(t.Context(), job)) // no current owner
	change.Activity = "IDLE"
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.activityChangedOn(t.Context(), tx, change) }))
	require.NoError(t, f.m.agentErrorJob(t.Context(), job))
	f.m.activity.SnapshotOn = nil
	require.ErrorIs(t, f.m.agentErrorJob(t.Context(), job), ErrUnavailable)
	f.m.activity.SnapshotOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (agents.ActivitySnapshot, bool, error) {
		return agents.ActivitySnapshot{}, false, nil
	}
	f.m.runtime = nil
	require.ErrorIs(t, f.m.agentErrorJob(t.Context(), job), ErrUnavailable)
	q := query.BindNotifications(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}
