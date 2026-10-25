package budget

import (
	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"path/filepath"
	"testing"
	"time"
)

type publisher func(context.Context, *gorm.DB, events.Event) (guid.ID, error)

func (f publisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return f(ctx, tx, e)
}

func budgetHarness(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "budget.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("b_"), appenv.WithDatabase(router))
	d := delivery.NewModule(rt, delivery.Config{})
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	for _, source := range []goosex.Source{d.MigrationSource(), m.MigrationSource()} {
		source.Prefix = "b_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	now := time.Now().UTC()
	require.NoError(t, db.Exec("INSERT INTO b_delivery_projects(tenant_id,project_id,code,budget_cap_micros,stage_changed_at,created_at,updated_at) VALUES (1,10,'AB',100,?,?,?),(2,10,'AB',100,?,?,?)", now, now, now, now, now, now).Error)
	require.NoError(t, db.Exec("CREATE TABLE effects(kind TEXT NOT NULL)").Error)
	s := module.NewServices()
	require.NoError(t, d.ProvideServices(s))
	require.NoError(t, s.Seal())
	m.projects, err = module.Resolve[delivery.ProjectBudgets](s)
	require.NoError(t, err)
	// The ledger uses the real owner's locking read. Its write dependency is a
	// transaction-backed fixture; delivery tests exercise threshold publication.
	m.projects.SpendOn = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, amount int64) error {
		f, err := m.projects.LockOn(ctx, tx, tenant, project)
		if err != nil {
			return err
		}
		if amount < 0 || amount > math.MaxInt64-f.SpentMicros {
			return delivery.ErrInvalidArgument
		}
		state := "NORMAL"
		if f.CapMicros != nil && f.SpentMicros+amount >= *f.CapMicros {
			state = "EXHAUSTED"
		}
		return tx.Exec("UPDATE b_delivery_projects SET budget_spent_micros=?,budget_state=? WHERE tenant_id=? AND project_id=?", f.SpentMicros+amount, state, tenant, project).Error
	}
	m.ids, err = guid.New(1)
	require.NoError(t, err)
	m.audit = func(_ context.Context, tx *gorm.DB, _ audit.DomainEvent) error {
		return tx.Exec("INSERT INTO effects VALUES ('audit')").Error
	}
	m.events = publisher(func(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
		return 1, tx.Exec("INSERT INTO effects VALUES (?)", e.Type).Error
	})
	return m, db
}

func TestReserveSettleExactCapReplayAndTenantIsolation(t *testing.T) {
	m, db := budgetHarness(t)
	ctx := t.Context()
	reserve := func(tenant, id guid.ID, amount int64) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, tenant, 10, "EXECUTION", id, amount, true) })
	}
	require.NoError(t, reserve(1, 20, 60))
	require.NoError(t, reserve(1, 20, 60))
	require.ErrorIs(t, reserve(1, 20, 61), ErrConflict)
	require.ErrorIs(t, reserve(1, 21, 41), ErrExceeded)
	require.NoError(t, reserve(1, 21, 40))
	require.NoError(t, reserve(2, 20, 100))
	settle := func(actual int64) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, actual, true) })
	}
	require.NoError(t, settle(70))
	require.NoError(t, settle(70))
	require.ErrorIs(t, settle(71), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, err := m.FactOn(ctx, tx, 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(70), f.SpentMicros)
		require.Equal(t, int64(40), f.ReservedMicros)
		return nil
	}))
	require.ErrorIs(t, reserve(1, 22, 0), ErrExceeded)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 21, 50, true) }))
	var spent int64
	var state string
	require.NoError(t, db.Raw("SELECT budget_spent_micros,budget_state FROM b_delivery_projects WHERE tenant_id=1").Row().Scan(&spent, &state))
	require.Equal(t, int64(120), spent)
	require.Equal(t, "EXHAUSTED", state)
	var count int64
	require.NoError(t, db.Table("effects").Count(&count).Error)
	require.Equal(t, int64(10), count, "replay never duplicates audit or outbox")
}

func TestBudgetUnknownCostsAndIntegerOverflowFailClosed(t *testing.T) {
	m, db := budgetHarness(t)
	ctx := t.Context()
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 0, false) }), ErrPriceUnknown)
	require.NoError(t, db.Exec("UPDATE b_delivery_projects SET budget_cap_micros=NULL WHERE tenant_id=1").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 0, false) }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 0, false) }), ErrPriceUnknown)
	require.NoError(t, db.Exec("UPDATE b_delivery_projects SET budget_cap_micros=100 WHERE tenant_id=1").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, err := m.FactOn(ctx, tx, 1, 10)
		require.True(t, f.UnknownReserved)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 30, 1, true) }), ErrPriceUnknown)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 0, true) }))
	require.NoError(t, db.Exec("UPDATE b_delivery_projects SET budget_cap_micros=NULL WHERE tenant_id=1").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 21, math.MaxInt64, true) }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 22, 1, true) }), ErrInvalidArgument)
	require.NoError(t, db.Exec("UPDATE b_delivery_projects SET budget_spent_micros=? WHERE tenant_id=1", math.MaxInt64).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 21, 1, true) }), delivery.ErrInvalidArgument)
}

