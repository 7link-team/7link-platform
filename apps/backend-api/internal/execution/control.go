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

// lockControlOn follows principal -> project/assignment -> machine -> execution.
func (m *Module) lockControlOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, operation string) (*model.Execution, error) {
	snapshot, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	if snapshot.BranchReason == "PLANNING" || snapshot.BranchReason == "PLAN_REVIEW" {
		planningOperation := "CANCEL"
		if operation == "RECOVER" {
			planningOperation = operation
		}
		if _, err = m.authorizePlanningOn(ctx, tx, snapshot, planningOperation); err != nil {
			return nil, err
		}
		if err = m.humanAuthority(ctx, tx, tenant, snapshot.ProjectID, "execution_stop"); err != nil {
			return nil, err
		}
		if err = m.lockMachineOn(ctx, tx, tenant, snapshot.MachineID); err != nil {
			return nil, err
		}
		return m.rowOn(ctx, tx, tenant, id, true)
	}
	if m.machines.ByIDOn == nil {
		return nil, ErrUnavailable
	}
	machine, err := m.machines.ByIDOn(ctx, tx, tenant, snapshot.MachineID)
	if err != nil {
		return nil, err
	}
	if err = m.workPrincipalLocks(ctx, tx, link(snapshot, authn.PrincipalIDFromContext(ctx)), authn.PrincipalIDFromContext(ctx), snapshot.InitiatorID, machine.PrincipalID); err != nil {
		return nil, err
	}
	if err = m.humanAuthority(ctx, tx, tenant, snapshot.ProjectID, "execution_stop"); err != nil {
		return nil, err
	}
	if m.work.AuthorizeOn == nil {
		return nil, ErrCapabilityUnavailable
	}
	if err = m.work.AuthorizeOn(ctx, tx, link(snapshot, authn.PrincipalIDFromContext(ctx)), operation); err != nil {
		return nil, err
	}
	if err = m.lockMachineOn(ctx, tx, tenant, snapshot.MachineID); err != nil {
		return nil, err
	}
	return m.rowOn(ctx, tx, tenant, id, true)
}
func (m *Module) lockMachineOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) error {
	if m.machines.ByIDOn == nil || m.machines.LockOn == nil {
		return ErrUnavailable
	}
	fact, err := m.machines.ByIDOn(ctx, tx, tenant, machine)
	if err != nil {
		return err
	}
	if err = m.principalLocks(ctx, tx, fact.PrincipalID); err != nil {
		return err
	}
	_, err = m.machines.LockOn(ctx, tx, tenant, machine)
	return err
}

func active(state string) bool { return state == "CREATED" || state == "RUNNING" || state == "PAUSED" }

// CancelOn records intent; only a runner acknowledgement confirms cancellation.
// An unleased command has never been delivered and can be canceled immediately.
func (m *Module) CancelOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int) (ExecutionView, error) {
	row, err := m.lockControlOn(ctx, tx, tenant, id, "CANCEL")
	if err != nil {
		return ExecutionView{}, err
	}
	if row.Version != version {
		return ExecutionView{}, ErrConflict
	}
	if row.State == StateCanceled || row.CancelRequestedAt != nil {
		return view(row)
	}
	if !active(row.State) {
		return ExecutionView{}, ErrConflict
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	state, result := row.State, row.ResultState
	if row.State == "CREATED" && row.LeaseExpiresAt == nil {
		state, result = StateCanceled, "CONFIRMED"
		if row.BranchReason == "PLANNING" || row.BranchReason == "PLAN_REVIEW" {
			if m.work.SettleBudgetOn == nil {
				return ExecutionView{}, ErrCapabilityUnavailable
			}
			if err = m.work.SettleBudgetOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: state, Unused: row.DispatchEpoch == 1}); err != nil {
				return ExecutionView{}, err
			}
		}
		if row.BranchReason == "PLANNING" {
			if m.planning.EndOn == nil {
				return ExecutionView{}, ErrCapabilityUnavailable
			}
			if err = m.planning.EndOn(ctx, tx, delivery.ExecutionWorkLink{TenantID: tenant, ProjectID: row.ProjectID, WorkItemID: row.WorkItemID, AssignmentID: row.AssignmentID, ExecutionID: row.ID, ActorID: authn.PrincipalIDFromContext(ctx)}, state); err != nil {
				return ExecutionView{}, err
			}
		} else if row.BranchReason != "PLAN_REVIEW" {
			if m.work.FinishedOn == nil {
				return ExecutionView{}, ErrCapabilityUnavailable
			}
			if err = m.work.FinishedOn(ctx, tx, link(row, authn.PrincipalIDFromContext(ctx)), WorkResult{State: state, Reason: "USER_CANCEL", Unused: row.DispatchEpoch == 1}); err != nil {
				return ExecutionView{}, err
			}
		}
		if m.machines.ReleaseSlotOn == nil {
			return ExecutionView{}, ErrUnavailable
		}
		if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, row.MachineID, id); err != nil {
			return ExecutionView{}, err
		}
	}
	q := query.BindExecutions(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": state, "state_reason": "USER_CANCEL", "result_state": result, "cancel_requested_at": now, "version": row.Version + 1, "updated_at": now}); err != nil {
		return ExecutionView{}, err
	}
	row.State, row.StateReason, row.ResultState, row.CancelRequestedAt, row.Version, row.UpdatedAt = state, "USER_CANCEL", result, &now, row.Version+1, now
	if row.BranchReason == "SKILL_DISTILLATION" && state == StateCanceled {
		if m.work.DistillationCompletedOn == nil {
			return ExecutionView{}, ErrCapabilityUnavailable
		}
		if err = m.work.DistillationCompletedOn(ctx, tx, tenant, id); err != nil {
			return ExecutionView{}, err
		}
	}
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "permission:execution_stop", map[string]any{"state": state, "cancel_requested": true}); err != nil {
		return ExecutionView{}, err
	}
	return view(row)
}

