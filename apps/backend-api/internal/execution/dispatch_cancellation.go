package execution

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) bindCancellation(s *module.Services) error {
	if err := m.bindDispatch(s); err != nil {
		return err
	}
	c, err := module.Resolve[delivery.ProjectCancellationConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		slog.Warn("project cancellation unavailable; enable the Delivery cancellation provider")
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil || c.AuthorizeReceiptOn == nil {
		return ErrCapabilityUnavailable
	}
	if err = c.Bind(delivery.ProjectExecutionCancellation{PrincipalsOn: m.projectCancellationPrincipalsOn, CancelOn: m.cancelProjectExecutionsOn}); err != nil {
		return err
	}
	authorize := m.work.AuthorizeOn
	m.work.AuthorizeOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
		if op == "DISPATCH" || op == "RECEIPT" || op == "CONTROL_RECEIPT" {
			r, e := m.rowOn(ctx, tx, l.TenantID, l.ExecutionID, false)
			if e != nil {
				return e
			}
			if r.CancelRequestedAt != nil {
				return c.AuthorizeReceiptOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, AssignmentID: l.AssignmentID, ExecutionID: l.ExecutionID, ActorID: l.ActorID, InitiatorID: l.InitiatorID})
			}
		}
		if authorize == nil {
			return ErrCapabilityUnavailable
		}
		return authorize(ctx, tx, l, op)
	}
	return nil
}

func (m *Module) bindDispatch(s *module.Services) error {
	c, err := module.Resolve[delivery.DispatchExecutionConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrCapabilityUnavailable
	}
	return c.Bind(delivery.DispatchExecutionAccess{PrincipalsOn: m.dispatchStopPrincipalsOn, StopOn: m.dispatchStopOn, QueuesOn: m.dispatchQueuesOn})
}

func (m *Module) dispatchStopPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, executionID guid.ID) ([]guid.ID, error) {
	if tx == nil || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrInvalidArgument
	}
	row, err := m.rowOn(ctx, tx, tenant, executionID, false)
	if err != nil {
		return nil, err
	}
	if row.ProjectID != project || row.WorkItemID != workID {
		return nil, ErrNotFound
	}
	if m.machines.ByIDOn == nil || m.work.PrincipalsOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, row.MachineID)
	if err != nil {
		return nil, err
	}
	ids, err := m.work.PrincipalsOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)))
	if err != nil {
		return nil, err
	}
	ids = append(ids, row.InitiatorID, machine.PrincipalID, authn.PrincipalIDFromContext(ctx))
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (m *Module) dispatchStopOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, executionID guid.ID, version int) (delivery.DispatchExecutionState, error) {
	if err := transaction(tx, tenant, executionID); err != nil {
		return delivery.DispatchExecutionState{}, err
	}
	row, err := m.rowOn(ctx, tx, tenant, executionID, false)
	if err != nil {
		return delivery.DispatchExecutionState{}, err
	}
	if row.ProjectID != project || row.WorkItemID != workID {
		return delivery.DispatchExecutionState{}, ErrNotFound
	}
	v, err := m.CancelOn(ctx, tx, tenant, executionID, version)
	if err != nil {
		return delivery.DispatchExecutionState{}, err
	}
	return delivery.DispatchExecutionState{ExecutionID: v.ID, State: v.State, Version: v.Version, CancelRequestedAt: v.CancelRequestedAt}, nil
}

// Pending commands are actual autostart executions not yet leased to a runner.
// This projection does not reserve capacity or manufacture a scheduling queue.
func (m *Module) dispatchQueuesOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, machines []guid.ID) ([]delivery.DispatchQueueFact, error) {
	if tx == nil || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant || len(machines) > 100 {
		return nil, ErrInvalidArgument
	}
	ids := slices.Clone(machines)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make([]delivery.DispatchQueueFact, 0, len(ids))
	q := query.BindExecutions(tx, m.names)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
		n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(id), q.State.Eq("CREATED"), q.Autostart.Is(true), q.LeaseExpiresAt.IsNull(), q.CancelRequestedAt.IsNull()).Count()
		if err != nil {
			return nil, err
		}
		out = append(out, delivery.DispatchQueueFact{MachineID: id, PendingCommands: int(n), ObservedAt: at})
	}
	return out, nil
}

