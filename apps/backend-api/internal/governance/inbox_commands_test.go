package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEscalationInboxAcknowledgementRequiresCurrentRevisionAndPreservesHold(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.recordEscalationOn(ctx, tx, escalationFixture())
		return err
	}))
	items, err := m.escalationInbox(ctx, InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "ESCALATION_ALERT", Limit: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	ack := func(revision string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			out, err := m.acknowledgeInboxOn(ctx, tx, 1, item.Kind, id, revision)
			if err == nil {
				require.Equal(t, id, out.ID)
			}
			return err
		})
	}
	require.ErrorIs(t, ack("stale"), ErrVersion)
	pub.Fail = true
	require.Error(t, ack(inboxRevision(item)))
	q := query.BindEscalationIncidents(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "OPEN", row.State)
	pub.Fail = false
	require.NoError(t, ack(inboxRevision(item)))
	require.NoError(t, ack(inboxRevision(item)))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	require.NoError(t, err)
	require.Equal(t, "ACKNOWLEDGED", row.State)
	items, err = m.escalationInbox(ctx, InboxRequest{TenantID: 1, PrincipalID: 2, Kind: item.Kind, Limit: 10})
	require.NoError(t, err)
	require.Empty(t, items)
	// Acknowledgement removes the notice while preserving the unresolved incident.
	require.NotNil(t, row.OpenKey)
}

