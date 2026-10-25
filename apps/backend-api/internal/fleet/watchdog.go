package fleet

import (
	"context"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"gorm.io/gorm"
)

const watchdogJob = "fleet.heartbeat_watchdog"

func (m *Module) JobHandlers() []jobs.Handler {
	return []jobs.Handler{{Kind: watchdogJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalidArgument)
		}
		db, err := m.writer()
		if err != nil {
			return err
		}
		q := query.BindMachines(db, m.names)
		now := time.Now().UTC().Truncate(time.Millisecond)
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(job.TenantID), q.State.In("ONLINE", "DRAINING"), q.LastSeenAt.Lt(now.Add(-30*time.Second))).Order(q.ID).Limit(200).Find()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.ExpireMachineOn(ctx, tx, job.TenantID, row.ID, now) }); err != nil {
				return err
			}
		}
		enrollments := query.BindEnrollments(db, m.names)
		// Scan only due, unacknowledged coordination records in bounded pages.
		cursor := guid.ID(0)
		for {
			pending, e := enrollments.WithContext(ctx).Where(enrollments.TenantID.Eq(job.TenantID), enrollments.ID.Gt(cursor), enrollments.AcknowledgedAt.IsNull(), enrollments.State.NotIn("FAILED", "DENIED", "CANCELLED")).Where(enrollments.WithContext(ctx).Where(enrollments.State.Eq("COMPENSATING")).Or(enrollments.State.Eq("COMPLETE"), enrollments.UpdatedAt.Lte(now.Add(-time.Minute))).Or(enrollments.State.Neq("COMPLETE"), enrollments.ExpiresAt.Lte(now))).Order(enrollments.ID).Limit(100).Find() //nolint:misspell // Preserve the published domain or serialized contract spelling.
			if e != nil {
				return e
			}
			if len(pending) == 0 {
				break
			}
			for _, enrollment := range pending {
				if err = m.ExpireEnrollment(ctx, job.TenantID, enrollment.ID, now); err != nil {
					return err
				}
				cursor = enrollment.ID
			}
		}
		if err = m.expireTerminalSessions(ctx, db, job.TenantID, now); err != nil {
			return err
		}
		if err = m.expireInspections(ctx, db, job.TenantID, now); err != nil {
			return err
		}
		if err = m.expireAgentResets(ctx, db, job.TenantID, now); err != nil {
			return err
		}
		if err = m.expireAgentMachineCommands(ctx, db, job.TenantID, now); err != nil {
			return err
		}
		return m.expireCredentialRotations(ctx, db, job.TenantID, now)
	}}}
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: watchdogJob, Kind: watchdogJob, Interval: 5 * time.Second, TenantScope: jobs.ScopePerTenant}}
}
func (m *Module) writer() (*gorm.DB, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil {
		return nil, ErrUnavailable
	}
	return m.runtime.Database().Write(), nil
}

var (
	_ jobs.HandlerProvider  = (*Module)(nil)
	_ jobs.ScheduleProvider = (*Module)(nil)
)