// Discovery is deliberately nonlocking. The caller locks the full sorted
// principal set, then project, and repeats discovery before changing anything.
func (m *Module) projectCancellationPrincipalsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]guid.ID, error) {
	if m == nil || tx == nil || tenant <= 0 || project <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.In("CREATED", "RUNNING", "PAUSED")).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx)}
	for _, row := range rows {
		if m.machines.ByIDOn == nil || m.work.PrincipalsOn == nil {
			return nil, ErrCapabilityUnavailable
		}
		machine, e := m.machines.ByIDOn(ctx, tx, tenant, row.MachineID)
		if e != nil {
			return nil, e
		}
		participants, e := m.work.PrincipalsOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)))
		if e != nil {
			return nil, e
		}
		ids = append(ids, participants...)
		ids = append(ids, row.InitiatorID, machine.PrincipalID)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func (m *Module) cancelProjectExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if tx == nil || tx.Statement == nil {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.In("CREATED", "RUNNING", "PAUSED")).Order(q.MachineID, q.ID).Find()
	if err != nil {
		return err
	}
	// Project serialization prevents new dispatch while machine locks are taken
	// in a stable order. CancelOn keeps current IAM and exact execution fences.
	for _, row := range rows {
		if _, err = m.CancelOn(ctx, tx, tenant, row.ID, row.Version); err != nil {
			return err
		}
	}
	return nil
}

// ForecastFacts reports genuine server-observed execution intervals. Missing
// ACCEPTED/terminal receipts stay unknown rather than borrowing creation times.
type ForecastExecution struct {
	ID, WorkItemID     guid.ID
	StartedAt, EndedAt *time.Time
	Terminal           bool
}
type ForecastFacts struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]ForecastExecution, error)
}

func (m *Module) provideForecastFacts(s *module.Services) error {
	return module.Provide[ForecastFacts](s, ForecastFacts{ReadOn: m.forecastExecutionFactsOn})
}
func (m *Module) forecastExecutionFactsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, at time.Time) ([]ForecastExecution, error) {
	if transaction(tx, tenant, project) != nil || at.IsZero() {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CreatedAt.Lte(at)).Order(q.ID).Find()
	if e != nil {
		return nil, e
	}
	out := make([]ForecastExecution, 0, len(rows))
	for _, row := range rows {
		fact := ForecastExecution{ID: row.ID, WorkItemID: row.WorkItemID, Terminal: slices.Contains([]string{"SUCCEEDED", "FAILED", "CANCELLED", "LOST", "REJECTED"}, row.State)} //nolint:misspell // Frozen protocol state.
		r := query.BindReceipts(tx, m.names)
		starts, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(row.ID), r.Kind.Eq("ACCEPTED"), r.CreatedAt.Lte(at)).Order(r.CreatedAt, r.ID).Limit(1).Find()
		if e != nil {
			return nil, e
		}
		if len(starts) > 0 {
			v := starts[0].CreatedAt
			fact.StartedAt = &v
		}
		ends, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ExecutionID.Eq(row.ID), r.Kind.In("COMPLETED", "FAILED", "CANCELLED"), r.CreatedAt.Lte(at)).Order(r.CreatedAt, r.ID).Limit(1).Find() //nolint:misspell // Frozen protocol state.
		if e != nil {
			return nil, e
		}
		if len(ends) > 0 {
			v := ends[0].CreatedAt
			fact.EndedAt = &v
			fact.Terminal = true
		}
		out = append(out, fact)
	}
	return out, nil
}
