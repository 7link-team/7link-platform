package notification

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReviewSheetProjectionAndAudienceFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "source", "visibility", "notify", "schedule"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			due := f.m.now().Add(time.Hour)
			fact := delivery.ReviewSheetNotificationFact{ProjectID: 81, State: "OPEN", Reviewers: []guid.ID{11}}
			if mode == "schedule" {
				fact.DueAt = &due
			}
			f.m.decisionFacts.ReviewSheetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ReviewSheetNotificationFact, bool, error) {
				if mode == "source" {
					return fact, false, ErrUnavailable
				}
				return fact, true, nil
			}
			f.m.decisionFacts.ReviewSheetEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return true, nil }
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "review_sheet", Type: "review_sheet.opened"}
			switch mode {
			case "identity":
				e.ID = 0
			case "missing":
				f.m.decisionFacts.ReviewSheetOn = nil
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			case "schedule":
				f.m.queue = &fixtureQueue{err: ErrUnavailable}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectReviewSheetOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	f.m.decisionFacts.ReviewSheetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ReviewSheetNotificationFact, bool, error) {
		return delivery.ReviewSheetNotificationFact{ProjectID: 81}, true, nil
	}
	f.m.decisionFacts.ReviewSheetEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) { return true, nil }
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91","ordinal":"bad"}`, `{"subject_id":"91","ordinal":"2"}`, `{"subject_id":"91","ordinal":"1"}`} {
		yes, err := f.m.reviewSheetAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, ProjectID: 81, Type: "review_sheet.due", Params: raw}, 11)
		require.False(t, yes)
		if raw == `{"subject_id":"91","ordinal":"2"}` || raw == `{"subject_id":"91","ordinal":"1"}` {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
	f.m.decisionFacts.ReviewSheetEligibleOn = nil
	yes, err := f.m.reviewSheetAudienceOn(t.Context(), f.db, &model.Notification{}, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestReviewSheetCurrentPendingAudienceDeadlineReplayAndClosure(t *testing.T) {
	f := newFixture(t)
	at := f.m.now()
	due := at.Add(24 * time.Hour)
	state := "OPEN"
	submitted := false
	f.m.decisionFacts.ReviewSheetOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ReviewSheetNotificationFact, bool, error) {
		return delivery.ReviewSheetNotificationFact{ProjectID: 81, InitiatorID: 12, State: state, DueAt: &due, Reviewers: []guid.ID{11}}, true, nil
	}
	f.m.decisionFacts.ReviewSheetEligibleOn = func(_ context.Context, _ *gorm.DB, _, _, actor guid.ID, typ string) (bool, error) {
		if typ == "review_sheet.rejected" {
			return state == "REJECTED" && actor == 12, nil
		}
		return state == "OPEN" && actor == 11 && !submitted, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "review_sheet", Type: "review_sheet.opened", Data: []byte(`{}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	queue := f.m.queue.(*fixtureQueue)
	require.Equal(t, at, queue.requests[len(queue.requests)-1].RunAt)
	raw, err := json.Marshal(sourceReminder{Kind: "review", SourceID: 91, Ordinal: 1})
	require.NoError(t, err)
	requestsBefore := len(queue.requests)
	require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}))
	require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw}))
	require.Len(t, queue.requests, requestsBefore, "review deadline reminder is once per sheet, never a daily recurrence")
	obsolete, err := json.Marshal(sourceReminder{Kind: "review", SourceID: 91, Ordinal: 2})
	require.NoError(t, err)
	require.NoError(t, f.m.sourceReminderJob(t.Context(), jobs.Job{TenantID: 7, Payload: obsolete}))
	rows, err = q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	submitted = true
	for _, row := range rows {
		yes, err := f.m.visibleOn(t.Context(), f.db, row, 11)
		require.NoError(t, err)
		require.False(t, yes)
	}
	state = "REJECTED"
	e.Type = "review_sheet.closed"
	e.ID++
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("review_sheet.rejected")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].RecipientPrincipalID)
	f.allow = false
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 12)
	require.NoError(t, err)
	require.False(t, yes)
}
