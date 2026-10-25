package budget

import (
	"7link.com/platform/internal/budget/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

type catalog struct{ err error }

func (c catalog) AddTypes(_ string, specs ...events.TypeSpec) error { return c.err }

func TestModuleLifecycleBindsGatewayAndFailsMissingDependencies(t *testing.T) {
	for _, fault := range []string{"", "connector", "bind_nil", "bind_error", "projects", "ids", "audit", "events", "catalog", "catalog_error"} {
		t.Run(fault, func(t *testing.T) {
			m, _ := budgetHarness(t)
			s := module.NewServices()
			require.NoError(t, m.ProvideServices(s))
			require.ErrorIs(t, m.ProvideServices(s), module.ErrServiceDuplicate)
			var bound fleet.GatewayBudgetConsumer
			if fault != "connector" {
				p := fleet.GatewayBudgetConnector{Bind: func(p fleet.GatewayBudgetConsumer) error {
					bound = p
					if fault == "bind_error" {
						return ErrUnavailable
					}
					return nil
				}}
				if fault == "bind_nil" {
					p.Bind = nil
				}
				require.NoError(t, module.Provide[fleet.GatewayBudgetConnector](s, p))
			}
			if fault != "projects" {
				require.NoError(t, module.Provide[delivery.ProjectBudgets](s, m.projects))
			}
			if fault != "ids" {
				require.NoError(t, module.Provide[guid.Source](s, m.ids))
			}
			if fault != "audit" {
				require.NoError(t, module.Provide[audit.Appender](s, m.audit))
			}
			if fault != "events" {
				require.NoError(t, module.Provide[events.Publisher](s, m.events))
			}
			if fault != "catalog" {
				c := catalog{}
				if fault == "catalog_error" {
					c.err = ErrUnavailable
				}
				require.NoError(t, module.Provide[events.Catalog](s, c))
			}
			require.NoError(t, s.Seal())
			err := m.InjectServices(s)
			if fault != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, bound.ReserveOn)
			require.NotNil(t, bound.SettleOn)
			require.NoError(t, m.Initialize(t.Context()))
			provider, err := module.Resolve[Provider](s)
			require.NoError(t, err)
			require.NotNil(t, provider.ReserveOn)
			require.NotNil(t, provider.SettleOn)
			require.NotNil(t, provider.FactOn)
			require.True(t, m.Enabled())
			require.Equal(t, Name, m.ModuleName())
			require.Contains(t, m.ModuleDependencies(), delivery.Name)
		})
	}
	for _, fault := range []string{"names", "lock", "spend", "ids", "audit", "events"} {
		m, _ := budgetHarness(t)
		switch fault {
		case "names":
			m.names = query.TableNames{}
		case "lock":
			m.projects.LockOn = nil
		case "spend":
			m.projects.SpendOn = nil
		case "ids":
			m.ids = nil
		case "audit":
			m.audit = nil
		case "events":
			m.events = nil
		}
		require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
	}
	var absent *Module
	require.False(t, absent.Enabled())
	require.ErrorIs(t, absent.Initialize(t.Context()), ErrUnavailable)
}

func TestGatewayBudgetAdapterEnforcesBothLimitsAndSettlesUnavailableAtZero(t *testing.T) {
	m, db := budgetHarness(t)
	ctx := t.Context()
	limit := int64(40)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveGatewayOn(ctx, tx, 1, 10, 20, 41, &limit) }), fleet.ErrVersionConflict)
	limit = -1
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveGatewayOn(ctx, tx, 1, 10, 20, 0, &limit) }), ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveGatewayOn(ctx, tx, 1, 10, 20, 101, nil) }), fleet.ErrVersionConflict)
	limit = 40
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := m.reserveGatewayOn(ctx, tx, 1, 10, 20, 40, &limit); err != nil {
			return err
		}
		return m.settleGatewayOn(ctx, tx, 1, 10, 20)
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, err := m.FactOn(ctx, tx, 1, 10)
		require.NoError(t, err)
		require.Zero(t, f.ReservedMicros)
		require.Zero(t, f.SpentMicros)
		return nil
	}))
	m.projects.LockOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ProjectBudget, error) {
		return delivery.ProjectBudget{}, errors.New("storage")
	}
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveGatewayOn(ctx, tx, 1, 10, 21, 1, nil) }), "storage")
}
