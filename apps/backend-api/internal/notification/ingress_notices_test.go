package notification

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIngressSubscriberRequiresCatalogAndDispatcherContext(t *testing.T) {
	f := newFixture(t)
	empty := module.NewServices()
	require.NoError(t, empty.Seal())
	require.NoError(t, f.m.injectIngress(empty))
	require.Error(t, f.m.injectIngress(module.NewServices()))
	for _, mode := range []string{"missing_catalog", "registration", "source", "context"} {
		t.Run(mode, func(t *testing.T) {
			s := module.NewServices()
			subs := []events.Subscriber{}
			require.NoError(t, module.Provide[yunxiao.IngressFailureFacts](s, func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, bool, error) {
				if mode == "source" {
					return 0, false, ErrUnavailable
				}
				return 81, false, nil
			}))
			if mode != "missing_catalog" {
				catalog := fixtureCatalog{subscribers: &subs}
				if mode == "registration" {
					catalog.err = ErrUnavailable
				}
				require.NoError(t, module.Provide[events.SubscriberCatalog](s, catalog))
			}
			require.NoError(t, s.Seal())
			err := f.m.injectIngress(s)
			if mode == "missing_catalog" || mode == "registration" {
				require.Error(t, err)
				require.Empty(t, subs)
				return
			}
			require.NoError(t, err)
			require.Len(t, subs, 1)
			require.Equal(t, events.Transactional, subs[0].Mode)
			err = f.db.Transaction(func(tx *gorm.DB) error {
				return subs[0].Handle(t.Context(), tx, events.Envelope{EventID: 1001, TenantID: 7, EventType: "integration.ingress_changed", Subject: events.Subject{ID: 91, Kind: "integration_ingress"}})
			})
			if mode == "source" {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.ErrorIs(t, err, events.ErrInvalidEvent)
			}
		})
	}
}

func TestIngressFailureProjectionCannotNotifyUnverifiedAudience(t *testing.T) {
	for _, mode := range []string{"identity", "missing", "source", "connection", "candidates", "order", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			facts := yunxiao.IngressFailureFacts(func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, bool, error) {
				if mode == "source" {
					return 0, false, ErrUnavailable
				}
				if mode == "connection" {
					return 0, true, nil
				}
				return 81, true, nil
			})
			e := events.Envelope{EventID: 1001, TenantID: 7, EventType: "integration.ingress_changed", Subject: events.Subject{ID: 91, Kind: "integration_ingress"}}
			switch mode {
			case "identity":
				e.Subject.ID = 0
			case "missing":
				facts = nil
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, ErrUnavailable }
			case "order":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return []guid.ID{0}, nil }
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.ingressOn(t.Context(), tx, e, facts) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestIngressExhaustionCurrentAudienceReplayAndRollback(t *testing.T) {
	f := newFixture(t)
	exhausted := false
	facts := func(_ context.Context, _ *gorm.DB, tenant, event guid.ID) (guid.ID, bool, error) {
		require.Equal(t, guid.ID(7), tenant)
		require.Equal(t, guid.ID(91), event)
		return 81, exhausted, nil
	}
	e := events.Envelope{EventID: 101, TenantID: 7, EventType: "integration.ingress_changed", Subject: events.Subject{Kind: "integration_ingress", ID: 91}}
	allowed := true
	f.m.tenantPermission = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, id guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "integration_manage", permission)
		return authz.DataConstraint{AllowAll: allowed && id == 11}, nil
	}
	apply := func() error {
		return f.db.Transaction(func(tx *gorm.DB) error { return f.m.ingressOn(t.Context(), tx, e, facts) })
	}
	count := func(want int64) {
		n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
		require.NoError(t, err)
		require.Equal(t, want, n)
	}
	require.NoError(t, apply())
	count(0)
	exhausted = true
	rollback := errors.New("later failure")
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, f.m.ingressOn(t.Context(), tx, e, facts))
		return rollback
	}), rollback)
	count(0)
	require.NoError(t, apply())
	e.EventID++
	require.NoError(t, apply())
	count(1)
	q := query.BindNotifications(f.db, f.m.names)
	row, err := q.WithContext(t.Context()).First()
	require.NoError(t, err)
	require.Equal(t, guid.ID(11), row.RecipientPrincipalID)
	require.Equal(t, "yunxiao.ingress_exhausted", row.TemplateKey)
	require.Equal(t, "integration_manage", row.Permission)
	yes, err := f.m.visibleOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.True(t, yes)
	allowed = false
	yes, err = f.m.visibleOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.False(t, yes)
	require.NoError(t, apply())
	count(1)
}
