package execution

import (
	"context"
	"errors"

	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindSpawnCosts(s *module.Services) error {
	if err := m.bindExecutionConsumers(s); err != nil {
		return err
	}
	c, err := module.Resolve[governance.SpawnCostConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.BindExecution == nil {
		return ErrUnavailable
	}
	return c.BindExecution(m.spawnExecutionOn)
}

func (m *Module) spawnExecutionOn(ctx context.Context, tx *gorm.DB, tenant, project, execution guid.ID) (guid.ID, bool, error) {
	if transaction(tx, tenant, project) != nil || execution <= 0 {
		return 0, false, ErrInvalidArgument
	}
	if m == nil || m.names.Executions.IsZero() {
		return 0, false, ErrUnavailable
	}
	q := query.BindExecutions(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(execution)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.AssignmentID, true, nil
}

func (m *Module) bindExecutionConsumers(s *module.Services) error {
	c, e := module.Resolve[fleet.EnsembleConnector](s)
	if e == nil {
		if c.BindRuntime != nil {
			if e = m.bindEnsembleRuntime(c); e != nil {
				return e
			}
		}
		if c.BindExecution == nil {
			return ErrUnavailable
		}
		if e = c.BindExecution(m.ensembleExecutionOn); e != nil {
			return e
		}
	} else if !errors.Is(e, module.ErrServiceMissing) {
		return e
	}
	r, e := module.Resolve[yunxiao.RuntimeConnector](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if r.BindExecution == nil {
		return ErrUnavailable
	}
	return r.BindExecution(m.runtimeOn)
}
func (m *Module) ensembleExecutionOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (fleet.EnsembleExecution, error) {
	var out fleet.EnsembleExecution
	if transaction(tx, tenant, project) != nil || id <= 0 {
		return out, fleet.ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, fleet.ErrMachineNotFound
	}
	if e != nil {
		return out, e
	}
	v, e := view(r)
	if e != nil {
		return out, e
	}
	return fleet.EnsembleExecution{ID: r.ID, WorkItemID: r.WorkItemID, PrincipalID: v.Plan.ExecutorPrincipalID, State: r.State, ResultState: r.ResultState}, nil
}

// Runtime is the interval between committed runner receipts. Missing receipts
// remain unavailable; creation/updated timestamps are never runtime substitutes.
func (m *Module) runtimeOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) (yunxiao.RuntimeFact, bool, error) {
	var out yunxiao.RuntimeFact
	if transaction(tx, tenant, project) != nil || id <= 0 {
		return out, false, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if r.State != "SUCCEEDED" && r.State != "FAILED" && r.State != "CANCELLED" { //nolint:misspell // Frozen protocol state.
		return out, false, nil
	}
	v, e := view(r)
	if e != nil {
		return out, false, e
	}
	if v.Plan.ExecutorPrincipalID <= 0 {
		return out, false, nil
	}
	receipts := query.BindReceipts(tx, m.names)
	start, e := receipts.WithContext(ctx).Where(receipts.TenantID.Eq(tenant), receipts.ExecutionID.Eq(id), receipts.Kind.Eq("ACCEPTED")).Order(receipts.CreatedAt, receipts.ID).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	finish, e := receipts.WithContext(ctx).Where(receipts.TenantID.Eq(tenant), receipts.ExecutionID.Eq(id), receipts.Kind.In("COMPLETED", "FAILED", "CANCELLED"), receipts.State.Eq(r.State)).Order(receipts.CreatedAt, receipts.ID).First() //nolint:misspell // Frozen protocol state.
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if finish.CreatedAt.Before(start.CreatedAt) {
		return out, false, ErrUnavailable
	}
	return yunxiao.RuntimeFact{ExecutionID: id, ProjectID: project, WorkItemID: r.WorkItemID, PrincipalID: v.Plan.ExecutorPrincipalID, StartedAt: start.CreatedAt, FinishedAt: finish.CreatedAt}, true, nil
}
