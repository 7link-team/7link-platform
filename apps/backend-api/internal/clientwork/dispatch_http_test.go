package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDispatchHistoryFiltersAndCursorsPreserveRecordedDecisions(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "history-create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, e)
	q := query.BindDispatchProposal(db, m.names)
	original, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, e)
	for i := 1; i <= 4; i++ {
		row := *original
		row.ID += guid.ID(i)
		row.ActiveSlot = nil
		row.State = "APPLIED"
		recommended, applied := guid.ID(21), guid.ID(22)
		row.RecommendedID, row.AppliedID = &recommended, &applied
		if i == 3 {
			row.State = "DISMISSED"
		}
		if i == 4 {
			row.WorkItemID = 12
		}
		require.NoError(t, q.WithContext(ctx).Create(&row))
	}
	input := &DispatchListInput{ListInput: ListInput{EntityID: "3", Limit: 1}, WorkItemID: "11", PrincipalID: "22", State: "APPLIED"}
	first, e := m.listDispatchFilteredHTTP(ctx, input)
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Equal(t, original.ID+1, first.Body.Data.Items[0].ID)
	input.Cursor = first.Body.Data.NextCursor
	second, e := m.listDispatchFilteredHTTP(ctx, input)
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Equal(t, original.ID+2, second.Body.Data.Items[0].ID)
	for _, altered := range []DispatchListInput{
		{ListInput: input.ListInput, WorkItemID: "12", PrincipalID: "22", State: "APPLIED"},
		{ListInput: input.ListInput, WorkItemID: "11", PrincipalID: "21", State: "APPLIED"},
		{ListInput: input.ListInput, WorkItemID: "11", PrincipalID: "22", State: "DISMISSED"},
		{ListInput: ListInput{EntityID: "3", Limit: 1}, WorkItemID: "bad"},
		{ListInput: ListInput{EntityID: "3", Limit: 1}, PrincipalID: "0"},
		{ListInput: ListInput{EntityID: "3", Limit: 1}, State: "invented"},
	} {
		_, e := m.listDispatchFilteredHTTP(ctx, &altered)
		require.Error(t, e, "filters must bind the cursor or be rejected")
	}
	input.Cursor = ""
	input.PrincipalID = "21"
	page, e := m.listDispatchFilteredHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, original.ID+1, page.Body.Data.Items[0].ID, "recommended history is included separately from actual assignee")
	_, e = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: original.ID.String(), IfMatch: `"1"`, Key: "history-dismiss", Body: DispatchDecision{Decision: "DISMISS", Reason: "Human declines this recommendation"}})
	require.NoError(t, e)
	history, e := m.listDispatchFilteredHTTP(ctx, &DispatchListInput{ListInput: ListInput{EntityID: "3", Limit: 20}, State: "DISMISSED"})
	require.NoError(t, e)
	require.NotNil(t, history.Body.Data.Items[0].Decision)
	require.Equal(t, "DISMISS", history.Body.Data.Items[0].Decision.Action)
	require.Equal(t, "Human declines this recommendation", history.Body.Data.Items[0].Decision.Reason)
}

