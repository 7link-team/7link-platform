package jobs

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/infra/jobs/model"
	"7link.com/platform/internal/infra/jobs/query"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// reapExpired scans at most 100 leases using database time, then settles each
// in its own transaction. finish rechecks ownership AND expiry under a row lock
// so a heartbeat or replacement after the scan cannot be overwritten.
func (m *Module) reapExpired(ctx context.Context, onDead func(context.Context, *gorm.DB, DeadLetter) error) (int, error) {
	var candidates []*model.Job
	if err := m.queueTransaction(ctx, nil, func(tx *gorm.DB) error {
		now, err := gormx.DatabaseClockMillis(ctx, tx)
		if err != nil {
			return err
		}
		table := query.BindJobs(tx, m.names)
		candidates, err = table.WithContext(ctx).Where(table.State.Eq("RUNNING"), table.LeaseExpiresAtMS.Lt(now)).Order(table.LeaseExpiresAtMS, table.ID).Limit(100).Find()
		return err
	}); err != nil {
		return 0, err
	}
	recovered := 0
	var failures error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return recovered, errors.Join(failures, err)
		}
		m.mu.RLock()
		handler, known := m.handlers[candidate.Kind]
		m.mu.RUnlock()
		if !known {
			// Disabling a handler leaves its work pending until re-enabled. Reclaim its
			// abandoned lease with the standard policy; never invent an executable handler.
			handler = Handler{BackoffBase: defaultBackoffBase, BackoffMax: defaultBackoffMax}
		}
		changed, err := m.finish(ctx, *candidate, handler, errLeaseExpired, onDead)
		if err != nil {
			failures = errors.Join(failures, fmt.Errorf("recover job %s: %w", candidate.ID, err))
			continue
		}
		if changed {
			recovered++
		}
	}
	return recovered, failures
}
