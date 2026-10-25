package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDeliveryConfirmationRejectsUnavailableOrChangedPublicationSource(t *testing.T) {
	for _, scenario := range []string{"unbound", "read_error", "missing", "wrong_project", "wrong_package", "published", "stale", "tenant", "invalid_package"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, source := deliveryConfirmationHarness(t)
			failure := errors.New("source unavailable")
			version, tenant, pkg := 1, guid.ID(1), guid.ID(81)
			want := ErrProjectNotFound
			switch scenario {
			case "unbound":
				m.clientPackageConfirmation = nil
				want = ErrUnavailable
			case "read_error":
				m.clientPackageConfirmation = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ClientPackageConfirmationFact, bool, error) {
					return ClientPackageConfirmationFact{}, false, failure
				}
				want = failure
			case "missing":
				m.clientPackageConfirmation = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ClientPackageConfirmationFact, bool, error) {
					return ClientPackageConfirmationFact{}, false, nil
				}
			case "wrong_project":
				source.ProjectID = 98
			case "wrong_package":
				source.PackageID = 80
			case "published":
				source.Draft = false
				want = ErrResultState
			case "stale":
				version = 2
				want = ErrWorkVersion
			case "tenant":
				tenant = 2
				want = ErrInvalidArgument
			case "invalid_package":
				pkg = 0
				want = ErrInvalidArgument
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.requestDeliveryConfirmationOn(ctx, tx, tenant, 99, pkg, 11, version)
				return err
			})
			require.ErrorIs(t, err, want)
			count, err := query.BindDeliveryConfirmations(db, m.service.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count, "a rejected source cannot create an approval occurrence")
		})
	}
}

func TestDeliveryConfirmationOutboxFailureRollsBackRequestAndWithdrawal(t *testing.T) {
	m, db, ctx, _ := deliveryConfirmationHarness(t)
	original := m.service.events
	failure := errors.New("outbox unavailable")
	broken := projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	m.service.events = broken
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
		return err
	})
	require.ErrorIs(t, err, failure)
	q := query.BindDeliveryConfirmations(db, m.service.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	m.service.events = original
	var first DeliveryConfirmationFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
		return err
	}))
	m.service.events = broken
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, first.ID, 11)
		return err
	})
	require.ErrorIs(t, err, failure)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, err)
	require.Nil(t, row.WithdrawnAt)
	require.Nil(t, row.WithdrawnBy)
	require.NotNil(t, row.ActiveKey)
	require.Equal(t, 1, row.Version)
	current, found, err := m.deliveryConfirmationOn(ctx, db, 1, first.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, first, current)
}

func deliveryConfirmationHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *ClientPackageConfirmationFact) {
	t.Helper()
	m, db, ctx, _ := workHTTPHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	source := &ClientPackageConfirmationFact{ProjectID: 99, PackageID: 81, Version: 1, Title: "Verified client package", Draft: true}
	m.clientPackageConfirmation = func(_ context.Context, _ *gorm.DB, tenant, project, id guid.ID, _ bool) (ClientPackageConfirmationFact, bool, error) {
		if tenant != 1 || project != source.ProjectID || id != source.PackageID {
			return ClientPackageConfirmationFact{}, false, nil
		}
		return *source, true, nil
	}
	return m, db, ctx, source
}

func TestDeliveryConfirmationOccurrenceWithdrawalAndCurrentSource(t *testing.T) {
	m, db, ctx, source := deliveryConfirmationHarness(t)
	request := func(version int) (DeliveryConfirmationFact, error) {
		var out DeliveryConfirmationFact
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, version)
			return err
		})
		return out, err
	}
	first, err := request(1)
	require.NoError(t, err)
	again, err := request(1)
	require.NoError(t, err)
	require.Equal(t, first, again)
	_, err = request(2)
	require.ErrorIs(t, err, ErrWorkVersion)
	read := func(want bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			fact, found, err := m.deliveryConfirmationOn(ctx, tx, 1, first.ID)
			require.NoError(t, err)
			require.Equal(t, want, found)
			if found {
				require.Equal(t, first, fact)
			}
			_, found, err = m.deliveryConfirmationOn(ctx, tx, 2, first.ID)
			require.NoError(t, err)
			require.False(t, found)
			return nil
		}))
	}
	read(true)
	source.Version = 2
	read(false)
	source.Version = 1
	source.Draft = false
	read(false)
	source.Draft = true
	read(true)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, first.ID, 11)
		return err
	}))
	read(false)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, first.ID, 11)
		return err
	}))
	second, err := request(1)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID, "a new request after withdrawal is a new notification occurrence")
	q := query.BindDeliveryConfirmations(db, m.service.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("tenant_id=? AND project_id=?", 1, 99).Updates(map[string]any{"stage": "CLOSED", "closed_at": second.RequestedAt, "archived_at": second.RequestedAt}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, err := m.deliveryConfirmationOn(ctx, tx, 1, second.ID)
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
}

func TestDeliveryConfirmationAtomicFailureAndCurrentHumanEligibility(t *testing.T) {
	m, db, ctx, _ := deliveryConfirmationHarness(t)
	original := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
		return err
	}))
	q := query.BindDeliveryConfirmations(db, m.service.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	m.service.audit = original
	var fact DeliveryConfirmationFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		fact, err = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
		return err
	}))
	for _, allowed := range []bool{true, false} {
		m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
			require.Equal(t, "delivery_send", permission)
			return authz.DataConstraint{AllowAll: allowed}, nil
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			ok, err := m.deliveryConfirmationEligibleOn(ctx, tx, 1, fact.ID, 11)
			require.NoError(t, err)
			require.Equal(t, allowed, ok)
			return nil
		}))
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	req := governance.InboxRequest{TenantID: 1, PrincipalID: 11, ProjectID: 99, Kind: "DELIVERY_CONFIRM", Limit: 1}
	rows, err := m.deliveryConfirmationInbox(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, fact.ID, rows[0].ID)
	req.After = &rows[0].InboxKey
	rows, err = m.deliveryConfirmationInbox(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = m.deliveryConfirmationInbox(context.Background(), req)
	require.ErrorIs(t, err, ErrWorkForbidden)
}

