package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func timedClarification(t *testing.T) (*Module, *gorm.DB, Receipt, time.Time) {
	t.Helper()
	m, db, _, _ := clarificationHarness(t)
	m.intakeSources.TimingOn = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID) (intake.ClarificationTiming, bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, guid.ID(50), id)
		return intake.ClarificationTiming{ChannelID: 3, ChannelVersion: 7, ReminderHours: 48, TimeoutHours: 168, MaxReminders: 2}, true, nil
	}
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	q := query.BindClarificationRound(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(opened.Body.Data.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, 7, *row.ChannelVersion)
	require.Equal(t, row.CreatedAt.Add(168*time.Hour), *row.DueAt)
	return m, db, opened.Body.Data, row.CreatedAt
}

func TestClarificationTimingRemindersExpiryLateAnswerAndNewRound(t *testing.T) {
	m, db, opened, at := timedClarification(t)
	ctx := testContext(t, 2)
	var observed []events.Event
	original := m.events.Publish
	m.events = expiryPublisher{Publisher: m.events, publish: func(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		observed = append(observed, e)
		return original(ctx, tx, e)
	}}
	require.NoError(t, m.advanceClarifications(ctx, 2, at.Add(49*time.Hour)))
	require.Empty(t, observed)
	require.NoError(t, m.advanceClarifications(ctx, 1, at.Add(47*time.Hour)))
	require.Empty(t, observed)
	for _, hours := range []int{48, 48, 96, 100} {
		require.NoError(t, m.advanceClarifications(ctx, 1, at.Add(time.Duration(hours)*time.Hour)))
	}
	require.Len(t, observed, 2)
	for i, e := range observed {
		require.Equal(t, "clientwork.clarification_reminder", e.Type)
		require.Equal(t, int64(i+1), e.SubjectVersion)
		require.Equal(t, "SYSTEM", e.Actor.Kind)
	}
	get := func() ClarificationView {
		v, e := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: opened.ID.String()})
		require.NoError(t, e)
		return v.Body.Data
	}
	v := get()
	require.Equal(t, 2, v.RemindersSent)
	require.Nil(t, v.NextReminderAt)
	require.Equal(t, 1, v.Version)
	require.NoError(t, m.advanceClarifications(ctx, 1, at.Add(168*time.Hour)))
	require.NoError(t, m.advanceClarifications(ctx, 1, at.Add(200*time.Hour)))
	require.Len(t, observed, 3)
	v = get()
	require.Equal(t, "EXPIRED", v.State)
	require.Equal(t, "EXPIRED", v.Questions[0].State)
	require.Equal(t, "DEADLINE_ELAPSED", *v.CloseReason)
	var source struct {
		Status             string
		Version            int
		ClientUnresponsive bool
		OpenRoundID        *guid.ID
	}
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
	require.Equal(t, "TRIAGED", source.Status)
	require.True(t, source.ClientUnresponsive)
	require.Nil(t, source.OpenRoundID)
	// A subsequent round must survive a late answer on the expired one.
	request := clarificationRequest()
	request.Key = "next-round"
	request.Body.IntakeVersion = source.Version
	next, err := m.requestClarificationHTTP(ctx, request)
	require.NoError(t, err)
	answered, err := m.answerClarificationHTTP(testContext(t, 4), &EditInput[ClarificationAnswer]{EntityID: "3", ID: opened.ID.String(), IfMatch: fmt.Sprintf(`"%d"`, v.Version), Key: "late-answer", Body: ClarificationAnswer{QuestionNo: 1, Answer: "CSV"}})
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", answered.Body.Data.State)
	require.NoError(t, db.Table("g_intake_records").Where("id=50").Take(&source).Error)
	require.Equal(t, "CLARIFYING", source.Status)
	require.False(t, source.ClientUnresponsive)
	require.Equal(t, next.Body.Data.ID, *source.OpenRoundID)
	v = get()
	require.Equal(t, "ANSWERED", v.Questions[0].State)
	require.Equal(t, "EXPIRED", v.Questions[1].State)
}

func TestClarificationTimingRollbackAndUnavailablePolicy(t *testing.T) {
	for _, expiry := range []bool{false, true} {
		for _, boundary := range []string{"audit", "outbox"} {
			t.Run(fmt.Sprint(expiry, boundary), func(t *testing.T) {
				m, db, opened, at := timedClarification(t)
				failure := errors.New("boundary failed")
				if boundary == "audit" {
					m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
				} else {
					m.events = expiryPublisher{Publisher: m.events, publish: func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure }}
				}
				hours := 48
				if expiry {
					hours = 168
				}
				require.ErrorIs(t, m.advanceClarifications(t.Context(), 1, at.Add(time.Duration(hours)*time.Hour)), failure)
				q := query.BindClarificationRound(db, m.names)
				row, err := q.WithContext(t.Context()).Where(q.ID.Eq(opened.ID)).Take()
				require.NoError(t, err)
				require.Equal(t, "OPEN", row.State)
				require.Zero(t, row.RemindersSent)
				require.Nil(t, row.ClosedAt)
				var status string
				require.NoError(t, db.Table("g_intake_records").Select("status").Where("id=50").Scan(&status).Error)
				require.Equal(t, "CLARIFYING", status)
			})
		}
	}
	m, _, _, _ := clarificationHarness(t)
	opened, err := m.requestClarificationHTTP(testContext(t, 2), clarificationRequest())
	require.NoError(t, err)
	v, err := m.getClarificationHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: opened.Body.Data.ID.String()})
	require.NoError(t, err)
	require.False(t, v.Body.Data.TimingAvailable)
	require.Nil(t, v.Body.Data.DueAt)
}
