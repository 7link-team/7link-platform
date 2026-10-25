package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestResultFeedbackExactVersionReplayAndCurrentAuthorization(t *testing.T) {
	m, db, ctx, in := feedbackHarness(t)
	created, err := m.appendResultFeedbackHTTP(ctx, in)
	require.NoError(t, err)
	replayed, err := m.appendResultFeedbackHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replayed.Replayed)
	require.Equal(t, created.Body.Data.ID, replayed.Body.Data.ID)
	page, err := m.listResultFeedbackHTTP(ctx, &resultFeedbackListInput{EntityID: "99", WorkItemID: in.WorkItemID, ResultID: "801", Limit: 50})
	require.NoError(t, err)
	require.Len(t, page.Body.Data, 1)
	require.Equal(t, in.Body.Detail, page.Body.Data[0].Detail)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true, DeniedIDs: []string{"99"}}, nil
	}
	_, err = m.appendResultFeedbackHTTP(ctx, in)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Table("d_delivery_result_feedback").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestResultFeedbackHTTPRejectsInvalidPathsMissingWorkAndUnavailableRuntime(t *testing.T) {
	m, _, ctx, in := feedbackHarness(t)
	in.ResultID = "invalid"
	_, err := m.appendResultFeedbackHTTP(ctx, in)
	assertWorkStatus(t, err, 422)
	_, err = m.listResultFeedbackHTTP(ctx, &resultFeedbackListInput{EntityID: "99", WorkItemID: in.WorkItemID, ResultID: "invalid", Limit: 50})
	assertWorkStatus(t, err, 422)
	in.ResultID, in.WorkItemID = "801", "333"
	_, err = m.appendResultFeedbackHTTP(ctx, in)
	assertWorkStatus(t, err, 404)
	m.runtime = nil
	_, err = m.appendResultFeedbackHTTP(ctx, in)
	assertWorkStatus(t, err, 503)
	_, err = m.listResultFeedbackHTTP(ctx, &resultFeedbackListInput{EntityID: "99", WorkItemID: "333", ResultID: "801", Limit: 50})
	assertWorkStatus(t, err, 503)
}
