package notification

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClarificationProjectionFailuresLeaveNoNotice(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "source", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.clarificationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ClarificationNotificationFact, bool, error) {
				if mode == "source" {
					return clientwork.ClarificationNotificationFact{}, false, ErrUnavailable
				}
				return clientwork.ClarificationNotificationFact{ProjectID: 81, IntakeID: 90, RequesterID: 11, Answered: true}, true, nil
			}
			f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
				return 81, 80, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "clientwork.clarification_answered"}
			switch mode {
			case "identity":
				e.ID = 0
			case "missing":
				f.m.clarificationFacts = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectClarificationOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	row := &model.Notification{TenantID: 7, ProjectID: 81, Type: "intake.client_silent", Params: `{"subject_id":"91"}`}
	f.m.clarificationFacts = nil
	yes, err := f.m.clarificationAudienceOn(t.Context(), f.db, row, 12)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.clarificationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ClarificationNotificationFact, bool, error) {
		return clientwork.ClarificationNotificationFact{ProjectID: 81, IntakeID: 90, RequesterID: 11, State: "EXPIRED"}, true, nil
	}
	f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 80, true, nil
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`} {
		row.Params = raw
		yes, err = f.m.clarificationAudienceOn(t.Context(), f.db, row, 12)
		require.False(t, yes)
		require.ErrorIs(t, err, ErrInvalid)
	}
	row.Params = `{"subject_id":"91"}`
	f.m.intakeTargets = nil
	yes, err = f.m.clarificationAudienceOn(t.Context(), f.db, row, 12)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestClarificationCurrentSourcePrivacyReplayAndExpiry(t *testing.T) {
	f := newFixture(t)
	fact := clientwork.ClarificationNotificationFact{ProjectID: 81, IntakeID: 90, RequesterID: 11, State: "OPEN", Answered: true}
	f.m.clarificationFacts = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (clientwork.ClarificationNotificationFact, bool, error) {
		return fact, tenant == 7 && id == 91, nil
	}
	f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 80, true, nil
	}
	f.m.intakeTargets = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{12}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "clientwork.clarification_answered", Data: []byte(`{"answer":"private client bytes"}`)}
	project := func() {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	project()
	project()
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	require.NotContains(t, rows[0].Params, "private")
	for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
		_, err := view(i18n.WithLocale(t.Context(), locale), rows[0])
		require.NoError(t, err)
	}
	at := f.m.now()
	f.m.now = func() time.Time { return at.Add(2 * time.Minute) }
	e.ID++
	project()
	rows, err = q.WithContext(t.Context()).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 2, rows[0].MergeCount)
	// Timeout goes to the actual requester and configured channel audience;
	// replay after a window/read change is still once per round.
	fact.State = "EXPIRED"
	e.Type = "clientwork.clarification_expired"
	e.ID++
	project()
	project()
	rows, err = q.WithContext(t.Context()).Where(q.Type.Eq("intake.client_silent")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		yes, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.True(t, yes)
	}
	fact.LateAnswer = true
	for _, row := range rows {
		yes, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, yes)
	}
}

func TestClarificationHandledReceiptSuppressesOnlyCurrentReply(t *testing.T) {
	f := newFixture(t)
	f.m.clarificationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ClarificationNotificationFact, bool, error) {
		return clientwork.ClarificationNotificationFact{ProjectID: 81, IntakeID: 90, RequesterID: 11, Answered: true}, true, nil
	}
	f.m.intakeSource = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error) {
		return 81, 80, true, nil
	}
	version := 1
	f.m.clarificationReplySource = func(context.Context, *gorm.DB, guid.ID, guid.ID, bool) (delivery.ClarificationReplyFact, bool, error) {
		return delivery.ClarificationReplyFact{ProjectID: 81, RequesterID: 11, ReplyVersion: version}, true, nil
	}
	f.m.clarificationHandled = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID, revision int) (bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(81), project)
		require.Equal(t, guid.ID(91), id)
		return revision == 1, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "clientwork.clarification_answered", Data: []byte(`{}`)}
	project := func() {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	project()
	q := query.BindNotifications(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	version = 2
	e.ID++
	project()
	count, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
