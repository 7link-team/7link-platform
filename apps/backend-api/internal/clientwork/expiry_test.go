package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExpiryPersistsDueRowsExactlyOnceWithSystemEvents(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	_, share := previewShareFixture(t, m, db)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Reviewed"})
		return e
	}))
	proposal, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "expiring", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, e)
	at := now().Add(2 * time.Hour)
	q := query.BindPreviewShare(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(share.ID)).Update(q.ExpiresAt, at)
	require.NoError(t, e)
	var observed []events.Event
	original := m.events.Publish
	m.events = expiryPublisher{Publisher: m.events, publish: func(ctx context.Context, tx *gorm.DB, event events.Event) (guid.ID, error) {
		observed = append(observed, event)
		return original(ctx, tx, event)
	}}
	require.NoError(t, m.expireDue(ctx, 2, at))
	require.Empty(t, observed, "another tenant must not mutate these rows")
	require.NoError(t, m.expireDue(ctx, 1, at))
	require.Len(t, observed, 2)
	for _, event := range observed {
		require.Equal(t, "SYSTEM", event.Actor.Kind)
		require.Zero(t, event.Actor.ID)
		require.Equal(t, "system:ttl_expiry", event.Actor.AuthoritySource)
	}
	require.NoError(t, m.expireDue(ctx, 1, at.Add(time.Minute)))
	require.Len(t, observed, 2)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "EXPIRED", row.State)
	require.Equal(t, 3, row.Version)
	require.Nil(t, row.ActiveSlot)
	d := query.BindDispatchProposal(db, m.names)
	dispatch, e := d.WithContext(ctx).Where(d.ID.Eq(proposal.Body.Data.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "EXPIRED", dispatch.State)
	require.Equal(t, 2, dispatch.Version)
	require.Nil(t, dispatch.ActiveSlot)
}

func TestExpiryRollbackAndSchedulerContract(t *testing.T) {
	for _, boundary := range []string{"audit", "outbox"} {
		t.Run(boundary, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "expiry-rollback", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
			require.NoError(t, e)
			failure := errors.New("write boundary unavailable")
			if boundary == "audit" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			} else {
				m.events = expiryPublisher{Publisher: m.events, publish: func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure }}
			}
			require.ErrorIs(t, m.expireDue(ctx, 1, now().Add(time.Hour)), failure)
			q := query.BindDispatchProposal(db, m.names)
			row, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "PROPOSED", row.State)
			require.Equal(t, 1, row.Version)
			require.NotNil(t, row.ActiveSlot)
		})
	}
	m, _, _ := dispatchFixture(t)
	require.ErrorIs(t, m.expireDue(t.Context(), 0, now()), ErrInvalid)
	require.ErrorIs(t, (&Module{}).expireDue(t.Context(), 1, now()), ErrUnavailable)
	handlers := m.JobHandlers()
	require.Len(t, handlers, 8)
	require.Error(t, handlers[0].Run(t.Context(), jobs.Job{}))
	require.NoError(t, handlers[0].Run(t.Context(), jobs.Job{TenantID: 1}))
	schedules := m.JobSchedules()
	require.Len(t, schedules, 4)
	require.Equal(t, handlers[0].Kind, schedules[0].Kind)
	require.Equal(t, jobs.ScopePerTenant, schedules[0].TenantScope)
}

type expiryPublisher struct {
	events.Publisher
	publish func(context.Context, *gorm.DB, events.Event) (guid.ID, error)
}

func (p expiryPublisher) Publish(ctx context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	return p.publish(ctx, tx, e)
}

func TestShareExpiryRollbackLeavesFutureAndPendingUntouched(t *testing.T) {
	for _, state := range []string{"PENDING_APPROVAL", "FUTURE", "AUDIT_FAILURE", "OUTBOX_FAILURE"} {
		t.Run(state, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			_, share := previewShareFixture(t, m, db)
			if state != "PENDING_APPROVAL" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Ready"})
					return e
				}))
			}
			q := query.BindPreviewShare(db, m.names)
			at := now().Add(25 * time.Hour)
			if state == "FUTURE" {
				at = now()
			}
			original := m.events
			if state == "AUDIT_FAILURE" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit failed") }
			}
			if state == "OUTBOX_FAILURE" {
				m.events = failPublisher{}
			}
			e := m.expireDue(ctx, 1, at)
			if state == "AUDIT_FAILURE" || state == "OUTBOX_FAILURE" {
				require.Error(t, e)
			} else {
				require.NoError(t, e)
			}
			row, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
			require.NoError(t, e)
			require.NotNil(t, row.ActiveSlot)
			if state == "PENDING_APPROVAL" {
				require.Equal(t, "PENDING_APPROVAL", row.State)
				require.Equal(t, 1, row.Version)
			} else {
				require.Equal(t, "ACTIVE", row.State)
				require.Equal(t, 2, row.Version)
			}
			if state == "OUTBOX_FAILURE" {
				m.events = original
				require.NoError(t, m.expireDue(ctx, 1, at))
				row, e = q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
				require.NoError(t, e)
				require.Equal(t, "EXPIRED", row.State)
				require.Nil(t, row.ActiveSlot)
			}
		})
	}
}

func TestExpiryDatabaseFailuresPreserveRetryableRows(t *testing.T) {
	for _, family := range []string{"dispatch", "share"} {
		for _, operation := range []string{"query", "update"} {
			t.Run(family+"/"+operation, func(t *testing.T) {
				m, db, _ := dispatchFixture(t)
				ctx := testContext(t, 2)
				_, share := previewShareFixture(t, m, db)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.decideShareOn(testContext(t, 6), tx, 3, share.ID, 1, PreviewShareDecision{Decision: "APPROVE", Reason: "Reviewed"})
					return e
				}))
				created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "expiry-database", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
				require.NoError(t, e)
				table := m.names.DispatchProposal.String()
				if family == "share" {
					table = m.names.PreviewShare.String()
				}
				failure := errors.New("database temporarily unavailable")
				fail := func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						_ = tx.AddError(failure)
					}
				}
				if operation == "query" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("clientwork.expiry_fault", fail))
				} else {
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("clientwork.expiry_fault", fail))
				}
				require.ErrorIs(t, m.expireDue(ctx, 1, now().Add(25*time.Hour)), failure)
				if operation == "query" {
					require.NoError(t, db.Callback().Query().Remove("clientwork.expiry_fault"))
				} else {
					require.NoError(t, db.Callback().Update().Remove("clientwork.expiry_fault"))
				}
				if family == "share" {
					q := query.BindPreviewShare(db, m.names)
					row, e := q.WithContext(ctx).Where(q.ID.Eq(share.ID)).First()
					require.NoError(t, e)
					require.Equal(t, "ACTIVE", row.State)
					require.NotNil(t, row.ActiveSlot)
				} else {
					q := query.BindDispatchProposal(db, m.names)
					row, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
					require.NoError(t, e)
					require.Equal(t, "PROPOSED", row.State)
					require.NotNil(t, row.ActiveSlot)
				}
				require.NoError(t, m.expireDue(ctx, 1, now().Add(25*time.Hour)))
			})
		}
	}
}
