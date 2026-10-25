package delivery

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDefectManualVerificationUsesExactAcceptedIndependentResult(t *testing.T) {
	h, resultID := acceptedDefectFixture(t)
	in := &defectStageInput{EntityID: "99", WorkItemID: h.work.String(), IfMatch: fmtETag(h.stored().Version), IdempotencyKey: "defect-verify", Body: defectStageBody{ResultID: resultID, Stage: "VERIFIED", Reason: "Independent evidence confirms the fix"}}
	verified, err := h.m.defectStageHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "VERIFIED", verified.Body.Data.Stage)
	require.Equal(t, "VERIFIED", *h.stored().DefectStage)
	require.Equal(t, "ACCEPTED", h.stored().State)
	replayed, err := h.m.defectStageHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, verified.Body.Data.Version, replayed.Body.Data.Version)
	in.IfMatch = fmtETag(h.stored().Version)
	in.IdempotencyKey = "defect-close"
	in.Body.Stage = "CLOSED"
	closed, err := h.m.defectStageHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "CLOSED", closed.Body.Data.Stage)
	require.Equal(t, "CLOSED", *h.stored().DefectStage)
	require.Equal(t, "ACCEPTED", h.stored().State)
	h.deniedAuthority = true
	_, err = h.m.defectStageHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 403)
}

func TestDefectHTTPRejectsInvalidPathsMissingWorkAndUnavailableRuntime(t *testing.T) {
	h, result := acceptedDefectFixture(t)
	in := &defectStageInput{EntityID: "99", WorkItemID: "invalid", IfMatch: fmtETag(h.stored().Version), Body: defectStageBody{ResultID: result, Stage: "VERIFIED", Reason: "Manual independent fix verification"}}
	_, err := h.m.defectStageHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 422)
	in.WorkItemID = "333"
	_, err = h.m.defectStageHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 404)
	in.WorkItemID = h.work.String()
	h.m.runtime = nil
	_, err = h.m.defectStageHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 503)
}
