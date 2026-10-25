package delivery

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestScopeRequestChangesPreservesResultAndRequiresExplicitRebaseline(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET baseline_stale=1,baseline_stale_since=2 WHERE id=?", h.work).Error)
	in := &scopeReworkInput{ResultVersionInput: ResultVersionInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IfMatch: fmtETag(h.stored().Version), IdempotencyKey: "scope-return"}, ResultID: result.ID.String()}, Body: scopeReworkBody{FailureCategory: "SCOPE_CHANGE", Reason: "Reconcile the revised business scope", Feedback: []resultFeedbackBody{{Kind: "FEEDBACK", Category: "SCOPE_CHANGE", Detail: "Apply the newly approved scope before resubmission"}}}}
	in.Body.SeenSeq = new(int64)
	before := h.snapshot()
	_, err = h.m.scopeReworkHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 409)
	require.Equal(t, before, h.snapshot(), "a stale decision cannot create feedback or rework")
	in.Body.SeenSeq = new(h.stored().ActivitySeq)
	returned, err := h.m.scopeReworkHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "REWORK", h.stored().State)
	require.True(t, h.stored().BaselineStale)
	require.Zero(t, h.stored().QualityReworkCount)
	require.Equal(t, result.ID, returned.Body.Data.ID)
	page, err := h.m.listResultsHTTP(h.actor(12), &resultListInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, result.Summary, page.Body.Data[0].Summary)
	require.Len(t, page.Body.Data[0].Actions, 1)
	require.Equal(t, "RETURN", page.Body.Data[0].Actions[0].Kind)
	feedback, total, err := h.m.resultFeedbackOn(h.actor(12), h.db, 1, 99, h.work, result.ID, 12, 0, 200)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "SCOPE_CHANGE", feedback[0].Category)
	replayed, err := h.m.scopeReworkHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	h.deniedAuthority = true
	_, err = h.m.scopeReworkHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 403)
}

func TestReworkHTTPRejectsInvalidIdentifiersMissingWorkAndUnavailableRuntime(t *testing.T) {
	h := newResultFixture(t)
	in := &workResumeInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: "invalid"}, IfMatch: fmtETag(2)}, Body: workResumeBody{Reason: "Human authorizes another attempt"}}
	_, err := h.m.resumeManualWorkHTTP(h.actor(13), in)
	assertWorkStatus(t, err, 422)
	in.WorkItemID = "333"
	_, err = h.m.resumeManualWorkHTTP(h.actor(13), in)
	assertWorkStatus(t, err, 404)
	in.WorkItemID = h.work.String()
	_, err = h.m.scopeReworkHTTP(h.actor(13), &scopeReworkInput{ResultVersionInput: ResultVersionInput{ResultMutationInput: in.ResultMutationInput, ResultID: "invalid"}})
	assertWorkStatus(t, err, 422)
	h.m.runtime = nil
	_, err = h.m.resumeManualWorkHTTP(h.actor(13), in)
	assertWorkStatus(t, err, 503)
}

func TestQualityThirdReturnPausesAndHumanCanAuthorizeManualResume(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	handoff, err := h.handoff(result.ID)
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET quality_rework_count=2 WHERE id=?", h.work).Error)
	h.m.service.events = realDeliveryPublisher(t, h.db)
	_, err = h.receive(result.ID, handoff.Handoffs[0].ID, 13, false)
	require.NoError(t, err)
	paused := h.stored()
	require.Equal(t, 3, paused.QualityReworkCount)
	require.NotNil(t, paused.PausedAt)
	require.Equal(t, "AUTONOMY_REWORK_LIMIT", *paused.PauseReason)
	require.True(t, paused.RequireHumanDecision)
	var escalationCount int64
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ?", "work_item.escalated").Count(&escalationCount).Error)
	require.EqualValues(t, 1, escalationCount)
	var escalation struct{ Data string }
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ?", "work_item.escalated").Take(&escalation).Error)
	require.Contains(t, escalation.Data, `"count":3`)
	_, err = h.submit()
	require.Error(t, err)
	in := &workResumeInput{ResultMutationInput: ResultMutationInput{ResultWorkInput: ResultWorkInput{EntityID: "99", WorkItemID: h.work.String()}, IfMatch: fmtETag(paused.Version), IdempotencyKey: "resume-human"}, Body: workResumeBody{Reason: "Human approves another bounded rework attempt"}}
	resumed, err := h.m.resumeManualWorkHTTP(h.actor(13), in)
	require.NoError(t, err)
	require.Equal(t, paused.Version+1, resumed.Body.Data.Version)
	require.Nil(t, h.stored().PausedAt)
	require.Nil(t, h.stored().PauseReason)
	require.True(t, h.stored().RequireHumanDecision)
	replayed, err := h.m.resumeManualWorkHTTP(h.actor(13), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.NoError(t, h.db.Table("domain_events").Where("event_type = ?", "work_item.escalated").Count(&escalationCount).Error)
	require.EqualValues(t, 1, escalationCount)
	h.deniedAuthority = true
	_, err = h.m.resumeManualWorkHTTP(h.actor(13), in)
	assertWorkStatus(t, err, 403)
	h.deniedAuthority = false
	next, err := h.submit()
	require.NoError(t, err)
	require.Equal(t, result.ID, *next.PreviousResultID)
}
