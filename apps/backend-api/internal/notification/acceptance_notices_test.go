package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAcceptanceSubscriberRequiresCatalogAndDispatcherContext(t *testing.T) {
	f := newFixture(t)
	empty := module.NewServices()
	require.NoError(t, empty.Seal())
	require.NoError(t, f.m.injectAcceptance(empty))
	require.Error(t, f.m.injectAcceptance(module.NewServices()))
	for _, mode := range []string{"missing_catalog", "registration", "source", "context"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			subs := []events.Subscriber{}
			require.NoError(t, module.Provide[clientwork.AcceptanceReminderFacts](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.AcceptanceReminderFact, bool, error) {
				if mode == "source" {
					return clientwork.AcceptanceReminderFact{}, false, ErrUnavailable
				}
				return clientwork.AcceptanceReminderFact{}, false, nil
			}))
			if mode != "missing_catalog" {
				catalog := fixtureCatalog{subscribers: &subs}
				if mode == "registration" {
					catalog.err = ErrUnavailable
				}
				require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
			}
			require.NoError(t, s.Seal())
			err := f.m.injectAcceptance(s)
			if mode == "missing_catalog" || mode == "registration" {
				require.Error(t, err)
				require.Empty(t, subs)
				return
			}
			require.NoError(t, err)
			require.Len(t, subs, 1)
			require.Equal(t, events.Transactional, subs[0].Mode)
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return subs[0].Handle(t.Context(), tx, events.Envelope{EventID: 1001, TenantID: 7, EventType: "clientwork.acceptance_reminder", Subject: events.Subject{ID: 91}, Data: []byte(`{"recipient_id":"11"}`)})
			})
			if mode == "source" {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, events.ErrInvalidEvent)
			}
		})
	}
	f.m.acceptanceReminderFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.AcceptanceReminderFact, bool, error) {
		return clientwork.AcceptanceReminderFact{}, false, ErrUnavailable
	}
	for _, raw := range []string{"[]", `{"subject_id":"bad"}`, `{"subject_id":"91"}`} {
		yes, err := f.m.acceptanceAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, Params: raw}, 11)
		require.False(t, yes)
		require.Error(t, err)
	}
}

func TestAcceptanceReminderRoutesCurrentRecipientAndRechecksSource(t *testing.T) {
	f := newFixture(t)
	found := true
	var failure error
	facts := clientwork.AcceptanceReminderFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.AcceptanceReminderFact, bool, error) {
		return clientwork.AcceptanceReminderFact{ProjectID: 81, PackageID: 21, RecipientID: 11, Day: 3, DueAt: time.Now()}, found, failure
	})
	f.m.acceptanceReminderFacts = facts
	e := events.Envelope{EventID: 1001, TenantID: 7, ProjectID: 81, EventType: "clientwork.acceptance_reminder", Subject: events.Subject{Kind: "clientwork", ID: 91}, SubjectVersion: 3, Data: []byte(`{"recipient_id":"11","notes":"PRIVATE"}`)}
	run := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.acceptanceOn(t.Context(), tx, e, facts) })
	}
	require.NoError(t, run())
	require.NoError(t, run())
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID)
	require.NotContains(t, rows[0].Params, "PRIVATE")
	require.Contains(t, rows[0].LinkPath, "/deliveries/21?tab=timeline")
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		visible, e := f.m.acceptanceAudienceOn(t.Context(), tx, rows[0], 11)
		require.NoError(t, e)
		require.True(t, visible)
		visible, e = f.m.acceptanceAudienceOn(t.Context(), tx, rows[0], 12)
		require.NoError(t, e)
		require.False(t, visible)
		found = false
		visible, e = f.m.acceptanceAudienceOn(t.Context(), tx, rows[0], 11)
		require.NoError(t, e)
		require.False(t, visible)
		return nil
	}))
	found = true
	e.EventID = 1002
	e.Data = []byte(`{"recipient_id":"12"}`)
	require.ErrorIs(t, run(), ErrInvalid)
	e.Data = []byte(`{"recipient_id":"11"}`)
	failure = errors.New("source unavailable")
	require.ErrorIs(t, run(), failure)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func TestAcceptanceEscalationUsesCurrentLeadAudience(t *testing.T) {
	f := newFixture(t)
	facts := clientwork.AcceptanceReminderFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.AcceptanceReminderFact, bool, error) {
		return clientwork.AcceptanceReminderFact{ProjectID: 81, PackageID: 21, RecipientID: 11, Day: 14, Escalation: true, DueAt: time.Now()}, true, nil
	})
	f.m.acceptanceReminderFacts = facts
	e := events.Envelope{EventID: 1003, TenantID: 7, ProjectID: 81, EventType: "clientwork.acceptance_overdue", Subject: events.Subject{Kind: "clientwork", ID: 92}, SubjectVersion: 14, Data: []byte(`{"recipient_id":"11"}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.acceptanceOn(t.Context(), tx, e, facts) }))
	rows, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.Equal(t, "delivery.acceptance_overdue", row.Type)
		require.Equal(t, "project_read", row.Permission)
	}
}

func TestAcceptanceEscalationFailureCannotNotifyUnverifiedAudience(t *testing.T) {
	for _, mode := range []string{"missing", "payload", "candidates_missing", "candidates", "order", "lock", "source", "lead", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			facts := clientwork.AcceptanceReminderFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) (clientwork.AcceptanceReminderFact, bool, error) {
				if mode == "source" {
					return clientwork.AcceptanceReminderFact{}, false, ErrUnavailable
				}
				return clientwork.AcceptanceReminderFact{ProjectID: 81, PackageID: 21, RecipientID: 11, Day: 3, Escalation: true, DueAt: f.m.now()}, true, nil
			})
			f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{11}, nil
			}
			e := events.Envelope{EventID: 1001, TenantID: 7, ProjectID: 81, EventType: "clientwork.acceptance_overdue", Subject: events.Subject{ID: 91}, SubjectVersion: 3, Data: []byte(`{"recipient_id":"11"}`)}
			switch mode {
			case "missing":
				facts = nil
			case "payload":
				e.Data = []byte("[")
			case "candidates_missing":
				f.m.candidates = nil
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "lead":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.acceptanceOn(t.Context(), tx, e, facts) }))
			n, x := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, x)
			require.Zero(t, n)
		})
	}
}
