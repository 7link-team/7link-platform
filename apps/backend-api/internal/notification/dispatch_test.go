package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLegacyDispatchWithoutReceiptRechecksCurrentProposal(t *testing.T) {
	f := newFixture(t)
	active := true
	f.m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
		state := "PROPOSED"
		if !active {
			state = "APPLIED"
		}
		return clientwork.CollaborationNotificationFact{ProjectID: 81, State: state, RequiresHuman: true, ExpiresAt: f.m.now().Add(time.Hour)}, true, nil
	}
	row := &model.Notification{ID: 1001, TenantID: 7, ProjectID: 81, Type: "dispatch.confirmation_required", Params: `{"subject_id":"91"}`}
	yes, err := f.m.dispatchGroupAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.True(t, yes)
	active = false
	yes, err = f.m.dispatchGroupAudienceOn(t.Context(), f.db, row, 11)
	require.NoError(t, err)
	require.False(t, yes)
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("dispatch_read", func(tx *gorm.DB) {
		if tx.Statement.Table == f.m.names.DispatchReceipts.String() {
			tx.AddError(ErrUnavailable)
		}
	}))
	yes, err = f.m.dispatchGroupAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
	require.NoError(t, f.db.Callback().Query().Remove("dispatch_read"))
	f.m.collaborationFacts = nil
	yes, err = f.m.dispatchGroupAudienceOn(t.Context(), f.db, row, 11)
	require.False(t, yes)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestChannelRequestInitiatorVisibilityAndGroupedRevocation(t *testing.T) {
	f := newFixture(t)
	f.m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, nil
	}
	f.m.dispatchInitiators = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (clientwork.DispatchRequestInitiatorFact, bool, error) {
		return clientwork.DispatchRequestInitiatorFact{ProjectID: 9, WorkItemID: 20, RequesterID: 11}, tenant == 7 && id == 91, nil
	}
	state := "PROPOSED"
	f.m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
		return clientwork.CollaborationNotificationFact{ProjectID: 9, WorkItemID: 20, State: state, RequiresHuman: true, ExpiresAt: f.m.now().Add(time.Hour)}, true, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.dispatch_proposed", Data: json.RawMessage(`{}`)}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("dispatch.confirmation_required")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, guid.ID(11), rows[0].RecipientPrincipalID)
	check := func(want bool) {
		t.Helper()
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
			yes, err := f.m.visibleOn(t.Context(), tx, rows[0], 11)
			require.NoError(t, err)
			require.Equal(t, want, yes)
			return nil
		}))
	}
	check(true)
	f.allow = false
	check(false)
	f.allow = true
	f.members.active = false
	check(false)
	f.members.active = true
	state = "APPLIED"
	check(false)
	state = "PROPOSED"
	check(true)
	f.m.dispatchInitiators = nil
	check(false)
	legacy := &model.Notification{TenantID: 7, ProjectID: 9, Type: "dispatch.confirmation_required", RecipientPrincipalID: 12, Permission: "work_item_assign", Params: `{"subject_id":"91"}`}
	yes, err := f.m.visibleOn(t.Context(), f.db, legacy, 12)
	require.NoError(t, err)
	require.False(t, yes, "unrelated Humans cannot see a request without assignment authority")
}