// RecoverOn requires an explicit acknowledgement of a potentially still-running
// prior attempt. The immutable plan survives; old epochs and receipts are fenced.
func (m *Module) RecoverOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, version int, acknowledge bool) (ExecutionView, error) {
	if !acknowledge {
		return ExecutionView{}, ErrInvalidArgument
	}
	row, err := m.lockControlOn(ctx, tx, tenant, id, "RECOVER")
	if err != nil {
		return ExecutionView{}, err
	}
	if row.Version != version || row.State != "INTERRUPTED" || row.DispatchEpoch == math.MaxInt64 {
		return ExecutionView{}, ErrConflict
	}
	if m.machines.ReacquireSlotOn == nil {
		return ExecutionView{}, ErrUnavailable
	}
	if _, err = m.machines.ReacquireSlotOn(ctx, tx, tenant, row.MachineID, id); err != nil {
		return ExecutionView{}, err
	}
	v, err := view(row)
	if err != nil {
		return ExecutionView{}, err
	}
	if err = m.workspaceFreeOn(ctx, tx, tenant, row.MachineID, id, v.Plan.WorkdirID); err != nil {
		return ExecutionView{}, err
	}
	q := query.BindExecutions(tx, m.names)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": "CREATED", "state_reason": "RECOVERY_ACKNOWLEDGED", "result_state": "NONE", "dispatch_epoch": row.DispatchEpoch + 1, "last_seq": 0, "lease_expires_at": nil, "cancel_requested_at": nil, "version": row.Version + 1, "updated_at": now}); err != nil {
		return ExecutionView{}, err
	}
	row.State, row.StateReason, row.ResultState, row.DispatchEpoch, row.LastSeq, row.LeaseExpiresAt, row.CancelRequestedAt, row.Version, row.UpdatedAt = "CREATED", "RECOVERY_ACKNOWLEDGED", "NONE", row.DispatchEpoch+1, 0, nil, nil, row.Version+1, now
	if err = m.eventOn(ctx, tx, row, "execution.state_changed", "HUMAN", "permission:execution_stop", map[string]any{"state": row.State, "dispatch_epoch": row.DispatchEpoch, "unknown_result_acknowledged": true}); err != nil {
		return ExecutionView{}, err
	}
	return view(row)
}

// Call only while holding the machine lock. Interrupted attempts retain their
// workspace fence until an explicit recovery decision, even after slot release.
func (m *Module) workspaceFreeOn(ctx context.Context, tx *gorm.DB, tenant, machine, except, workdir guid.ID) error {
	q := query.BindExecutions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Neq(except), q.State.In("CREATED", "RUNNING", "PAUSED", "INTERRUPTED")).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		v, e := view(row)
		if e != nil {
			return e
		}
		if v.Plan.WorkdirID == workdir {
			return ErrConflict
		}
	}
	return nil
}

// ExpireOn records uncertainty; it never automatically retries or reports a
// successful cancel/Claim when a runner disappears or loses its IAM authority.
func (m *Module) ExpireOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, now time.Time) error {
	snapshot, err := m.rowOn(ctx, tx, tenant, id, false)
	if err != nil {
		return err
	}
	if err = m.lockMachineOn(ctx, tx, tenant, snapshot.MachineID); err != nil {
		return err
	}
	fact, err := m.machines.ByIDOn(ctx, tx, tenant, snapshot.MachineID)
	if err != nil {
		return err
	}
	row, err := m.rowOn(ctx, tx, tenant, id, true)
	if err != nil {
		return err
	}
	if !active(row.State) {
		return nil
	}
	reason := ""
	if fact.State == "REVOKED" {
		reason = "MACHINE_REVOKED"
	} else if fact.State == "OFFLINE" {
		reason = "MACHINE_OFFLINE"
	} else if row.CancelRequestedAt != nil && !row.CancelRequestedAt.Add(30*time.Second).After(now) {
		reason = "CANCEL_TIMEOUT"
	} else if row.LeaseExpiresAt != nil && !row.LeaseExpiresAt.After(now) {
		reason = "LEASE_EXPIRED"
	}
	if reason == "" {
		return nil
	}
	if m.machines.ReleaseSlotOn == nil {
		return ErrUnavailable
	}
	if err = m.machines.ReleaseSlotOn(ctx, tx, tenant, row.MachineID, id); err != nil {
		return err
	}
	q := query.BindExecutions(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": "INTERRUPTED", "state_reason": reason, "result_state": "UNKNOWN", "lease_expires_at": nil, "version": row.Version + 1, "updated_at": now}); err != nil {
		return err
	}
	row.State, row.StateReason, row.ResultState, row.Version = "INTERRUPTED", reason, "UNKNOWN", row.Version+1
	return m.eventOn(ctx, tx, row, "execution.state_changed", "SYSTEM", "SYSTEM:watchdog", map[string]any{"state": row.State, "reason": reason, "result_state": "UNKNOWN"})
}
