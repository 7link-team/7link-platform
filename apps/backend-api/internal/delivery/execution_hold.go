package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"slices"
)

// ExecutionHolds is the execution owner's transaction capability. The caller
// authorizes the hold and locks PrincipalsOn before locking project/work rows.
// Requested controls are distinct from actual Runner acknowledgements.
type ExecutionHolds struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)
	ChangeOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) ([]ExecutionHoldFact, error)
}

type ExecutionHoldFact struct {
	ExecutionID guid.ID `json:"execution_id"`
	State       string  `json:"state"`
	Control     string  `json:"control" enum:"PAUSE_REQUESTED,PAUSED,RESUME_REQUESTED,NOT_RUNNING"`
}

// ExecutionHoldConnector avoids a delivery-to-execution import cycle.
type ExecutionHoldConnector struct{ Bind func(ExecutionHolds) error }

func (m *Module) provideExecutionHolds(s *module.Services) error {
	return module.Provide[ExecutionHoldConnector](s, ExecutionHoldConnector{Bind: func(h ExecutionHolds) error {
		if h.PrincipalsOn == nil || h.ChangeOn == nil {
			return ErrUnavailable
		}
		m.executionHolds = h
		return nil
	}})
}

func (m *Module) lockHoldPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, extra ...guid.ID) error {
	_, err := m.holdPrincipalFenceOn(ctx, tx, tenant, project, work, extra...)
	return err
}

// Discover without source locks, acquire principals in one order, then verify
// under the project fence. A newly committed runtime must cause retry rather
// than acquiring a new principal after the project lock.
func (m *Module) holdPrincipalFenceOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID, extra ...guid.ID) (func() error, error) {
	if m.principals.Lock == nil {
		return nil, ErrUnavailable
	}
	if work > 0 {
		ids, err := m.hiddenAcceptancePrincipalsOn(ctx, tx, tenant, project, work)
		if err != nil {
			return nil, err
		}
		extra = append(extra, ids...)
	}
	if m.executionHolds.PrincipalsOn != nil {
		ids, err := m.executionHolds.PrincipalsOn(ctx, tx, tenant, project, 0)
		if err != nil {
			return nil, err
		}
		extra = append(extra, ids...)
	}
	slices.Sort(extra)
	extra = slices.Compact(extra)
	for _, id := range extra {
		if id <= 0 {
			return nil, ErrWorkForbidden
		}
		if err := m.principals.Lock(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	return func() error {
		if m.executionHolds.PrincipalsOn == nil {
			return nil
		}
		ids, err := m.executionHolds.PrincipalsOn(ctx, tx, tenant, project, 0)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !slices.Contains(extra, id) {
				return ErrWorkVersion
			}
		}
		return nil
	}, nil
}
