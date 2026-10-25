package fleet

import (
	"context"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// CancelEnrollment authorizes the device using its private enrollment proof.
// Only an undecided request can be withdrawn; an approved machine must use the
// separately authorized revocation flow. The saga lease serializes decisions,
// credential exchange and cancellation, including cancellation replays.
func (m *Module) CancelEnrollment(ctx context.Context, tenant, id guid.ID, proof string) error {
	if tenant <= 0 || id <= 0 || proof == "" || len(proof) > 256 {
		return ErrInvalidArgument
	}
	return m.enrollmentRun(ctx, id, func(ctx context.Context, db *gorm.DB, lease *dlock.Lock) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lease.FenceOn(ctx, tx); err != nil {
				return err
			}
			row, err := m.enrollmentOn(ctx, tx, tenant, id)
			if err != nil {
				return err
			}
			if row.DeviceHash != enrollmentHash(proof) {
				return ErrMachineNotFound
			}
			if row.State == "CANCELLED" { //nolint:misspell // Preserve the published domain or serialized contract spelling.
				return nil
			}
			if row.State != "PENDING" || !row.ExpiresAt.After(time.Now().UTC()) {
				return ErrEnrollmentFailed
			}
			q := query.BindEnrollments(tx, m.names)
			result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.State.Eq("PENDING")).Updates(map[string]any{"state": "CANCELLED", "updated_at": time.Now().UTC().Truncate(time.Millisecond)}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			if err != nil {
				return err
			}
			if result.RowsAffected != 1 {
				return ErrEnrollmentFailed
			}
			return m.enrollmentEventOn(ctx, tx, tenant, id, "CANCELLED") //nolint:misspell // Preserve the published domain or serialized contract spelling.
		})
	})
}
