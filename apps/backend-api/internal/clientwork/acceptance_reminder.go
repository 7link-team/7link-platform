package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (m *Module) scheduleAcceptanceRemindersOn(ctx context.Context, tx *gorm.DB, p, id guid.ID) error {
	row, err := m.packageOn(ctx, tx, p, id, false)
	if err != nil {
		return err
	}
	if row.State != "PUBLISHED" || row.ArchivedAt != nil {
		return ErrConflict
	}
	for _, day := range []int{3, 7, 12, 14} {
		rid, e := m.nextID()
		if e != nil {
			return e
		}
		kind := "REMINDER"
		if day == 14 {
			kind = "ESCALATION"
		}
		reminder := &model.AcceptanceReminder{TenantID: row.TenantID, ID: rid, ProjectID: p, PackageID: id, PackageVersion: row.Version, ContentHash: row.ContentHash, RecipientID: row.RecipientID, Day: day, Kind: kind, PublishedAt: row.UpdatedAt, DueAt: row.UpdatedAt.Add(time.Duration(day) * 24 * time.Hour)}
		if e = query.BindAcceptanceReminder(tx, m.names).WithContext(ctx).Create(reminder); e != nil {
			return e
		}
	}
	return nil
}

// Package locking serializes receipt/archival with reminders. Repeated and
// delayed sweeps emit each frozen ordinal once; nothing creates an acceptance.
func (m *Module) advanceAcceptanceReminders(ctx context.Context, tenant guid.ID, at time.Time) error {
	if tenant <= 0 {
		return ErrInvalid
	}
	db := m.writer()
	if db == nil || m.events == nil || m.audit == nil || m.principals.Lock == nil {
		return ErrUnavailable
	}
	q := query.BindAcceptanceReminder(db, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.DueAt.Lte(at), q.EmittedAt.IsNull(), q.CancelledAt.IsNull()).Order(q.DueAt, q.ID).Limit(200).Find()
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range rows {
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if m.acceptanceProjects.CurrentOn == nil {
				return ErrUnavailable
			}
			if e := m.principals.Lock(ctx, tx, candidate.RecipientID); e != nil {
				return e
			}
			active, e := m.acceptanceProjects.CurrentOn(ctx, tx, tenant, candidate.ProjectID, candidate.PackageID, false)
			if e != nil {
				return e
			}
			p := query.BindDeliveryPackage(tx, m.names)
			pkg, e := p.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(candidate.ProjectID), p.ID.Eq(candidate.PackageID)).Take()
			if e != nil {
				return e
			}
			q := query.BindAcceptanceReminder(tx, m.names)
			row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(candidate.ID)).Take()
			if e != nil {
				return e
			}
			if row.EmittedAt != nil || row.CancelledAt != nil || at.Before(row.DueAt) {
				return nil
			}
			if !active || pkg.State != "PUBLISHED" || pkg.ArchivedAt != nil || pkg.Version != row.PackageVersion || pkg.ContentHash != row.ContentHash || pkg.RecipientID != row.RecipientID {
				_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Update(q.CancelledAt, at)
				return e
			}
			r := query.BindPackageReceipt(tx, m.names)
			receipts, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.PackageID.Eq(pkg.ID)).Count()
			if e != nil {
				return e
			}
			if receipts != 0 {
				_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID)).Update(q.CancelledAt, at)
				return e
			}
			if row.Kind == "REMINDER" {
				if m.clients == nil {
					return ErrUnavailable
				}
				current, e := m.clients.IsClientOn(ctx, tx, tenant, row.ProjectID, row.RecipientID)
				if e != nil {
					return e
				}
				if !current {
					return nil
				}
				// Recipient principal lock serializes this throttle across projects.
				recent, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.RecipientID.Eq(row.RecipientID), q.Kind.Eq("REMINDER"), q.EmittedAt.Gt(at.Add(-24*time.Hour))).Limit(1).Find()
				if e != nil {
					return e
				}
				if len(recent) > 0 {
					return nil
				}
			}
			kind := "acceptance_reminder"
			if row.Kind == "ESCALATION" {
				kind = "acceptance_overdue"
			}
			if row.Kind == "ESCALATION" {
				if _, e = m.acceptanceProjects.CurrentOn(ctx, tx, tenant, row.ProjectID, row.PackageID, true); e != nil {
					return e
				}
			}
			if e = m.acceptanceReminderEventOn(ctx, tx, row, kind); e != nil {
				return e
			}
			_, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(row.ID), q.EmittedAt.IsNull()).Update(q.EmittedAt, at)
			return e
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Read-side routing rechecks current package and client membership; it exposes
// no notes, artifacts or signer evidence. The notice owner selects current leads.
type AcceptanceReminderFact struct {
	ProjectID, PackageID, RecipientID guid.ID
	Day                               int
	Escalation                        bool
	DueAt                             time.Time
}
type AcceptanceReminderFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (AcceptanceReminderFact, bool, error)

