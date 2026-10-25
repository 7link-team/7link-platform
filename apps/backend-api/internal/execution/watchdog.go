package execution

import (
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"context"
	"gorm.io/gorm"
	"time"
)

const watchdogJob = "execution.lease_watchdog"

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: watchdogJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		db, err := m.writer()
		if err != nil {
			return err
		}
		q := query.BindExecutions(db, m.names)
		// Cursor through all active rows: CREATED without a lease still needs machine
		// revocation/offline checks and must not starve later expired leases.
		cursor := int64(0)
		for {
			rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.ID.Gt(guid.ID(cursor)), q.State.In("CREATED", "RUNNING", "PAUSED")).Order(q.ID).Limit(100).Find()
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				if e = m.expirePreviews(ctx, db, job.TenantID, time.Now().UTC()); e != nil {
					return e
				}
				if e = m.expireMerges(ctx, db, job.TenantID, time.Now().UTC()); e != nil {
					return e
				}
				return m.expireVerificationTasks(ctx, db, job.TenantID, time.Now().UTC().Truncate(time.Millisecond))
			}
			for _, row := range rows {
				if e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					return m.ExpireOn(ctx, tx, job.TenantID, row.ID, time.Now().UTC().Truncate(time.Millisecond))
				}); e != nil {
					return e
				}
				cursor = int64(row.ID)
			}
		}
	}}}
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: watchdogJob, Kind: watchdogJob, Interval: 5 * time.Second, TenantScope: jobs.ScopePerTenant}}
}
