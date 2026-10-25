package fleet

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var ErrVersionConflict = errors.New("fleet: version conflict")

// controlAuthorityOn fences current Human authority before taking business locks.
func (m *Module) controlAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrMachineForbidden
	}
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.policy == nil {
		return ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrMachineForbidden
	}
	c, err := m.policy(ctx, tx, tenant, permission, actor)
	if err != nil {
		return err
	}
	if !c.AllowAll {
		return ErrMachineForbidden
	}
	return nil
}

// SetDrainOn is replayable against the resulting version. Draining prevents new
// reservations and preserves existing ones, including while a machine is offline.
func (m *Module) SetDrainOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, version int, drain bool) (MachineFact, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return MachineFact{}, err
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); err != nil {
		return MachineFact{}, err
	}
	ctx = humanMachineContext(ctx)
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return MachineFact{}, err
	}
	if row.State == "REVOKED" {
		return MachineFact{}, ErrMachineRevoked
	}
	if row.Version != version {
		return MachineFact{}, ErrVersionConflict
	}
	if row.DrainRequested == drain {
		return m.MachineByIDOn(ctx, tx, tenant, machine)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if machineState(row, now) == "OFFLINE" && row.State != "OFFLINE" {
		if err = m.ExpireMachineOn(ctx, tx, tenant, machine, now); err != nil {
			return MachineFact{}, err
		}
		row, err = m.machineOn(ctx, tx, tenant, machine, true)
		if err != nil {
			return MachineFact{}, err
		}
	}
	state := machineState(row, now)
	if state != "OFFLINE" {
		if drain {
			state = "DRAINING"
		} else {
			state = "ONLINE"
		}
	}
	q := query.BindMachines(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(machine)).Updates(map[string]any{"drain_requested": drain, "state": state, "version": row.Version + 1, "updated_at": now}); err != nil {
		return MachineFact{}, err
	}
	if err = m.machineEventOn(ctx, tx, tenant, machine, "machine.state_changed", row.Version+1, map[string]any{"state": state, "drain_requested": drain}); err != nil {
		return MachineFact{}, err
	}
	return m.MachineByIDOn(ctx, tx, tenant, machine)
}

// ExpireMachineOn rechecks under the machine lock, so a fresh heartbeat wins
// over a stale watchdog scan. It never releases slots or infers task results.
func (m *Module) ExpireMachineOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, now time.Time) error {
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return err
	}
	if row.State != "ONLINE" && row.State != "DRAINING" {
		return nil
	}
	if machineState(row, now) != "OFFLINE" {
		return nil
	}
	q := query.BindMachines(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(machine)).Updates(map[string]any{"state": "OFFLINE", "version": row.Version + 1, "updated_at": now}); err != nil {
		return err
	}
	s := query.BindSessions(tx, m.names)
	if _, err = s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.MachineID.Eq(machine), s.State.Eq("ACTIVE")).Updates(map[string]any{"state": "EXPIRED", "closed_at": now}); err != nil {
		return err
	}
	return m.machineEventOn(ctx, tx, tenant, machine, "machine.state_changed", row.Version+1, map[string]any{"state": "OFFLINE", "reason": "HEARTBEAT_EXPIRED"})
}