func TestBudgetReservationAndSpendRollbackWithAuditOutbox(t *testing.T) {
	for _, operation := range []string{"reserve", "settle"} {
		for _, fault := range []string{"audit", "outbox"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				m, db := budgetHarness(t)
				ctx := t.Context()
				injected := errors.New("injected")
				if operation == "settle" {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 30, true) }))
				}
				if fault == "audit" {
					m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
				} else {
					m.events = publisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
				}
				err := db.Transaction(func(tx *gorm.DB) error {
					if operation == "reserve" {
						return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 30, true)
					}
					return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 20, true)
				})
				require.ErrorIs(t, err, injected)
				var spent int64
				require.NoError(t, db.Raw("SELECT budget_spent_micros FROM b_delivery_projects WHERE tenant_id=1").Scan(&spent).Error)
				require.Zero(t, spent)
				q := query.BindReservations(db, m.names)
				rows, err := q.WithContext(ctx).Find()
				require.NoError(t, err)
				if operation == "reserve" {
					require.Empty(t, rows)
				} else {
					require.Len(t, rows, 1)
					require.Equal(t, "RESERVED", rows[0].State)
					require.Nil(t, rows[0].ActualMicros)
				}
			})
		}
	}
}

func TestBudgetRejectsInvalidTransactionsAndReplayProject(t *testing.T) {
	m, db := budgetHarness(t)
	ctx := t.Context()
	_, err := m.FactOn(ctx, nil, 1, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.FactOn(ctx, db, 1, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	for _, in := range []struct {
		source string
		id     guid.ID
		amount int64
		known  bool
	}{{"OTHER", 1, 1, true}, {"GATEWAY", 0, 1, true}, {"GATEWAY", 1, -1, true}, {"GATEWAY", 1, 1, false}} {
		require.ErrorIs(t, m.ReserveOn(ctx, nil, 1, 10, in.source, in.id, in.amount, in.known), ErrInvalidArgument)
		require.ErrorIs(t, m.SettleOn(ctx, nil, 1, 10, in.source, in.id, in.amount, in.known), ErrInvalidArgument)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 10, true) }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 2, 10, "EXECUTION", 20, 1, true) }))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.FactOn(ctx, tx, 3, 10); return err }))
	require.NoError(t, db.Exec("UPDATE b_budget_reservations SET project_id=11 WHERE tenant_id=1").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.ReserveOn(ctx, tx, 1, 10, "EXECUTION", 20, 10, true) }), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.SettleOn(ctx, tx, 1, 10, "EXECUTION", 20, 1, true) }), ErrConflict)
}

type reservationIDs struct {
	id  int64
	err error
}

func (s reservationIDs) Next() (int64, error) { return s.id, s.err }

func TestReservationDependencyFailuresLeaveNoLedgerOrEffects(t *testing.T) {
	for _, fault := range []string{"ids_missing", "ids_error", "ids_zero", "audit_missing", "events_missing", "project_missing"} {
		t.Run(fault, func(t *testing.T) {
			m, db := budgetHarness(t)
			want := ErrUnavailable
			switch fault {
			case "ids_missing":
				m.ids = nil
			case "ids_error":
				want = errors.New("identifier unavailable")
				m.ids = reservationIDs{err: want}
			case "ids_zero":
				m.ids = reservationIDs{}
			case "audit_missing":
				m.audit = nil
			case "events_missing":
				m.events = nil
			case "project_missing":
				m.projects.LockOn = nil
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.ReserveOn(t.Context(), tx, 1, 10, "EXECUTION", 20, 10, true)
			})
			require.ErrorIs(t, err, want)
			rows, err := query.BindReservations(db, m.names).WithContext(t.Context()).Find()
			require.NoError(t, err)
			require.Empty(t, rows)
			var effects int64
			require.NoError(t, db.Table("effects").Count(&effects).Error)
			require.Zero(t, effects)
		})
	}
}

func TestSettlementWithoutSpendCapabilityPreservesReservation(t *testing.T) {
	m, db := budgetHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ReserveOn(t.Context(), tx, 1, 10, "EXECUTION", 20, 10, true)
	}))
	m.projects.SpendOn = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.SettleOn(t.Context(), tx, 1, 10, "EXECUTION", 20, 7, true)
	}), ErrUnavailable)
	rows, err := query.BindReservations(db, m.names).WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "RESERVED", rows[0].State)
	require.Nil(t, rows[0].ActualMicros)
	var effects int64
	require.NoError(t, db.Table("effects").Count(&effects).Error)
	require.EqualValues(t, 2, effects)
}
