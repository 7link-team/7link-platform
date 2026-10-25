package notification

import (
	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReminderSubscribersBindSourcesAndRollbackOutsideDispatcher(t *testing.T) {
	f := newFixture(t)
	empty := module.NewServices()
	require.NoError(t, empty.Seal())
	require.NoError(t, f.m.injectReminder(empty))
	require.Error(t, f.m.injectReminder(module.NewServices()))
	for _, mode := range []string{"missing_catalog", "registration", "success"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			subs := []events.Subscriber{}
			require.NoError(t, module.Provide[clientwork.ClarificationReminderFacts](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ClarificationReminderFact, bool, error) {
				return clientwork.ClarificationReminderFact{ProjectID: 81, RecipientID: 11, Ordinal: 1, DueAt: f.m.now()}, true, nil
			}))
			require.NoError(t, module.Provide[delivery.DispatchStarvationFacts](s, delivery.DispatchStarvationFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }}))
			if mode != "missing_catalog" {
				catalog := fixtureCatalog{subscribers: &subs}
				if mode == "registration" {
					catalog.err = ErrUnavailable
				}
				require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
			}
			require.NoError(t, s.Seal())
			err := f.m.injectReminder(s)
			if mode != "success" {
				require.Error(t, err)
				require.Empty(t, subs)
				return
			}
			require.NoError(t, err)
			require.Len(t, subs, 2)
			for _, sub := range subs {
				require.Equal(t, events.Transactional, sub.Mode)
				require.Equal(t, events.Latest, sub.StartFrom)
				e := events.Envelope{EventID: 1001, TenantID: 7, ProjectID: 81, Subject: events.Subject{ID: 91}, SubjectVersion: 1}
				require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return sub.Handle(t.Context(), tx, e) }), events.ErrInvalidEvent)
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n, "dispatcher context is mandatory for commit hints")
		})
	}
}

func TestClarificationReminderUsesRealRecipientAndDedupesOrdinal(t *testing.T) {
	f := newFixture(t)
	exists := true
	var failure error
	facts := clientwork.ClarificationReminderFacts(func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (clientwork.ClarificationReminderFact, bool, error) {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 91, id)
		return clientwork.ClarificationReminderFact{ProjectID: 81, RecipientID: 11, Ordinal: 2, DueAt: time.Now().Add(time.Hour)}, exists, failure
	})
	e := events.Envelope{EventID: 1001, TenantID: 7, ProjectID: 81, Subject: events.Subject{Kind: "clientwork", ID: 91}, SubjectVersion: 1, Data: []byte(`{"recipient_id":"12","questions":"PRIVATE BYTES"}`)}
	run := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.reminderOn(t.Context(), tx, e, facts) })
	}
	require.NoError(t, run())
	require.NoError(t, run())
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 11, rows[0].RecipientPrincipalID)
	require.NotContains(t, rows[0].Params, "PRIVATE")
	require.Contains(t, rows[0].LinkPath, "/baseline?")
	exists = false
	e.SubjectVersion = 2
	e.EventID = 1002
	require.NoError(t, run())
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	exists = true
	failure = errors.New("current client source failed")
	require.Error(t, run())
	failure = nil
	require.NoError(t, run())
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestStarvationNotifiesCurrentLeadAtMostOncePerFourHours(t *testing.T) {
	f := newFixture(t)
	eligible := true
	at := f.m.now()
	f.m.now = func() time.Time { return at }
	facts := delivery.DispatchStarvationFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
		return eligible, nil
	}}
	e := events.Envelope{TenantID: 7, ProjectID: 81, Subject: events.Subject{Kind: "clientwork", ID: 91}, SubjectVersion: 7}
	run := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.starvationOn(t.Context(), tx, e, facts) })
	}
	require.NoError(t, run())
	require.NoError(t, run())
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// Reading or archiving cannot restart the four-hour throttle.
	require.NoError(t, f.db.Table(f.m.names.Notifications.String()).Where("type='dispatch.starved'").Updates(map[string]any{"read_at": at, "archived_at": at, "dedupe_slot": nil}).Error)
	at = at.Add(4*time.Hour - time.Millisecond)
	require.NoError(t, run())
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	at = at.Add(time.Millisecond)
	require.NoError(t, run())
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, n)
	eligible = false
	at = at.Add(5 * time.Hour)
	require.NoError(t, run())
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, n)
	eligible = true
	f.allow = false
	require.NoError(t, run())
	n, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, n)
}

func TestStarvationFailuresCannotPersistUnverifiedReminder(t *testing.T) {
	for _, mode := range []string{"missing", "source", "candidates", "order", "lead", "lock", "guard", "read", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			facts := delivery.DispatchStarvationFacts{CurrentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
				if mode == "source" {
					return false, ErrUnavailable
				}
				return true, nil
			}}
			switch mode {
			case "missing":
				facts.CurrentOn = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "lead":
				f.err = ErrUnavailable
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("starvation_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.Notifications.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "notify":
				f.ids.err = ErrUnavailable
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.starvationOn(t.Context(), tx, events.Envelope{TenantID: 7, ProjectID: 81, Subject: events.Subject{ID: 91}}, facts)
			})
			if mode == "order" {
				require.ErrorIs(t, err, ErrInvalid)
			} else {
				require.ErrorIs(t, err, ErrUnavailable)
			}
			if mode == "read" {
				require.NoError(t, f.db.Callback().Query().Remove("starvation_read"))
			}
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestClarificationReminderRejectsInvalidCurrentRecipientAndOrdinal(t *testing.T) {
	for _, mode := range []string{"missing", "project", "recipient", "ordinal", "version", "future", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			fact := clientwork.ClarificationReminderFact{ProjectID: 81, RecipientID: 11, Ordinal: 2, DueAt: f.m.now()}
			e := events.Envelope{EventID: 1001, TenantID: 7, ProjectID: 81, Subject: events.Subject{ID: 91}, SubjectVersion: 1}
			switch mode {
			case "project":
				fact.ProjectID = 82
			case "recipient":
				fact.RecipientID = 0
			case "ordinal":
				fact.Ordinal = 0
			case "version":
				e.SubjectVersion = 0
			case "future":
				e.SubjectVersion = 3
			case "notify":
				f.ids.err = ErrUnavailable
			}
			facts := clientwork.ClarificationReminderFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.ClarificationReminderFact, bool, error) {
				return fact, true, nil
			})
			if mode == "missing" {
				facts = nil
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.reminderOn(t.Context(), tx, e, facts) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}
