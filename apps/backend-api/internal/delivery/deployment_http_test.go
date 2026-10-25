package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestDeploymentHTTPReceiptReplayAndMismatch(t *testing.T) {
	h, ctx, body := deploymentFixture(t)
	in := &deploymentEventInput{EntityID: "99", WorkItemID: h.work.String(), IdempotencyKey: "trusted-deployment-http", Body: body}
	first, err := h.m.deploymentEventHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "DEPLOYED", first.Body.Data.Stage)
	replay, err := h.m.deploymentEventHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	in.IdempotencyKey = "another-deployment"
	in.Body.Commit = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	_, err = h.m.deploymentEventHTTP(ctx, in)
	assertWorkStatus(t, err, 409)
	raw, e := respx.MarshalEnvelope(ctx, err)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"code":126006`)
}

func TestDefectLifecycleHTTPPublishesExactRejectionContracts(t *testing.T) {
	h, _ := acceptedDefectFixture(t)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "REWORK").Error)
	in := &defectReopenInput{EntityID: "99", WorkItemID: h.work.String(), IfMatch: fmtETag(h.stored().Version), IdempotencyKey: "reject-processing-reopen", Body: defectReopenBody{Reason: "Regression reproduced during ongoing work"}}
	_, err := h.m.defectReopenHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 409)
	raw, e := respx.MarshalEnvelope(h.ctx, err)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"code":126007`)
	err = resultHTTPError(h.ctx, ErrDefectAgentAccept)
	assertWorkStatus(t, err, 403)
	raw, e = respx.MarshalEnvelope(h.ctx, err)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"reason_code":"AGENT_CANNOT_ACCEPT"`)
}

func TestDeploymentHTTPRejectsHumanAndInvalidPaths(t *testing.T) {
	h, _, body := deploymentFixture(t)
	in := &deploymentEventInput{EntityID: "99", WorkItemID: h.work.String(), Body: body}
	_, err := h.m.deploymentEventHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 403)
	in.EntityID = "bad"
	_, err = h.m.deploymentEventHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 422)
	in.EntityID = "99"
	in.WorkItemID = "bad"
	_, err = h.m.deploymentEventHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 422)
}

func TestDefectReopenHTTPImmutableSourceAndReplay(t *testing.T) {
	h, _ := acceptedDefectFixture(t)
	before := h.stored()
	in := &defectReopenInput{EntityID: "99", WorkItemID: h.work.String(), IfMatch: fmtETag(before.Version), IdempotencyKey: "defect-reopen-http", Body: defectReopenBody{Reason: "Regression reproduced by the Human tester"}}
	first, err := h.m.defectReopenHTTP(h.actor(12), in)
	require.NoError(t, err)
	replay, err := h.m.defectReopenHTTP(h.actor(12), in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data.ID, replay.Body.Data.ID)
	require.Equal(t, before, h.stored())
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = h.m.defectReopenHTTP(h.actor(12), in)
	assertWorkStatus(t, err, 404)
}