func TestDispatchGroupsReplayCurrentSourcesAndAudience(t *testing.T) {
	f := newFixture(t)
	start := f.m.now()
	clock := start
	f.m.now = func() time.Time { return clock }
	facts := map[guid.ID]clientwork.CollaborationNotificationFact{}
	for _, id := range []guid.ID{91, 92, 93, 94} {
		facts[id] = clientwork.CollaborationNotificationFact{ProjectID: 9, State: "PROPOSED", RequiresHuman: true, ExpiresAt: start.Add(time.Hour)}
	}
	f.m.collaborationFacts = func(_ context.Context, _ *gorm.DB, _, id guid.ID, _ string) (clientwork.CollaborationNotificationFact, bool, error) {
		v, ok := facts[id]
		return v, ok, nil
	}
	allowed := map[guid.ID]bool{11: true}
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: allowed[r.PrincipalID]}, nil
	}
	project := func(id guid.ID) {
		t.Helper()
		e := events.RecordedEvent{ID: id + 1000, TenantID: 7, ProjectID: 9, SubjectID: id, SubjectKind: "clientwork", Type: "clientwork.dispatch_proposed", Data: json.RawMessage(`{}`)}
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	rows := func() []*NotificationView {
		t.Helper()
		current, err := f.m.inboxRows(t.Context(), f.db, 11, 7, 0, "", 100)
		require.NoError(t, err)
		out := []*NotificationView{}
		for _, r := range current {
			v, err := view(t.Context(), r)
			require.NoError(t, err)
			out = append(out, &v)
		}
		return out
	}
	project(91)
	clock = start.Add(9 * time.Minute)
	project(92)
	project(92)
	current := rows()
	require.Len(t, current, 1)
	require.Equal(t, 2, current[0].MergeCount)
	require.Contains(t, current[0].Body, "2")
	first := current[0].ID
	// Resolving the original subject leaves its actionable sibling visible.
	v := facts[91]
	v.State = "APPLIED"
	facts[91] = v
	current = rows()
	require.Len(t, current, 1)
	require.Equal(t, 1, current[0].MergeCount)
	allowed[11] = false
	require.Empty(t, rows())
	allowed[11] = true
	clock = start.Add(10 * time.Minute)
	project(93)
	current = rows()
	require.Len(t, current, 2, "exact ten-minute boundary opens a new group")
	require.NotEqual(t, first, current[0].ID)
	project(92)
	require.Len(t, rows(), 2, "late replay cannot open another group")
	// Replays may reach a newly eligible recipient once, without inflating old groups.
	allowed[12] = true
	project(92)
	q := query.BindDispatchReceipts(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Where(q.ProposalID.Eq(guid.ID(92))).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	// Read rollover still preserves the permanent source receipt.
	n := query.BindNotifications(f.db, f.m.names)
	_, err = n.WithContext(t.Context()).Where(n.ID.Eq(current[0].ID)).Update(n.ReadAt, clock)
	require.NoError(t, err)
	project(94)
	require.Len(t, rows(), 3)
	project(93)
	require.Len(t, rows(), 3)
	// Current source project/expiry/missing facts close only the corresponding group.
	v = facts[92]
	v.ProjectID = 8
	facts[92] = v
	v = facts[93]
	v.ExpiresAt = clock
	facts[93] = v
	delete(facts, 94)
	require.Empty(t, rows())
}

func TestDispatchReceiptsRollbackWithWholeAudience(t *testing.T) {
	f := newFixture(t)
	f.m.collaborationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (clientwork.CollaborationNotificationFact, bool, error) {
		return clientwork.CollaborationNotificationFact{ProjectID: 9, State: "PROPOSED", RequiresHuman: true, ExpiresAt: f.m.now().Add(time.Hour)}, true, nil
	}
	failure := errors.New("current audience unavailable")
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		if r.PrincipalID == 12 {
			return authz.Explanation{}, failure
		}
		return authz.Explanation{Allowed: true}, nil
	}
	e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91, SubjectKind: "clientwork", Type: "clientwork.dispatch_proposed", Data: json.RawMessage(`{}`)}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), failure)
	for _, table := range []string{f.m.names.Notifications.String(), f.m.names.DispatchReceipts.String()} {
		var count int64
		require.NoError(t, f.db.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestDispatchOccurrenceReceiptFailureRollsBackInbox(t *testing.T) {
	for _, mode := range []string{"lock", "guard", "read", "receipt", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			switch mode {
			case "lock":
				f.m.principal.LockActive = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "guard":
				f.m.guard = func(context.Context, *gorm.DB, string) error { return ErrUnavailable }
			case "read":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("occurrence_read", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.DispatchReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "receipt":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("occurrence_write", func(tx *gorm.DB) {
					if tx.Statement.Table == f.m.names.DispatchReceipts.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "notify":
				f.ids.err = ErrUnavailable
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.notifyDispatchOn(t.Context(), tx, events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 9, SubjectID: 91}, 11)
			})
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, f.db.Callback().Query().Remove("occurrence_read"))
			require.NoError(t, f.db.Callback().Create().Remove("occurrence_write"))
			for _, table := range []string{f.m.names.Notifications.String(), f.m.names.DispatchReceipts.String()} {
				var n int64
				require.NoError(t, f.db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}
