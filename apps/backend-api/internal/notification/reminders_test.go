package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSourceReminderMissingSourcesAndInvalidAudienceFailClosed(t *testing.T) {
	f := newFixture(t)
	f.m.spawnFacts = nil
	f.m.decisionFacts.ReviewSheetOn = nil
	f.m.incidentFacts = nil
	f.m.decisionFacts.DeadlineOn = nil
	for _, kind := range []string{"review_age", "project_blocked", "spawn", "review", "triage", "interaction", "incident", "unknown"} {
		fact, err := f.m.sourceReminderFactOn(t.Context(), f.db, 7, sourceReminder{Kind: kind, SourceID: 91, Ordinal: 1})
		require.Error(t, err)
		require.False(t, fact.active)
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91","ordinal":"0"}`, `{"subject_id":"91","ordinal":"100001"}`} {
		yes, err := f.m.sourceReminderAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Params: raw}, 11)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`} {
		yes, err := f.m.deadlineAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Params: raw}, 11)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestSourceReminderSchedulingAndJobFailuresRollbackInbox(t *testing.T) {
	for _, mode := range []string{"queue_missing", "admission", "queue", "payload", "missing_candidates", "database", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.triageReminder = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
				if mode == "source" {
					return 0, 0, time.Time{}, false, ErrUnavailable
				}
				return 81, 80, f.m.now(), true, nil
			}
			f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				return []guid.ID{11}, nil
			}
			switch mode {
			case "queue_missing":
				f.m.queue = nil
			case "admission":
				f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "queue":
				f.m.queue = &fixtureQueue{err: ErrUnavailable}
			case "missing_candidates":
				f.m.candidates = nil
			case "database":
				f.m.runtime = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			var err error
			if mode == "queue_missing" || mode == "admission" || mode == "queue" {
				err = f.db.Transaction(func(tx *gorm.DB) error {
					return f.m.scheduleSourceReminderOn(t.Context(), tx, 7, sourceReminder{Kind: "triage", SourceID: 91, Ordinal: 1})
				})
			} else {
				raw := []byte(`{"kind":"triage","source_id":"91","ordinal":1}`)
				if mode == "payload" {
					raw = []byte(`{}`)
				}
				err = f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw})
			}
			require.Error(t, err)
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestSourceReminderDeadlineReplayCurrentAudienceAndClosure(t *testing.T) {
	for _, kind := range []string{"triage", "interaction", "incident"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			at := f.m.now()
			active := true
			f.m.triageReminder = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
				return 81, 80, at, active, nil
			}
			f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				return []guid.ID{11}, nil
			}
			f.m.interactionReminder = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
				return 92, 81, at, active, nil
			}
			f.m.interactionFacts = func(_ context.Context, _ *gorm.DB, _, _, recipient guid.ID) (guid.ID, guid.ID, bool, error) {
				return 92, 81, active && recipient == 11, nil
			}
			f.m.incidentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.IncidentNotificationFact, bool, error) {
				state := "OPEN"
				if !active {
					state = "RESOLVED"
				}
				return governance.IncidentNotificationFact{ProjectID: 81, State: state, CreatedAt: at.Add(-24 * time.Hour)}, true, nil
			}
			in := sourceReminder{Kind: kind, SourceID: 91, Ordinal: 1}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.scheduleSourceReminderOn(t.Context(), tx, 7, in) }))
			queue := f.m.queue.(*fixtureQueue)
			require.Len(t, queue.requests, 1)
			require.Equal(t, at, queue.requests[0].RunAt)
			raw, err := json.Marshal(in)
			require.NoError(t, err)
			job := jobs.Job{TenantID: 7, Payload: raw}
			f.m.now = func() time.Time { return at.Add(-time.Nanosecond) }
			require.Error(t, f.m.sourceReminderJob(t.Context(), job))
			q := query.BindNotifications(f.db, f.m.names)
			count, err := q.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			f.m.now = func() time.Time { return at }
			require.NoError(t, f.m.sourceReminderJob(t.Context(), job))
			require.NoError(t, f.m.sourceReminderJob(t.Context(), job))
			rows, err := q.WithContext(t.Context()).Find()
			require.NoError(t, err)
			expected := 1
			if kind == "incident" {
				expected = 2
			}
			require.Len(t, rows, expected)
			for _, row := range rows {
				require.Equal(t, 1, row.MergeCount)
				for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
					_, err := view(i18n.WithLocale(t.Context(), locale), row)
					require.NoError(t, err)
				}
			}
			if kind == "incident" {
				require.Equal(t, at.Add(24*time.Hour), queue.requests[1].RunAt)
			}
			active = false
			before := len(queue.requests)
			require.NoError(t, f.m.sourceReminderJob(t.Context(), job))
			require.Len(t, queue.requests, before)
			for _, row := range rows {
				yes, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
				require.NoError(t, err)
				require.False(t, yes)
			}
		})
	}
}

func TestSourceReminderEnqueueFailureRollsBackAndUnknownKindsFail(t *testing.T) {
	f := newFixture(t)
	at := f.m.now()
	f.m.triageReminder = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
		return 81, 80, at, true, nil
	}
	sentinel := errors.New("queue unavailable")
	f.m.queue.(*fixtureQueue).err = sentinel
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		return f.m.scheduleSourceReminderOn(t.Context(), tx, 7, sourceReminder{Kind: "triage", SourceID: 91, Ordinal: 1})
	}), sentinel)
	require.ErrorIs(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: []byte(`{"kind":"invented","source_id":"91","ordinal":1}`)}), ErrInvalid)
}

func TestReviewAndBlockedDeadlineNoticesUseCurrentSourceEpisode(t *testing.T) {
	for _, kind := range []string{"review_age", "project_blocked"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			at := f.m.now()
			changed := at
			active := true
			f.m.decisionFacts.DeadlineOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID, k string) (delivery.DeadlineNotificationFact, bool, error) {
				require.Equal(t, kind, k)
				return delivery.DeadlineNotificationFact{ProjectID: 9, Active: active, ChangedAt: changed}, true, nil
			}
			f.m.workFacts.ReviewEligibleOn = func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID) (bool, error) { return actor == 12, nil }
			f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: r.PrincipalID == 12, Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
			}
			in := sourceReminder{Kind: kind, SourceID: 91, Ordinal: 1}
			require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.scheduleSourceReminderOn(t.Context(), tx, 7, in) }))
			queue := f.m.queue.(*fixtureQueue)
			require.Len(t, queue.requests, 1)
			if kind == "review_age" {
				require.Equal(t, at.Add(24*time.Hour), queue.requests[0].RunAt)
			} else {
				require.Equal(t, at.Add(72*time.Hour), queue.requests[0].RunAt)
			}
			raw, err := json.Marshal(queue.requests[0].Payload)
			require.NoError(t, err)
			require.Error(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}), "must not notify early")
			now := queue.requests[0].RunAt
			f.m.now = func() time.Time { return now }
			for range 2 {
				require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}))
			}
			q := query.BindNotifications(f.db, f.m.names)
			rows, err := q.WithContext(t.Context()).Find()
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
			require.Len(t, queue.requests, 1)
			changed = at.Add(time.Minute)
			yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
			require.NoError(t, err)
			require.False(t, yes, "old episode cannot survive re-entry")
			active = false
			require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}))
		})
	}
	require.Equal(t, "source-reminder:triage:91:1", sourceReminderIdentity(sourceReminder{Kind: "triage", SourceID: 91, Ordinal: 1}), "preserve existing replay identities")
}