func TestInboxAcknowledgementStorageFailureRollsBackReceiptAndEvent(t *testing.T) {
	for _, operation := range []string{"read", "create"} {
		t.Run(operation, func(t *testing.T) {
			m, db, ctx, _ := settingsHarness(t)
			item := inboxFixture(7, "CLARIFICATION_REPLY", "HIGH")
			require.NoError(t, m.inbox.Register(inboxFixtureSource(item.Kind, []InboxItem{item})))
			fault := errors.New("acknowledgement store unavailable")
			table := m.seed.names.InboxAcknowledgements.String()
			if operation == "read" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:inbox-ack", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			} else {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:inbox-ack", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.acknowledgeInboxOn(ctx, tx, 1, item.Kind, item.ID, inboxRevision(item))
				return err
			})
			if operation == "read" {
				require.NoError(t, db.Callback().Query().Remove("test:inbox-ack"))
			} else {
				require.NoError(t, db.Callback().Create().Remove("test:inbox-ack"))
			}
			require.ErrorIs(t, err, fault)
			require.Zero(t, seedCount(t, db, table))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
}

func TestInboxCurrentSourceCountDetailAcknowledgementReplayAndRollback(t *testing.T) {
	m, db, ctx, pub := settingsHarness(t)
	rows := []InboxItem{}
	for n := 1; n <= 105; n++ {
		item := inboxFixture(guid.ID(n), "CLARIFICATION_REPLY", "HIGH")
		item.SourceRevision = "1"
		rows = append(rows, item)
	}
	require.NoError(t, m.inbox.Register(inboxFixtureSource("CLARIFICATION_REPLY", rows)))
	req := InboxRequest{TenantID: 1, PrincipalID: 2, Limit: 50, Kind: "CLARIFICATION_REPLY"}
	counts, err := m.inboxCounts(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 105, counts.Total)
	require.Equal(t, 105, counts.ByKind["CLARIFICATION_REPLY"])
	first, err := m.inboxItem(ctx, 1, "CLARIFICATION_REPLY", 1, false)
	require.NoError(t, err)
	ack := func(revision string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.acknowledgeInboxOn(ctx, tx, 1, "CLARIFICATION_REPLY", 1, revision)
			return e
		})
	}
	require.ErrorIs(t, ack("stale"), ErrVersion)
	pub.Fail = true
	require.Error(t, ack(inboxRevision(first)))
	pub.Fail = false
	q := query.BindInboxAcknowledgements(db, m.seed.names)
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, ack(inboxRevision(first)))
	require.NoError(t, ack(inboxRevision(first)))
	n, err = q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = m.inboxItem(ctx, 1, "CLARIFICATION_REPLY", 1, false)
	require.ErrorIs(t, err, ErrNotFound)
	counts, err = m.inboxCounts(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 104, counts.Total)
	// A whole acknowledged first raw source page cannot hide later visible rows.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, item := range rows[:100] {
			if _, err := m.acknowledgeInboxOn(ctx, tx, 1, item.Kind, item.ID, inboxRevision(item)); err != nil {
				return err
			}
		}
		return nil
	}))
	page, err := m.readInbox(ctx, req, "", false)
	require.NoError(t, err)
	require.Len(t, page.Items, 5)
	require.False(t, page.HasMore)
	// New source revision is not acknowledged by a receipt for an older reply.
	rows[0].SourceRevision = "2"
	m.inbox.sources["CLARIFICATION_REPLY"] = inboxFixtureSource("CLARIFICATION_REPLY", rows)
	_, err = m.inboxItem(ctx, 1, "CLARIFICATION_REPLY", 1, false)
	require.NoError(t, err)
	// Current recipient visibility remains mandatory for replay and direct lookup.
	m.inbox.sources["CLARIFICATION_REPLY"] = inboxFixtureSource("CLARIFICATION_REPLY", nil)
	require.ErrorIs(t, ack(inboxRevision(first)), ErrNotFound)
	_, err = m.inboxItem(context.Background(), 1, "CLARIFICATION_REPLY", 1, false)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestInboxReadHasMoreMeansVisibleSourcesAndCountsUnavailableExplicitly(t *testing.T) {
	m, _, ctx, _ := settingsHarness(t)
	rows := []InboxItem{inboxFixture(1, "WORK_ITEM_REVIEW", "HIGH"), inboxFixture(2, "WORK_ITEM_REVIEW", "HIGH")}
	require.NoError(t, m.inbox.Register(inboxFixtureSource("WORK_ITEM_REVIEW", rows)))
	req := InboxRequest{TenantID: 1, PrincipalID: 2, Limit: 1}
	page, err := m.readInbox(ctx, req, "", false)
	require.NoError(t, err)
	require.True(t, page.HasMore)
	require.Len(t, page.Items, 1)
	next, err := m.readInbox(ctx, req, page.NextCursor, false)
	require.NoError(t, err)
	require.False(t, next.HasMore)
	require.Len(t, next.Items, 1)
	require.NotEqual(t, page.Items[0].ID, next.Items[0].ID)
	counts, err := m.inboxCounts(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 2, counts.Total)
	require.NotContains(t, counts.ByKind, "CLARIFICATION_REPLY")
	require.Len(t, counts.Capabilities, 26)
	_, err = m.inboxItem(ctx, 1, "WORK_ITEM_REVIEW", 999, false)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestInboxAcknowledgementCurrentIdentityAndIdentifierFailuresWriteNothing(t *testing.T) {
	for _, scenario := range []string{"lock", "human", "inactive", "unavailable", "identifier", "zero_identifier", "foreign", "nontransaction"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := settingsHarness(t)
			item := inboxFixture(7, "CLARIFICATION_REPLY", "HIGH")
			require.NoError(t, m.inbox.Register(inboxFixtureSource(item.Kind, []InboxItem{item})))
			fault := errors.New("acknowledgement identity source failed")
			want := fault
			switch scenario {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "inactive":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = ErrForbidden
			case "unavailable":
				m.principals.Lock = nil
				want = ErrUnavailable
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "foreign":
				ctx = t.Context()
				want = ErrForbidden
			case "nontransaction":
				want = ErrInvalidArgument
			}
			call := func(tx *gorm.DB) error {
				_, err := m.acknowledgeInboxOn(ctx, tx, 1, item.Kind, item.ID, inboxRevision(item))
				return err
			}
			var err error
			if scenario == "nontransaction" {
				err = call(db)
			} else {
				err = db.Transaction(call)
			}
			require.ErrorIs(t, err, want)
			require.Zero(t, seedCount(t, db, m.seed.names.InboxAcknowledgements.String()))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
}
func TestEscalationAcknowledgementCurrentSourcesRejectWithoutChangingIncident(t *testing.T) {
	for _, scenario := range []string{"read", "project", "recipient", "recipient_error", "absent", "authority"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _ := settingsHarness(t)
			var id guid.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				id, err = m.recordEscalationOn(ctx, tx, escalationFixture())
				return err
			}))
			fault := errors.New("escalation acknowledgement source failed")
			want := fault
			switch scenario {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:incident-ack", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.EscalationIncidents.String() {
						tx.AddError(fault)
					}
				}))
			case "project", "recipient", "recipient_error", "authority":
				m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					if scenario == "authority" || scenario == "project" && r.Permission == "project_read" || scenario == "recipient_error" && r.Permission != "policy_read" && r.Permission != "project_read" {
						return authz.Explanation{}, fault
					}
					return authz.Explanation{Allowed: scenario != "recipient" || r.Permission == "policy_read" || r.Permission == "project_read"}, nil
				}
				if scenario == "recipient" {
					want = ErrNotFound
				}
			case "absent":
				id++
				want = ErrNotFound
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.acknowledgeInboxOn(ctx, tx, 1, "ESCALATION_ALERT", id, "unused")
				return err
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("test:incident-ack"))
			q := query.BindEscalationIncidents(db, m.seed.names)
			row, err := q.WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, "OPEN", row.State)
			require.Equal(t, 1, row.Version)
		})
	}
}
