package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAcceptanceReminderDependencyFailuresPreserveUnemittedSchedule(t *testing.T) {
	for _, fault := range []string{"project_capability", "project_read", "lock", "package_read", "reminder_read", "receipt_count", "client_capability", "client_read", "throttle_read", "write"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			ctx := testContext(t, 2)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, pkg, 1); return e }))
			q := query.BindAcceptanceReminder(db, m.names)
			row, err := q.WithContext(ctx).Order(q.Day).First()
			require.NoError(t, err)
			switch fault {
			case "project_capability":
				m.acceptanceProjects.CurrentOn = nil
			case "project_read":
				m.acceptanceProjects.CurrentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (bool, error) {
					return false, ErrUnavailable
				}
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "client_capability":
				m.clients = nil
			case "client_read":
				m.clients = &testClients{clientErr: ErrUnavailable}
			case "write":
				m.events = expiryPublisher{Publisher: m.events, publish: func(_ context.Context, tx *gorm.DB, _ events.Event) (guid.ID, error) {
					return 1, tx.Exec("INSERT INTO baseline_probe VALUES (123)").Error
				}}
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reminder_write_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AcceptanceReminder.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			default:
				target := m.names.AcceptanceReminder.String()
				if fault == "package_read" {
					target = m.names.DeliveryPackage.String()
				}
				if fault == "receipt_count" {
					target = m.names.PackageReceipt.String()
				}
				reads := 0
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reminder_source_failure", func(tx *gorm.DB) {
					if tx.Statement.Table != target {
						return
					}
					reads++
					if (fault == "reminder_read" && reads == 2) || (fault == "throttle_read" && reads == 3) || fault == "package_read" || fault == "receipt_count" {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err = m.advanceAcceptanceReminders(ctx, 1, row.DueAt)
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, db.Callback().Query().Remove("reminder_source_failure"))
			require.NoError(t, db.Callback().Update().Remove("reminder_write_failure"))
			stored, e := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, e)
			require.Nil(t, stored.EmittedAt)
			require.Nil(t, stored.CancelledAt)
			count, e := query.BindPackageReceipt(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, count)
		})
	}
}

func TestAcceptanceRemindersCatchUpOnceWithoutInventingReceipt(t *testing.T) {
	m, db, _, clients := harness(t)
	ctx := testContext(t, 2)
	var published Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, e := m.createPackageOn(ctx, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Actual notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
		if e != nil {
			return e
		}
		published, e = publishBuiltPackage(t, m, ctx, tx, 3, r.ID, r.Version)
		return e
	}))
	q := query.BindAcceptanceReminder(db, m.names)
	rows, err := q.WithContext(ctx).Order(q.Day).Find()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	for i, day := range []int{3, 7, 12, 14} {
		require.Equal(t, day, rows[i].Day)
		require.Equal(t, time.Duration(day)*24*time.Hour, rows[i].DueAt.Sub(rows[i].PublishedAt))
	}
	var emitted []events.Event
	m.events = expiryPublisher{Publisher: m.events, publish: func(_ context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		emitted = append(emitted, event)
		return 1, tx.Exec("INSERT INTO acceptance_probe VALUES (?)", event.Type).Error
	}}
	require.NoError(t, db.Exec("CREATE TABLE acceptance_probe(kind TEXT)").Error)
	require.NoError(t, m.advanceAcceptanceReminders(ctx, 1, rows[0].DueAt.Add(-time.Millisecond)))
	require.Empty(t, emitted)
	at := rows[3].DueAt.Add(time.Hour)
	for range 2 {
		require.NoError(t, m.advanceAcceptanceReminders(ctx, 1, at))
	}
	require.Len(t, emitted, 2)
	require.Equal(t, "clientwork.acceptance_overdue", emitted[1].Type)
	require.NoError(t, m.advanceAcceptanceReminders(ctx, 1, at.Add(24*time.Hour-time.Millisecond)))
	require.Len(t, emitted, 2)
	require.NoError(t, m.advanceAcceptanceReminders(ctx, 1, at.Add(24*time.Hour)))
	require.Len(t, emitted, 3)
	require.NoError(t, m.advanceAcceptanceReminders(ctx, 1, at.Add(48*time.Hour)))
	require.Len(t, emitted, 4)
	pkg, err := query.BindDeliveryPackage(db, m.names).WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, "PUBLISHED", pkg.State)
	require.Equal(t, published.Version, pkg.Version)
	count, err := query.BindPackageReceipt(db, m.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, found, e := m.acceptanceReminderFactsOn(ctx, tx, 1, rows[0].ID)
		require.True(t, found)
		require.EqualValues(t, 4, fact.RecipientID)
		return e
	}))
	clients.revoked = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, found, e := m.acceptanceReminderFactsOn(ctx, tx, 1, rows[0].ID)
		require.False(t, found)
		return e
	}))
}

