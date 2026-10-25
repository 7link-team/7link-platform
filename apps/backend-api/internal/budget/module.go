// Package budget serializes project reservation and settlement in caller transactions.
package budget

//go:generate go run ./dbgen

import (
	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

const Name = "budget"

type Config struct{ module.Config }
type Module struct {
	config   Config
	names    query.TableNames
	projects delivery.ProjectBudgets
	ids      guid.Source
	audit    audit.Appender
	events   events.Publisher
}

func NewModule(rt *appenv.Runtime, c Config) *Module {
	return &Module{config: c, names: query.BindNames(rt.TablePrefix())}
}
func (*Module) ModuleName() string { return Name }
func (m *Module) Enabled() bool    { return m != nil && m.config.Enabled }
func (*Module) ModuleDependencies() []string {
	return []string{delivery.Name, fleet.Name, guid.Name, audit.Name, events.Name}
}
func (m *Module) ProvideServices(s *module.Services) error {
	if e := m.provideWorkflowSettlements(s); e != nil {
		return e
	}
	return module.Provide[Provider](s, Provider{FactOn: m.FactOn, ReserveOn: m.ReserveOn, SettleOn: m.SettleOn})
}
func (m *Module) InjectServices(s *module.Services) error {
	if err := m.bindProjectHistory(s); err != nil {
		return err
	}
	if err := m.bindGatewaySettlement(s); err != nil {
		return err
	}
	if err := m.bindGatewayReservations(s); err != nil {
		return err
	}
	connector, err := module.Resolve[fleet.GatewayBudgetConnector](s)
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	if err = connector.Bind(fleet.GatewayBudgetConsumer{ReserveOn: m.reserveGatewayOn, SettleOn: m.settleGatewayOn}); err != nil {
		return err
	}
	if m.projects, err = module.Resolve[delivery.ProjectBudgets](s); err != nil {
		return err
	}
	if m.ids, err = module.Resolve[guid.Source](s); err != nil {
		return err
	}
	if m.audit, err = module.Resolve[audit.Appender](s); err != nil {
		return err
	}
	if m.events, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	c, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	return c.AddTypes(Name, events.TypeSpec{Name: "budget.reserved", SchemaVersion: 1, SubjectKind: "budget_reservation", Realtime: events.RealtimeSignal}, events.TypeSpec{Name: "budget.settled", SchemaVersion: 1, SubjectKind: "budget_reservation", Realtime: events.RealtimeSignal})
}

func (m *Module) reserveGatewayOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, amount int64, limit *int64) error {
	f, err := m.FactOn(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	if limit != nil {
		if *limit < 0 {
			return ErrInvalidArgument
		}
		if f.SpentMicros > *limit || f.ReservedMicros > *limit-f.SpentMicros || amount > *limit-f.SpentMicros-f.ReservedMicros {
			return fleet.ErrVersionConflict
		}
	}
	if err = m.ReserveOn(ctx, tx, tenant, project, "GATEWAY", id, amount, true); errors.Is(err, ErrExceeded) || errors.Is(err, ErrPriceUnknown) {
		return fleet.ErrVersionConflict
	}
	return err
}
func (m *Module) settleGatewayOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	return m.SettleOn(ctx, tx, tenant, project, "GATEWAY", id, 0, true)
}
func (m *Module) Initialize(context.Context) error {
	if m == nil || m.names.Reservations.IsZero() || m.projects.LockOn == nil || m.projects.SpendOn == nil || m.ids == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	return nil
}

var (
	_ module.Enabler                 = (*Module)(nil)
	_ module.Depender                = (*Module)(nil)
	_ module.ServiceProvider         = (*Module)(nil)
	_ module.ServiceInjector         = (*Module)(nil)
	_ module.Initializer             = (*Module)(nil)
	_ module.MigrationSourceProvider = (*Module)(nil)
)
