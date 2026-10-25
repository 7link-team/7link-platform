package delivery

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReviewPlanHTTPProposeListAndReplay(t *testing.T) {
	m, _, ctx, work, _ := readinessHarness(t)
	in := &reviewPlanProposeInput{EntityID: "99", Key: "plan-proposal-once"}
	in.Body.WorkItemID = work.ID
	in.Body.WorkItemVersion = work.Version
	in.Body.Content = ReviewPlanContent{Files: []string{}, Risks: []string{}, OutOfScopeConfirmed: true}
	for _, c := range work.Criteria {
		in.Body.Content.CriteriaCoverage = append(in.Body.Content.CriteriaCoverage, PlanCoverage{CriterionID: c.ID, Approach: "Implement criterion", Verification: "Check independent evidence"})
	}
	first, e := m.proposeReviewPlanHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.proposeReviewPlanHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	page, e := m.listReviewPlansHTTP(ctx, &reviewPlansListInput{EntityID: "99", Limit: 50})
	require.NoError(t, e)
	require.Len(t, page.Body.Data, 1)
}

func TestReviewPlanHTTPRejectsOverflowIDsAndMissingRuntime(t *testing.T) {
	h := newResultFixture(t)
	for _, in := range []reviewPlansListInput{{EntityID: "9999999999999999999", Limit: 1}, {EntityID: "99", WorkItemID: "9999999999999999999", Limit: 1}} {
		out, e := h.m.listReviewPlansHTTP(h.ctx, &in)
		require.Nil(t, out)
		assertWorkStatus(t, e, 422)
	}
	out, e := h.m.proposeReviewPlanHTTP(h.ctx, &reviewPlanProposeInput{EntityID: "9999999999999999999"})
	require.Nil(t, out)
	assertWorkStatus(t, e, 422)
	missing := &reviewPlanProposeInput{EntityID: "99", Key: "missing-work"}
	missing.Body.WorkItemID = 999
	_, e = h.m.proposeReviewPlanHTTP(h.ctx, missing)
	assertWorkStatus(t, e, 404)
	h.m.runtime = nil
	_, e = h.m.listReviewPlansHTTP(h.ctx, &reviewPlansListInput{EntityID: "99", Limit: 1})
	assertWorkStatus(t, e, 503)
	_, e = h.m.proposeReviewPlanHTTP(h.ctx, missing)
	assertWorkStatus(t, e, 503)
}
