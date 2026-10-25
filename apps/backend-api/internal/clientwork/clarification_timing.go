package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// advanceClarifications checks persisted deadlines. A reminder is an in-app
// event only; committing the outbox is never recorded as an email receipt.
func (m *Module) advanceClarifications(ctx context.Context, tenant guid.ID, at time.Time) error {
	db := m.writer()
	if db == nil || m.audit == nil || m.events == nil {
		return ErrUnavailable
	}
	q := query.BindClarificationRound(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.State.Eq("OPEN"), q.DueAt.IsNotNull()).Where(q.WithContext(ctx).Where(q.DueAt.Lte(at)).Or(q.NextReminderAt.Lte(at))).Order(q.DueAt, q.ID).Limit(100).Find()
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range rows {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			q := query.BindClarificationRound(tx, m.names)
			row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(candidate.ID)).Take()
			if err != nil {
				return err
			}
			if row.State != "OPEN" || row.DueAt == nil {
				return nil
			}
			if !at.Before(*row.DueAt) {
				if m.intakeSources.ExpireClarificationOn == nil {
					return ErrUnavailable
				}
				version, err := m.intakeSources.ExpireClarificationOn(ctx, tx, tenant, row.ProjectID, row.IntakeID, row.ID, at)
				if err != nil {
					return err
				}
				changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("OPEN")).Updates(map[string]any{"state": "EXPIRED", "intake_version": version, "closed_at": at, "close_reason": "DEADLINE_ELAPSED", "version": row.Version + 1, "next_reminder_at": nil, "updated_at": at})
				if err != nil {
					return err
				}
				if changed.RowsAffected != 1 {
					return ErrConflict
				}
				return m.expiryEventOn(ctx, tx, tenant, row.ProjectID, row.ID, "clarification_expired", row.Version+1, at)
			}
			if row.NextReminderAt == nil || at.Before(*row.NextReminderAt) {
				return nil
			}
			if row.ReminderHours == nil || row.MaxReminders == nil || *row.ReminderHours < 1 || row.RemindersSent >= *row.MaxReminders {
				return ErrConflict
			}
			// Catch up each due ordinal once, without sending beyond the cap or due date.
			for row.NextReminderAt != nil && !at.Before(*row.NextReminderAt) && row.NextReminderAt.Before(*row.DueAt) && row.RemindersSent < *row.MaxReminders {
				row.RemindersSent++
				if err := m.expiryEventOn(ctx, tx, tenant, row.ProjectID, row.ID, "clarification_reminder", row.RemindersSent, *row.NextReminderAt); err != nil {
					return err
				}
				next := row.CreatedAt.Add(time.Duration((row.RemindersSent+1)**row.ReminderHours) * time.Hour)
				row.NextReminderAt = nil
				if row.RemindersSent < *row.MaxReminders && next.Before(*row.DueAt) {
					row.NextReminderAt = &next
				}
			}
			// Reminders do not invalidate the client's outstanding answer version.
			changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.Version.Eq(row.Version), q.State.Eq("OPEN")).Updates(map[string]any{"reminders_sent": row.RemindersSent, "next_reminder_at": row.NextReminderAt})
			if err != nil {
				return err
			}
			if changed.RowsAffected != 1 {
				return ErrConflict
			}
			return nil
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
