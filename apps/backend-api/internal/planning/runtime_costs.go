package planning

import (
	"context"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RuntimeCosts is bound by the composition-root adapter joining observed
// execution starts, historical assignments and real budget settlements.
type RuntimeCosts struct {
	PeriodOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (int64, bool, error)
}
type RuntimeCostsConnector struct{ Bind func(RuntimeCosts) error }

func (m *Module) ProvideServices(s *module.Services) error {
	return module.Provide[RuntimeCostsConnector](s, RuntimeCostsConnector{Bind: func(costs RuntimeCosts) error {
		if costs.PeriodOn == nil {
			return ErrInvalid
		}
		if m.runtimeCosts.PeriodOn != nil {
			return ErrConflict
		}
		m.runtimeCosts = costs
		return nil
	}})
}

func (m *Module) hydrateRuntimeCostsOn(ctx context.Context, tx *gorm.DB, tenant, project, subject guid.ID, start, end time.Time, out *delivery.PlanningSnapshot) error {
	if m.runtimeCosts.PeriodOn == nil {
		return nil
	}
	ids := make([]guid.ID, len(out.Work))
	for i, work := range out.Work {
		ids[i] = work.ID
	}
	var agent guid.ID
	if subject > 0 {
		if m.principals.ActiveHumanByID == nil {
			return ErrUnavailable
		}
		principal, human, err := m.principals.ActiveHumanByID(ctx, tx, subject)
		if err != nil {
			return err
		}
		if !human || principal.ID != subject {
			agent = subject
		}
	}
	cost, known, err := m.runtimeCosts.PeriodOn(ctx, tx, tenant, project, agent, ids, start, end)
	if err != nil {
		return err
	}
	if !known {
		return nil
	}
	if cost < 0 {
		return ErrUnavailable
	}
	out.ExecutionCostMicros = &cost
	out.MissingSources = slices.DeleteFunc(out.MissingSources, func(source string) bool { return source == "EXECUTION_COST" })
	return nil
}

var _ module.ServiceProvider = (*Module)(nil)
