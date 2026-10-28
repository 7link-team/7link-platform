package clientwork

import (
	activityi18n "7link.com/platform/internal/clientwork/i18n"
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"gorm.io/gorm"
)

const expiryJob = "clientwork.expiry"

func (m *Module) JobHandlers() []jobs.Handler {
	return append(append([]jobs.Handler{{DisplayNameKey: activityi18n.Namespace + ".activity_job_clientwork_expiry_name", DescriptionKey: activityi18n.Namespace + ".activity_job_clientwork_expiry_description", Kind: expiryJob, Timeout: time.Minute, Run: func(ctx context.Context, job jobs.Job) error {
		if job.TenantID <= 0 {
			return jobs.Permanent(ErrInvalid)
		}
		return m.expireDue(ctx, job.TenantID, now())
	}}}, m.forecastHandler(), m.hiddenVerifierHandler(), m.packageBundleHandler(), m.testReportHandler()), m.dispatchHandlers()...)
}
func (*Module) JobSchedules() []jobs.Schedule {
	return []jobs.Schedule{{Name: expiryJob, Kind: expiryJob, Interval: 30 * time.Second, TenantScope: jobs.ScopePerTenant}, {Name: toleranceForecastJob, Kind: toleranceForecastJob, Interval: time.Hour, TenantScope: jobs.ScopePerTenant}, {Name: dispatchScanJob, Kind: dispatchScanJob, Interval: 5 * time.Minute, TenantScope: jobs.ScopePerTenant}, {Name: dispatchStarvationJob, Kind: dispatchStarvationJob, Interval: 10 * time.Minute, TenantScope: jobs.ScopePerTenant}}
}

// Expiry is a system state transition, independent of a past requester's
// remaining authority. Read-time TTL checks continue to deny access immediately.
func (m *Module) expireDue(ctx context.Context, tenant guid.ID, at time.Time) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	db := m.writer()
	if db == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	d := query.BindDispatchProposal(db, m.names)
	proposals, e := d.WithContext(ctx).Where(d.TenantID.Eq(tenant), d.State.Eq("PROPOSED"), d.ExpiresAt.Lte(at)).Order(d.ExpiresAt, d.ID).Limit(200).Find()
	if e != nil {
		return e
	}
	s := query.BindPreviewShare(db, m.names)
	shares, e := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.State.Eq("ACTIVE"), s.ExpiresAt.Lte(at)).Order(s.ExpiresAt, s.ID).Limit(200).Find()
	if e != nil {
		return e
	}
	var failures []error
	if err := m.advanceAcceptanceReminders(ctx, tenant, at); err != nil {
		failures = append(failures, err)
	}
	if err := m.advanceClarifications(ctx, tenant, at); err != nil {
		failures = append(failures, err)
	}
	for _, row := range proposals {
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			q := query.BindDispatchProposal(tx, m.names)
			changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("PROPOSED"), q.ExpiresAt.Lte(at)).UpdateSimple(q.State.Value("EXPIRED"), q.ActiveSlot.Null(), q.Version.Add(1))
			if e != nil || changed.RowsAffected == 0 {
				return e
			}
			if e = m.recordDispatchExpirationOn(ctx, tx, row, at); e != nil {
				return e
			}
			return m.expiryEventOn(ctx, tx, tenant, row.ProjectID, row.ID, "dispatch_expired", row.Version+1, at)
		})
		if e != nil {
			failures = append(failures, e)
		}
	}
	for _, row := range shares {
		e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			q := query.BindPreviewShare(tx, m.names)
			changed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("ACTIVE"), q.ExpiresAt.Lte(at)).UpdateSimple(q.State.Value("EXPIRED"), q.ActiveSlot.Null(), q.Version.Add(1))
			if e != nil || changed.RowsAffected == 0 {
				return e
			}
			return m.expiryEventOn(ctx, tx, tenant, row.ProjectID, row.ID, "preview_share_ended", row.Version+1, at)
		})
		if e != nil {
			failures = append(failures, e)
		}
	}
	return errors.Join(failures...)
}
func (m *Module) expiryEventOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID, kind string, version int, at time.Time) error {
	detail := map[string]any{"project_id": project.String(), "version": version, "authority_source": "system:ttl_expiry", "expired_at": at.UTC().Format(time.RFC3339Nano)}
	if e := m.audit(ctx, tx, audit.DomainEvent{TenantID: tenant, EventType: "clientwork." + kind, TargetType: Name, TargetID: id, Detail: detail}); e != nil {
		return e
	}
	_, e := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "clientwork." + kind, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "system:ttl_expiry"}, Subject: events.Subject{Kind: Name, ID: id}, SubjectVersion: int64(version), Data: detail})
	return e
}

var _ jobs.HandlerProvider = (*Module)(nil)
var _ jobs.ScheduleProvider = (*Module)(nil)
