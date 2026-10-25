package execution

import (
	"context"
	"math"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/model"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// Cancellation intent is canonical. A delivered attempt gets a real 30-second
// cancellation window, then its UNKNOWN outcome is fenced before source work
// is detached. This records no Runner ACK, model success or evidence.
func (m *Module) reconcileRetirementExecutionsOn(ctx context.Context, tx *gorm.DB, tenant, agent, retirement guid.ID, detach func(context.Context, *gorm.DB, delivery.ExecutionWorkLink) error) (bool, error) {
	if m.handover.authority.AuthorizeArtifactOn == nil || detach == nil {
		return false, ErrCapabilityUnavailable
	}
	if err := m.handover.authority.AuthorizeArtifactOn(ctx, tx, tenant, agent, retirement); err != nil {
		return false, err
	}
	rows, err := m.retirementExecutionRowsOn(ctx, tx, tenant, agent)
	if err != nil {
		return false, err
	}
	ready := true
	for _, snapshot := range rows {
		if snapshot.BranchReason == "HANDOVER" {
			if err := m.cancelHandoverOn(ctx, tx, tenant, agent, retirement, snapshot.ID); err != nil {
				return false, err
			}
			continue
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if snapshot.State == "INTERRUPTED" {
			if _, err := m.lockControlOn(ctx, tx, tenant, snapshot.ID, "CANCEL"); err != nil {
				return false, err
			}
		} else {
			v, err := m.CancelOn(ctx, tx, tenant, snapshot.ID, snapshot.Version)
			if err != nil {
				return false, err
			}
			if !active(v.State) {
				continue
			}
			if v.CancelRequestedAt == nil || v.CancelRequestedAt.Add(30*time.Second).After(now) {
				ready = false
				continue
			}
			if err = m.ExpireOn(ctx, tx, tenant, snapshot.ID, now); err != nil {
				return false, err
			}
		}
		row, err := m.rowOn(ctx, tx, tenant, snapshot.ID, true)
		if err != nil {
			return false, err
		}
		if row.State != "INTERRUPTED" || row.ResultState != "UNKNOWN" || row.DispatchEpoch == math.MaxInt64 {
			return false, ErrConflict
		}
		l := delivery.ExecutionWorkLink{TenantID: tenant, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx), InitiatorID: row.InitiatorID}
		if err = detach(ctx, tx, l); err != nil {
			return false, err
		}
		q := query.BindExecutions(tx, m.names)
		changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version)).Updates(map[string]any{"state": StateCanceled, "state_reason": "RETIREMENT_RUNTIME_UNCONFIRMED", "dispatch_epoch": row.DispatchEpoch + 1, "version": row.Version + 1, "updated_at": now})
		if err != nil {
			return false, err
		}
		if changed.RowsAffected != 1 {
			return false, ErrConflict
		}
		row.State, row.StateReason, row.Version, row.DispatchEpoch = StateCanceled, "RETIREMENT_RUNTIME_UNCONFIRMED", row.Version+1, row.DispatchEpoch+1
		if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "permission:execution_stop", map[string]any{"state": row.State, "retirement_id": retirement.String(), "result_state": "UNKNOWN", "runner_acknowledged": false}); err != nil {
			return false, err
		}
	}
	return ready, nil
}

// Discovery does not acquire business locks. Callers prelock all principals
// before protocol/project/machine locks, then commands recheck exact identity.
func (m *Module) retirementExecutionRowsOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) ([]*model.Execution, error) {
	if transaction(tx, tenant, agent) != nil {
		return nil, ErrInvalidArgument
	}
	q := query.BindExecutions(tx, m.names)
	var out []*model.Execution
	var after guid.ID
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(after), q.State.In("CREATED", "RUNNING", "PAUSED", "INTERRUPTED")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			v, err := view(row)
			if err != nil {
				return nil, err
			}
			if v.Plan.ExecutorPrincipalID == agent {
				out = append(out, row)
			}
		}
		if len(rows) < 100 {
			return out, nil
		}
		after = rows[len(rows)-1].ID
	}
}
