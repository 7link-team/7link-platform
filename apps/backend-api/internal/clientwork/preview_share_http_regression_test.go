package clientwork

import (
	"context"
	"net/http"
	"testing"

	"7link.com/platform/internal/delivery"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreviewShareHTTPApprovalReplayAndPrivateContent(t *testing.T) {
	m, db, projects, clients := harness(t)
	ctx := testContext(t, 2)
	var preview Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		preview, err = m.createPreviewOn(ctx, tx, 3, PreviewBody{Title: "Fixed preview", SourceStage: "PROGRESS", Artifact: delivery.ClientArtifactRef{WorkItemID: 11, ResultID: 20, EvidenceID: 30}})
		return err
	}))
	request := &CreateInput[PreviewShareBody]{EntityID: "3", Key: "share-create", Body: PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 24, Note: "Review fixed content"}}
	created, err := m.requestShareHTTP(ctx, request)
	require.NoError(t, err)
	replay, err := m.requestShareHTTP(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data, replay.Body.Data)
	input := &ObjectInput{EntityID: "3", ID: created.Body.Data.ID.String()}
	pending, err := m.getShareHTTP(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "PENDING_APPROVAL", pending.Body.Data.State)
	require.Nil(t, pending.Body.Data.Preview)
	list, err := m.listSharesHTTP(ctx, &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, err)
	require.Len(t, list.Body.Data.Items, 1)
	list, err = m.listSharesHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, err)
	require.Empty(t, list.Body.Data.Items)
	decision := &EditInput[PreviewShareDecision]{EntityID: "3", ID: input.ID, Key: "share-approve", IfMatch: "\"1\"", Body: PreviewShareDecision{Decision: "APPROVE", Reason: "Audience checked"}}
	approved, err := m.decideShareHTTP(testContext(t, 6), decision)
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", approved.Body.Data.State)
	replay, err = m.decideShareHTTP(testContext(t, 6), decision)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	list, err = m.listSharesHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, err)
	require.Len(t, list.Body.Data.Items, 1)
	list, err = m.listSharesHTTP(testContext(t, 5), &ListInput{EntityID: "3", Limit: 20})
	require.NoError(t, err)
	require.Empty(t, list.Body.Data.Items)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "share.content.test", Method: http.MethodGet, Path: "/content"}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return m.shareContentHTTP(testContext(t, 4), input)
	})
	response := api.Get("/content")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "test", response.Body.String())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	clients.revoked = true
	_, err = m.shareContentHTTP(testContext(t, 4), input)
	require.Error(t, err)
	clients.revoked = false
	projects.deny = true
	_, err = m.decideShareHTTP(testContext(t, 6), decision)
	require.Error(t, err, "replay must recheck current actor authority")
	projects.deny = false
	decision.Key, decision.IfMatch, decision.Body.Decision = "share-revoke", "\"2\"", "REVOKE"
	revoked, err := m.decideShareHTTP(ctx, decision)
	require.NoError(t, err)
	require.Equal(t, "REVOKED", revoked.Body.Data.State)
	_, err = m.shareContentHTTP(testContext(t, 4), input)
	require.Error(t, err)
}

func TestPreviewShareHTTPInvalidInputsFailClosed(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	_, err := m.requestShareHTTP(ctx, &CreateInput[PreviewShareBody]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.decideShareHTTP(ctx, &EditInput[PreviewShareDecision]{EntityID: "3", ID: "1", IfMatch: "bad"})
	require.Error(t, err)
	for _, input := range []*ObjectInput{{EntityID: "bad"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}, {EntityID: "8", ID: "999"}} {
		_, err = m.getShareHTTP(ctx, input)
		require.Error(t, err)
		_, err = m.shareContentHTTP(ctx, input)
		require.Error(t, err)
	}
	_, err = (&Module{}).getShareHTTP(ctx, &ObjectInput{EntityID: "3", ID: "1"})
	require.Error(t, err)
	_, err = (&Module{}).shareContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: "1"})
	require.Error(t, err)
}
