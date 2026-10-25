package planning

import (
	"context"
	"time"

	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/infra/guid"

	"7link.com/platform/internal/templates"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type runtimeCostsAdapter struct{ enabled bool }

func (*runtimeCostsAdapter) ModuleName() string { return "workflow_runtime_costs" }
func (m *runtimeCostsAdapter) Enabled() bool    { return m.enabled }
func (*runtimeCostsAdapter) ModuleDependencies() []string {
	return []string{templates.Name, Name, delivery.Name, execution.Name, budget.Name}
}
func (*runtimeCostsAdapter) InjectServices(s *module.Services) error {
	starts, err := module.Resolve[execution.LoopExecutionFacts](s)
	if err != nil {
		return err
	}
	settlements, err := module.Resolve[budget.SettlementFacts](s)
	if err != nil {
		return err
	}
	connector, err := module.Resolve[templates.RuntimeCostsConnector](s)
	if err != nil {
		return err
	}
	if starts.ReadOn == nil || settlements.ExecutionsOn == nil || connector.Bind == nil {
		return templates.ErrUnavailable
	}
	assignments, err := module.Resolve[delivery.LoopAssignments](s)
	if err != nil {
		return err
	}
	periodConnector, err := module.Resolve[RuntimeCostsConnector](s)
	if err != nil {
		return err
	}
	if assignments.AgentsOn == nil || periodConnector.Bind == nil {
		return templates.ErrUnavailable
	}
	period := func(ctx context.Context, tx *gorm.DB, tenant, project, agent guid.ID, work []guid.ID, start, end time.Time) (int64, bool, error) {
		facts, e := starts.ReadOn(ctx, tx, tenant, project, work, start, end)
		if e != nil {
			return 0, false, e
		}
		agents := map[guid.ID]guid.ID{}
		if agent > 0 {
			assignmentIDs := make([]guid.ID, 0, len(facts))
			seen := map[guid.ID]bool{}
			for _, fact := range facts {
				if !seen[fact.AssignmentID] {
					seen[fact.AssignmentID] = true
					assignmentIDs = append(assignmentIDs, fact.AssignmentID)
				}
			}
			agents, e = assignments.AgentsOn(ctx, tx, tenant, project, assignmentIDs)
			if e != nil {
				return 0, false, e
			}
		}
		ids := make([]guid.ID, 0, len(facts))
		for _, fact := range facts {
			if agent > 0 {
				holder, exists := agents[fact.AssignmentID]
				if !exists {
					return 0, false, nil
				}
				if holder != agent {
					continue
				}
			}
			if fact.StartedAt == nil {
				return 0, false, nil
			}
			ids = append(ids, fact.ID)
		}
		return settlements.ExecutionsOn(ctx, tx, tenant, project, ids)
	}
	if err = periodConnector.Bind(RuntimeCosts{PeriodOn: period}); err != nil {
		return err
	}
	return connector.Bind(templates.RuntimeCosts{WorkOn: func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, work []guid.ID) (int64, bool, error) {
		if work == nil || len(work) > 500 {
			return 0, false, templates.ErrInvalid
		}
		return period(ctx, tx, tenant, project, 0, work, time.Time{}, time.Now().UTC().Add(time.Second))
	}})
}

// NewRuntimeCostsBridge registers the existing capability adapter.
func NewRuntimeCostsBridge(enabled bool) module.Module { return &runtimeCostsAdapter{enabled: enabled} }

var _ module.Enabler = (*runtimeCostsAdapter)(nil)

var _ module.Depender = (*runtimeCostsAdapter)(nil)

var _ module.ServiceInjector = (*runtimeCostsAdapter)(nil)
