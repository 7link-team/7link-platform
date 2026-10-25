package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
	"time"
)

// ExpireEnrollment cleans abandoned IAM provisioning through the same durable
// saga lease. A credential never used to open a session is revoked after60s.
// Machine denial commits before IAM cleanup; a failed cleanup stays retryable.
func (m *Module) ExpireEnrollment(ctx context.Context, tenant, id guid.ID, now time.Time) error {
	return m.enrollmentRun(ctx, id, func(ctx context.Context, db *gorm.DB, lease *dlock.Lock) error {
		var row *model.Enrollment
		cleanup := false
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := lease.FenceOn(ctx, tx); e != nil {
				return e
			}
			// Match session's machine-before-enrollment lock order. The saga lease
			// prevents provisioning from changing the machine link during this read.
			q0 := query.BindEnrollments(tx, m.names)
			snapshot, e := q0.WithContext(ctx).Where(q0.TenantID.Eq(tenant), q0.ID.Eq(id)).First()
			if e != nil {
				return e
			}
			if snapshot.MachineID != nil {
				if _, e = m.machineOn(ctx, tx, tenant, *snapshot.MachineID, true); e != nil {
					return e
				}
			}
			row, e = m.enrollmentOn(ctx, tx, tenant, id)
			if e != nil {
				return e
			}
			if row.AcknowledgedAt != nil || row.State == "FAILED" || row.State == "DENIED" || row.State == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				return nil
			}
			if row.State != "COMPENSATING" {
				deadline := row.ExpiresAt
				if row.State == "COMPLETE" {
					deadline = row.UpdatedAt.Add(time.Minute)
				}
				if deadline.After(now) {
					return nil
				}
			}
			if row.MachineID != nil {
				machine, e := m.machineOn(ctx, tx, tenant, *row.MachineID, true)
				if e != nil {
					return e
				}
				// A successful authenticated session acknowledges credential delivery.
				if machine.LastSeenAt != nil {
					q := query.BindEnrollments(tx, m.names)
					_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Update(q.AcknowledgedAt, *machine.LastSeenAt)
					return e
				}
				if machine.State != "REVOKED" {
					q := query.BindMachines(tx, m.names)
					if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(machine.ID)).Updates(map[string]any{"state": "REVOKED", "name_key": nil, "version": machine.Version + 1, "updated_at": now}); e != nil {
						return e
					}
					if e = m.machineEventOn(ctx, tx, tenant, machine.ID, "machine.state_changed", machine.Version+1, map[string]any{"state": "REVOKED", "reason": "ENROLLMENT_EXPIRED"}); e != nil {
						return e
					}
				}
			}
			q := query.BindEnrollments(tx, m.names)
			state := "FAILED"
			if row.PrincipalID != nil || row.State == "PROVISIONING" || row.State == "COMPENSATING" {
				state = "COMPENSATING"
				cleanup = true
			}
			if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Updates(map[string]any{"state": state, "updated_at": now}); e != nil {
				return e
			}
			return m.enrollmentEventOn(ctx, tx, tenant, id, state)
		})
		if err != nil || !cleanup {
			return err
		}
		account, found, err := m.enrollmentIAM.FindAccount(ctx, tenant, "machine-"+id.String())
		if err != nil {
			return err
		}
		if found {
			if err = m.enrollmentIAM.DeleteAccount(ctx, tenant, account.ID, account.Version); err != nil {
				return err
			}
		}
		return m.enrollmentUpdate(ctx, db, lease, tenant, id, map[string]any{"state": "FAILED"})
	})
}
