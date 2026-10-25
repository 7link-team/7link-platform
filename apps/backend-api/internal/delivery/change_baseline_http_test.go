package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWorkBaselineCreatesImmutableExplicitSelectionAndNoop(t *testing.T) {
	m, db, ctx, id, ids := staleBaselineFixture(t)
	q := query.BindWorkContracts(db, m.service.names)
	old, err := q.WithContext(ctx).Where(q.WorkItemID.Eq(id)).First()
	require.NoError(t, err)
	var oldSnapshot workContractSnapshot
	require.NoError(t, json.Unmarshal([]byte(old.Snapshot), &oldSnapshot))
	in := &workBaselineInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: id.String()}, IfMatch: fmtETag(2), IdempotencyKey: "rebaseline"}, Body: workBaselineBody{RequirementVersionNo: 2, CriterionIDs: ids}}
	result, err := m.workBaselineHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, 3, result.Body.Data.Version)
	replayed, err := m.workBaselineHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	contracts, err := q.WithContext(ctx).Where(q.WorkItemID.Eq(id)).Order(q.VersionNo).Find()
	require.NoError(t, err)
	require.Len(t, contracts, 2)
	require.Equal(t, old.Snapshot, contracts[0].Snapshot)
	var snapshot workContractSnapshot
	require.NoError(t, json.Unmarshal([]byte(contracts[1].Snapshot), &snapshot))
	require.Equal(t, ids, snapshot.RequirementCriterionIDs)
	require.Equal(t, oldSnapshot.Criteria, snapshot.Criteria)
	require.Equal(t, 2, snapshot.BaselineVersionNo)
	w := query.BindWorkItems(db, m.service.names)
	work, err := w.WithContext(ctx).Where(w.ID.Eq(id)).First()
	require.NoError(t, err)
	require.False(t, work.BaselineStale)
	require.Nil(t, work.BaselineStaleSince)
	in.IfMatch, in.IdempotencyKey = fmtETag(work.Version), ""
	noop, err := m.workBaselineHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, result.Body.Data, noop.Body.Data)
	count, err := q.WithContext(ctx).Where(q.WorkItemID.Eq(id)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	transitions := query.BindWorkTransitions(db, m.service.names)
	transition, err := transitions.WithContext(ctx).Where(transitions.WorkItemID.Eq(id), transitions.ReasonCode.Eq("WORK_BASELINE_CONFIRMED")).First()
	require.NoError(t, err)
	require.Contains(t, *transition.SourceRef, "work_item:"+id.String()+":")
}

func TestWorkBaselineHTTPRejectsInvalidPathUnavailableRuntimeAndMissingWork(t *testing.T) {
	m, _, ctx, id, ids := staleBaselineFixture(t)
	in := &workBaselineInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: "invalid"}, IfMatch: fmtETag(2)}, Body: workBaselineBody{RequirementVersionNo: 2, CriterionIDs: ids}}
	_, err := m.workBaselineHTTP(ctx, in)
	assertWorkStatus(t, err, 422)
	in.WorkItemID = "333"
	_, err = m.workBaselineHTTP(ctx, in)
	assertWorkStatus(t, err, 404)
	in.WorkItemID = id.String()
	m.runtime = nil
	_, err = m.workBaselineHTTP(ctx, in)
	assertWorkStatus(t, err, 503)
}