func TestDispatchStopCommandPersistsIntentWithoutApplyingStaleAssignment(t *testing.T) {
	for _, scenario := range []string{"pending", "stopped", "stop_failure", "audit_failure", "lock_failure", "stale_proposal", "wrong_trigger"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, changed := dispatchFixture(t)
			ctx := testContext(t, 2)
			trigger := "REDISPATCH"
			if scenario == "wrong_trigger" {
				trigger = "MANUAL_REQUEST"
			}
			created, err := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "stop-create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: trigger}})
			require.NoError(t, err)
			locks, stops := 0, 0
			m.dispatch.LockStopOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, execution guid.ID) error {
				locks++
				require.EqualValues(t, 1, tenant)
				require.EqualValues(t, 3, project)
				require.EqualValues(t, 11, work)
				require.EqualValues(t, 77, execution)
				if scenario == "lock_failure" {
					return ErrForbidden
				}
				return nil
			}
			m.dispatch.StopOn = func(_ context.Context, tx *gorm.DB, _, _, _, execution guid.ID, version int) (delivery.DispatchExecutionState, error) {
				stops++
				require.Positive(t, locks)
				require.Equal(t, 4, version)
				if err := tx.Exec("INSERT INTO baseline_probe VALUES (?)", execution).Error; err != nil {
					return delivery.DispatchExecutionState{}, err
				}
				if scenario == "stop_failure" {
					return delivery.DispatchExecutionState{}, ErrUnavailable
				}
				state := "RUNNING"
				if scenario == "stopped" {
					state = "CANCELLED" //nolint:misspell // Frozen protocol state.
				}
				at := now()
				return delivery.DispatchExecutionState{ExecutionID: execution, State: state, Version: version + 1, CancelRequestedAt: &at}, nil
			}
			if scenario == "audit_failure" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			}
			if scenario == "stale_proposal" {
				*changed = true
			}
			input := &DispatchStopInput{EntityID: "3", ID: created.Body.Data.ID.String(), Key: "stop", IfMatch: `"1"`, Body: DispatchStopBody{ExecutionID: 77, ExpectedExecutionVersion: 4, Reason: "Stop before reassignment"}}
			out, err := m.stopDispatchExecutionHTTP(ctx, input)
			var writes int64
			require.NoError(t, db.Table("baseline_probe").Count(&writes).Error)
			if scenario != "pending" && scenario != "stopped" {
				require.Error(t, err)
				require.Zero(t, writes)
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, writes)
			want := "EXECUTION_STOP_PENDING"
			if scenario == "stopped" {
				want = "EXECUTION_STOPPED"
			}
			require.Equal(t, want, out.Body.Data.State)
			replayed, err := m.stopDispatchExecutionHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "true", replayed.Replayed)
			require.Equal(t, 1, stops)
			view, err := m.getDispatchHTTP(ctx, &ObjectInput{EntityID: "3", ID: input.ID})
			require.NoError(t, err)
			require.Equal(t, "PROPOSED", view.Body.Data.State)
			require.Nil(t, view.Body.Data.AssignmentID)
			require.Nil(t, view.Body.Data.Decision)
		})
	}
}

func TestDispatchReadRechecksWorkScopeAndExpiry(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "read-scope", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, e)
	input := &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()}
	q := query.BindDispatchProposal(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).Update(q.ExpiresAt, now().Add(-time.Second))
	require.NoError(t, e)
	view, e := m.getDispatchHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, "EXPIRED", view.Body.Data.State)
	require.True(t, view.Body.Data.CanExpire)
	_, e = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: input.ID, Key: "expire-view", IfMatch: `"1"`, Body: DispatchDecision{Decision: "EXPIRE", Reason: "TTL reached"}})
	require.NoError(t, e)
	view, e = m.getDispatchHTTP(ctx, input)
	require.NoError(t, e)
	require.False(t, view.Body.Data.CanExpire)
	m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
		return delivery.DispatchWorkFacts{}, ErrForbidden
	}
	view, e = m.getDispatchHTTP(ctx, input)
	require.Error(t, e)
	require.Nil(t, view)
	list, e := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3"})
	require.Error(t, e)
	require.Nil(t, list)
}