func TestAcceptanceRemindersSuppressClosedPackagesAndRollBackFailures(t *testing.T) {
	for _, fault := range []string{"accepted", "archived", "changed", "audit", "outbox"} {
		t.Run(fault, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			var r Receipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				r, e = m.createPackageOn(ctx, tx, 3, PackageBody{Title: "Release", ReleaseNotes: "Actual notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}})
				if e != nil {
					return e
				}
				r, e = publishBuiltPackage(t, m, ctx, tx, 3, r.ID, r.Version)
				return e
			}))
			q := query.BindAcceptanceReminder(db, m.names)
			row, e := q.WithContext(ctx).Order(q.Day).First()
			require.NoError(t, e)
			p := query.BindDeliveryPackage(db, m.names)
			failure := errors.New("notice unavailable")
			switch fault {
			case "accepted":
				_, e = p.WithContext(ctx).Where(p.ID.Eq(r.ID)).Update(p.State, "ACCEPTED")
			case "archived":
				_, e = p.WithContext(ctx).Where(p.ID.Eq(r.ID)).Update(p.ArchivedAt, now())
			case "changed":
				_, e = p.WithContext(ctx).Where(p.ID.Eq(r.ID)).Update(p.ContentHash, "changed")
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.events = expiryPublisher{Publisher: m.events, publish: func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure }}
			}
			require.NoError(t, e)
			e = m.advanceAcceptanceReminders(ctx, 1, row.DueAt)
			stored, readErr := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
			require.NoError(t, readErr)
			require.Nil(t, stored.EmittedAt)
			if fault == "audit" || fault == "outbox" {
				require.ErrorIs(t, e, failure)
				require.Nil(t, stored.CancelledAt)
			} else {
				require.NoError(t, e)
				require.NotNil(t, stored.CancelledAt)
			}
			receipts, e := query.BindPackageReceipt(db, m.names).WithContext(ctx).Count()
			require.NoError(t, e)
			require.Zero(t, receipts)
		})
	}
}

func TestAcceptanceReminderFactRequiresEmittedCurrentUnansweredPackage(t *testing.T) {
	for _, fault := range []string{"missing", "pending", "canceled", "project_missing", "project_read", "inactive", "reminder_read", "package_read", "version", "receipt_read", "clients_missing"} {
		t.Run(fault, func(t *testing.T) {
			m, db, pkg := releaseNoteHarness(t)
			ctx := testContext(t, 2)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, pkg, 1); return e }))
			q := query.BindAcceptanceReminder(db, m.names)
			row, e := q.WithContext(ctx).Order(q.Day).First()
			require.NoError(t, e)
			require.NoError(t, db.Table(m.names.AcceptanceReminder.String()).Where("id=?", row.ID).Update("emitted_at", row.DueAt).Error)
			id := row.ID
			wantError := false
			switch fault {
			case "missing":
				id = 999
			case "pending":
				require.NoError(t, db.Table(m.names.AcceptanceReminder.String()).Where("id=?", row.ID).Update("emitted_at", nil).Error)
			case "canceled":
				require.NoError(t, db.Table(m.names.AcceptanceReminder.String()).Where("id=?", row.ID).Updates(map[string]any{"emitted_at": nil, "cancelled_at": row.DueAt}).Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
			case "project_missing":
				m.acceptanceProjects.CurrentOn = nil
				wantError = true
			case "project_read":
				m.acceptanceProjects.CurrentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (bool, error) {
					return false, ErrUnavailable
				}
				wantError = true
			case "inactive":
				m.acceptanceProjects.CurrentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) (bool, error) { return false, nil }
			case "version":
				require.NoError(t, db.Table(m.names.DeliveryPackage.String()).Where("id=?", pkg).Update("version", 99).Error)
			case "clients_missing":
				m.clients = nil
				wantError = true
			default:
				target := m.names.AcceptanceReminder.String()
				if fault == "package_read" {
					target = m.names.DeliveryPackage.String()
				}
				if fault == "receipt_read" {
					target = m.names.PackageReceipt.String()
				}
				wantError = true
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("reminder_fact_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				out, found, e := m.acceptanceReminderFactsOn(ctx, tx, 1, id)
				require.False(t, found)
				require.Zero(t, out.PackageID)
				return e
			})
			if wantError {
				require.ErrorIs(t, err, ErrUnavailable)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, db.Callback().Query().Remove("reminder_fact_failure"))
		})
	}
	m, db, _, _ := harness(t)
	_, found, e := m.acceptanceReminderFactsOn(t.Context(), nil, 1, 1)
	require.False(t, found)
	require.ErrorIs(t, e, ErrInvalid)
	_, found, e = m.acceptanceReminderFactsOn(t.Context(), db, 1, 1)
	require.False(t, found)
	require.ErrorIs(t, e, ErrInvalid)
}