func TestDeliveryConfirmationStorageFailuresRollBackBothLifecycleCommands(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		t.Run(fmt.Sprint(withdraw), func(t *testing.T) {
			m, db, ctx, _ := deliveryConfirmationHarness(t)
			var first DeliveryConfirmationFact
			if withdraw {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					var e error
					first, e = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
					return e
				}))
			}
			q := query.BindDeliveryConfirmations(db, m.service.names)
			before, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			failure := errors.New("confirmation storage unavailable")
			rollback := errors.New("rollback confirmation")
			calls, failAt := 0, 0
			probe := func(tx *gorm.DB) {
				calls++
				if calls == failAt {
					tx.AddError(failure)
				}
			}
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("confirmation-storage-failure", probe))
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("confirmation-storage-failure", probe))
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("confirmation-storage-failure", probe))
			run := func() error {
				return db.Transaction(func(tx *gorm.DB) error {
					var e error
					if withdraw {
						_, e = m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, first.ID, 11)
					} else {
						_, e = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
					}
					if e != nil {
						return e
					}
					return rollback
				})
			}
			require.ErrorIs(t, run(), rollback)
			total := calls
			require.GreaterOrEqual(t, total, 3)
			for i := 1; i <= total; i++ {
				calls, failAt = 0, i
				require.ErrorIs(t, run(), failure, "confirmation stage %d", i)
			}
			require.NoError(t, db.Callback().Query().Remove("confirmation-storage-failure"))
			require.NoError(t, db.Callback().Create().Remove("confirmation-storage-failure"))
			require.NoError(t, db.Callback().Update().Remove("confirmation-storage-failure"))
			after, e := q.WithContext(ctx).Find()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestDeliveryConfirmationInjectionRegistersOnlyAvailableOwnerSource(t *testing.T) {
	for _, registered := range []bool{false, true} {
		m, _, _, _ := deliveryConfirmationHarness(t)
		source := m.clientPackageConfirmation
		m.clientPackageConfirmation = nil
		s := module.NewServices()
		require.NoError(t, module.Provide(s, source))
		if registered {
			require.NoError(t, module.Provide[governance.InboxSources](s, inboxTestRegistry{}))
		}
		require.NoError(t, s.Seal())
		require.NoError(t, m.injectDeliveryConfirmations(s))
		require.NotNil(t, m.clientPackageConfirmation)
	}
	m := &Module{}
	require.ErrorIs(t, m.injectDeliveryConfirmations(module.NewServices()), module.ErrServicesProviding)
}
func TestDeliveryConfirmationIDsAndCurrentPublicationFenceRemainAtomic(t *testing.T) {
	for _, zero := range []bool{false, true} {
		m, db, ctx, _ := deliveryConfirmationHarness(t)
		failure := errors.New("confirmation id unavailable")
		want := failure
		m.ids = unavailableActivityIDs{err: failure}
		if zero {
			m.ids = draftIDSource(func() (int64, error) { return 0, nil })
			want = ErrUnavailable
		}
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1); return e }), want)
		n, e := query.BindDeliveryConfirmations(db, m.service.names).WithContext(ctx).Count()
		require.NoError(t, e)
		require.Zero(t, n)
	}
	m, db, ctx, _ := deliveryConfirmationHarness(t)
	var fact DeliveryConfirmationFact
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		fact, e = m.requestDeliveryConfirmationOn(ctx, tx, 1, 99, 81, 11, 1)
		return e
	}))
	failure := errors.New("publication owner unavailable")
	m.clientPackageConfirmation = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ClientPackageConfirmationFact, bool, error) {
		return ClientPackageConfirmationFact{}, false, failure
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, fact.ID, 11)
		return e
	}), failure)
	_, _, e := m.deliveryConfirmationOn(ctx, db, 1, fact.ID)
	require.ErrorIs(t, e, failure)
	m.clientPackageConfirmation = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (ClientPackageConfirmationFact, bool, error) {
		return ClientPackageConfirmationFact{}, false, nil
	}
	_, found, e := m.deliveryConfirmationOn(ctx, db, 1, fact.ID)
	require.NoError(t, e)
	require.False(t, found)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.withdrawDeliveryConfirmationOn(ctx, tx, 1, 99, 999, 11); return e }), ErrProjectNotFound)
}