func TestDispatchListFiltersWorkScopeBeforePagination(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	created, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "mixed-list", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.NoError(t, e)
	q := query.BindDispatchProposal(db, m.names)
	source, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, e)
	// Inaccessible rows span several database batches; visible rows are
	// interleaved and followed by inaccessible rows on the final page.
	for i := 1; i <= 14; i++ {
		row := *source
		row.ID += guid.ID(i)
		row.WorkItemID = guid.ID(100 + i)
		row.ActiveSlot = &row.WorkItemID
		require.NoError(t, q.WithContext(ctx).Create(&row))
	}
	visible := map[guid.ID]bool{105: true, 107: true, 109: true, 112: true}
	m.dispatch.WorkOn = func(_ context.Context, _ *gorm.DB, _, _, id guid.ID) (delivery.DispatchWorkFacts, error) {
		if visible[id] {
			return delivery.DispatchWorkFacts{}, nil
		}
		if id%2 == 0 {
			return delivery.DispatchWorkFacts{}, delivery.ErrProjectNotFound
		}
		return delivery.DispatchWorkFacts{}, ErrForbidden
	}
	first, e := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 2})
	require.NoError(t, e)
	require.True(t, first.Body.Data.HasMore)
	require.Len(t, first.Body.Data.Items, 2)
	require.Equal(t, guid.ID(105), first.Body.Data.Items[0].WorkItemID)
	require.Equal(t, guid.ID(107), first.Body.Data.Items[1].WorkItemID)
	second, e := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 2, Cursor: first.Body.Data.NextCursor})
	require.NoError(t, e)
	require.False(t, second.Body.Data.HasMore)
	require.Empty(t, second.Body.Data.NextCursor)
	require.Len(t, second.Body.Data.Items, 2)
	require.Equal(t, guid.ID(109), second.Body.Data.Items[0].WorkItemID)
	require.Equal(t, guid.ID(112), second.Body.Data.Items[1].WorkItemID)
	for _, cause := range []error{ErrUnavailable, errors.New("provider failed")} {
		m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
			return delivery.DispatchWorkFacts{}, cause
		}
		out, e := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 2})
		require.Error(t, e)
		require.Nil(t, out, "operational failures must not become an empty page")
	}
	m.dispatch.WorkOn = nil
	out, e := m.listDispatchHTTP(ctx, &ListInput{EntityID: "3", Limit: 2})
	require.Error(t, e)
	require.Nil(t, out)
}

func TestDispatchCandidatesAreCurrentDeterministicAndNeverPersist(t *testing.T) {
	m, db, changed := dispatchFixture(t)
	ctx := testContext(t, 2)
	input := &DispatchCandidatesInput{EntityID: "3", WorkItemID: "11"}
	first, e := m.dispatchCandidatesHTTP(ctx, input)
	require.NoError(t, e)
	second, e := m.dispatchCandidatesHTTP(ctx, input)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, second.Body.Data)
	require.Len(t, first.Body.Data.InputsDigest, 64)
	require.Len(t, first.Body.Data.Snapshot.Candidates, 1)
	require.Empty(t, first.Body.Data.Snapshot.Candidates[0].IneligibleCodes)
	q := query.BindDispatchProposal(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	*changed = true
	latest, e := m.dispatchCandidatesHTTP(ctx, input)
	require.NoError(t, e)
	require.NotEqual(t, first.Body.Data.InputsDigest, latest.Body.Data.InputsDigest)
	require.Equal(t, 2, latest.Body.Data.Snapshot.Work.WorkVersion)
	for _, in := range []*DispatchCandidatesInput{{EntityID: "bad", WorkItemID: "11"}, {EntityID: "3", WorkItemID: "bad"}} {
		_, e = m.dispatchCandidatesHTTP(ctx, in)
		require.Error(t, e)
	}
	m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
		return delivery.DispatchWorkFacts{}, ErrForbidden
	}
	denied, e := m.dispatchCandidatesHTTP(ctx, input)
	require.Error(t, e)
	require.Nil(t, denied)
	m.dispatch.WorkOn = nil
	_, e = m.dispatchCandidatesHTTP(ctx, input)
	require.Error(t, e)
	_, e = (&Module{}).dispatchCandidatesHTTP(ctx, input)
	require.Error(t, e)
}

func TestDispatchHTTPInvalidPathsAndUnavailableProvider(t *testing.T) {
	m, _, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}, {EntityID: "4", ID: "1"}} {
		out, e := m.getDispatchHTTP(ctx, in)
		require.Error(t, e)
		require.Nil(t, out)
	}
	_, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "bad"})
	require.Error(t, e)
	_, e = m.decideDispatchHTTP(ctx, &EditInput[DispatchDecision]{EntityID: "3", ID: "1", IfMatch: "bad"})
	require.Error(t, e)
	m.dispatch.WorkOn = nil
	_, e = m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "unavailable", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}})
	require.Error(t, e)
}