func (m *Module) acceptanceReminderFactsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (AcceptanceReminderFact, bool, error) {
	var out AcceptanceReminderFact
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return out, false, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, false, ErrInvalid
	}
	q := query.BindAcceptanceReminder(tx, m.names)
	row, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if row.EmittedAt == nil || row.CancelledAt != nil {
		return out, false, nil
	}
	if m.acceptanceProjects.CurrentOn == nil {
		return out, false, ErrUnavailable
	}
	active, e := m.acceptanceProjects.CurrentOn(ctx, tx, tenant, row.ProjectID, row.PackageID, false)
	if e != nil || !active {
		return out, false, e
	}
	p := query.BindDeliveryPackage(tx, m.names)
	pkg, e := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ID.Eq(row.PackageID), p.ProjectID.Eq(row.ProjectID)).Take()
	if e != nil {
		return out, false, e
	}
	if pkg.State != "PUBLISHED" || pkg.ArchivedAt != nil || pkg.Version != row.PackageVersion || pkg.ContentHash != row.ContentHash || pkg.RecipientID != row.RecipientID {
		return out, false, nil
	}
	r := query.BindPackageReceipt(tx, m.names)
	count, e := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.PackageID.Eq(pkg.ID)).Count()
	if e != nil || count > 0 {
		return out, false, e
	}
	if m.clients == nil {
		return out, false, ErrUnavailable
	}
	active, e = m.clients.IsClientOn(ctx, tx, tenant, row.ProjectID, row.RecipientID)
	if e != nil || !active {
		return out, false, e
	}
	return AcceptanceReminderFact{row.ProjectID, row.PackageID, row.RecipientID, row.Day, row.Kind == "ESCALATION", row.DueAt}, true, nil
}

func (m *Module) acceptanceReminderEventOn(ctx context.Context, tx *gorm.DB, row *model.AcceptanceReminder, kind string) error {
	detail := map[string]any{"recipient_id": row.RecipientID.String(), "project_id": row.ProjectID.String(), "package_id": row.PackageID.String(), "day": row.Day, "due_at": row.DueAt.UTC().Format(time.RFC3339Nano), "authority_source": "system:acceptance_schedule"}
	if err := m.audit(ctx, tx, audit.DomainEvent{TenantID: row.TenantID, EventType: "clientwork." + kind, TargetType: Name, TargetID: row.ID, Detail: detail}); err != nil {
		return err
	}
	_, err := m.events.Publish(ctx, tx, events.Event{TenantID: row.TenantID, ProjectID: row.ProjectID, Type: "clientwork." + kind, Actor: events.Actor{Kind: "SYSTEM", AuthoritySource: "system:acceptance_schedule"}, Subject: events.Subject{Kind: Name, ID: row.ID}, SubjectVersion: int64(row.Day), Data: detail})
	return err
}
