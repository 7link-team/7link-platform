package intake

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type PendingTriageFact struct {
	ID, ProjectID guid.ID
	Title         string
	CreatedAt     time.Time
}

// PendingTriage exposes only qualifying persisted change/rework sources. The
// generic RECEIVED state does not imply a conversion recommendation.
type PendingTriage func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]PendingTriageFact, error)

type NotificationSource func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, bool, error)

type TriagedNotificationSource NotificationSource

// TriageReminderSource exposes the actual source deadline without recipient data.
type TriageReminderSource func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, guid.ID, time.Time, bool, error)

func (m *Module) triageReminderSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, guid.ID, time.Time, bool, error) {
	if tx == nil || tenant <= 0 || id <= 0 {
		return 0, 0, time.Time{}, false, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, 0, time.Time{}, false, err
	}
	if r.ProjectID == nil || r.Status != "RECEIVED" || r.LatestCardRevision != 0 {
		return 0, 0, time.Time{}, false, nil
	}
	return *r.ProjectID, r.ChannelID, r.ReceivedAt.Add(30 * time.Minute), true, nil
}

func (m *Module) triagedNotificationSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, guid.ID, bool, error) {
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.LatestCardRevision.Gte(1)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if row.ProjectID == nil {
		return 0, 0, false, nil
	}
	return *row.ProjectID, row.ChannelID, true, nil
}

func (m *Module) notificationSourceOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (guid.ID, guid.ID, bool, error) {
	q := query.BindRecords(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if row.ProjectID == nil {
		return 0, 0, false, nil
	}
	return *row.ProjectID, row.ChannelID, true, nil
}

func (m *Module) provideNotificationFacts(s *module.Services) error {
	if err := module.Provide[TriageReminderSource](s, m.triageReminderSourceOn); err != nil {
		return err
	}
	if err := module.Provide[TriagedNotificationSource](s, m.triagedNotificationSourceOn); err != nil {
		return err
	}
	if err := module.Provide[NotificationSource](s, m.notificationSourceOn); err != nil {
		return err
	}
	return module.Provide[PendingTriage](s, m.pendingTriageOn)
}

func (m *Module) pendingTriageOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]PendingTriageFact, error) {
	if tx == nil || tenant <= 0 || project < 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindRecords(tx, m.names)
	stmt := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.Status.In("RECEIVED", "TRIAGED"))
	if project > 0 {
		stmt = stmt.Where(q.ProjectID.Eq(project))
	}
	rows, err := stmt.Order(q.ReceivedAt, q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []PendingTriageFact{}
	for _, row := range rows {
		if row.ProjectID == nil || (row.Kind != "CHANGE" && (row.OriginAcceptanceID == nil || *row.OriginAcceptanceID <= 0)) {
			continue
		}
		for _, permission := range []string{"project_read", "intake_read", "intake_convert"} {
			err = m.changeSourceAuthority(ctx, tx, tenant, *row.ProjectID, permission)
			if err != nil {
				break
			}
		}
		if errors.Is(err, ErrForbidden) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, PendingTriageFact{ID: row.ID, ProjectID: *row.ProjectID, Title: row.Title, CreatedAt: row.ReceivedAt})
	}
	return out, nil
}