func TestDispatchReplacementExpiresOldProposalWithinSameTransaction(t *testing.T) {
	m, db, _ := dispatchFixture(t)
	ctx := testContext(t, 2)
	in := DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}
	first, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "first-slot", Body: in})
	require.NoError(t, e)
	_, e = m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "duplicate-slot", Body: in})
	require.Error(t, e)
	q := query.BindDispatchProposal(db, m.names)
	_, e = q.WithContext(ctx).Where(q.ID.Eq(first.Body.Data.ID)).Update(q.ExpiresAt, now().Add(-time.Second))
	require.NoError(t, e)
	second, e := m.createDispatchHTTP(ctx, &CreateInput[DispatchBody]{EntityID: "3", Key: "replacement-slot", Body: in})
	require.NoError(t, e)
	require.NotEqual(t, first.Body.Data.ID, second.Body.Data.ID)
	old, e := q.WithContext(ctx).Where(q.ID.Eq(first.Body.Data.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "EXPIRED", old.State)
	require.Nil(t, old.ActiveSlot)
	require.Equal(t, 2, old.Version)
	active, e := q.WithContext(ctx).Where(q.ActiveSlot.Eq(guid.ID(11))).Find()
	require.NoError(t, e)
	require.Len(t, active, 1)
	require.Equal(t, second.Body.Data.ID, active[0].ID)
}

func TestDispatchCurrentWorkScopeFencesEveryDecisionAndReplay(t *testing.T) {
	for _, decision := range []string{"CONFIRM", "OVERRIDE", "DISMISS", "EXPIRE"} {
		t.Run(decision, func(t *testing.T) {
			m, db, _ := dispatchFixture(t)
			ctx := testContext(t, 2)
			create := &CreateInput[DispatchBody]{EntityID: "3", Key: "scope-create", Body: DispatchBody{WorkItemID: 11, ExpectedWorkVersion: 1, Trigger: "MANUAL_REQUEST"}}
			created, e := m.createDispatchHTTP(ctx, create)
			require.NoError(t, e)
			q := query.BindDispatchProposal(db, m.names)
			if decision == "EXPIRE" {
				_, e = q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).Update(q.ExpiresAt, now().Add(-time.Second))
				require.NoError(t, e)
			}
			target := guid.ID(6)
			input := &EditInput[DispatchDecision]{EntityID: "3", ID: created.Body.Data.ID.String(), Key: "scope-decide", IfMatch: `"1"`, Body: DispatchDecision{Decision: decision, Reason: "Reviewed", AssigneePrincipalID: &target}}
			original := m.dispatch.WorkOn
			m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
				return delivery.DispatchWorkFacts{}, ErrForbidden
			}
			_, e = m.decideDispatchHTTP(ctx, input)
			require.Error(t, e)
			_, e = m.createDispatchHTTP(ctx, create)
			require.Error(t, e, "existing create receipt cannot bypass current scope")
			row, e := q.WithContext(ctx).Where(q.ID.Eq(created.Body.Data.ID)).First()
			require.NoError(t, e)
			require.Equal(t, "PROPOSED", row.State)
			require.Equal(t, 1, row.Version)
			m.dispatch.WorkOn = original
			_, e = m.decideDispatchHTTP(ctx, input)
			require.NoError(t, e)
			replay, e := m.decideDispatchHTTP(ctx, input)
			require.NoError(t, e)
			require.Equal(t, "true", replay.Replayed)
			m.dispatch.WorkOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.DispatchWorkFacts, error) {
				return delivery.DispatchWorkFacts{}, ErrForbidden
			}
			replay, e = m.decideDispatchHTTP(ctx, input)
			require.Error(t, e, "committed decision receipt must recheck work scope")
			require.Nil(t, replay)
		})
	}
}
