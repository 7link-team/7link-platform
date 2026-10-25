package clientwork

import (
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPreviewShareHTTPReplayCurrentAuthorityAndScope(t *testing.T) {
	m, db, _, clients := harness(t)
	preview, first := previewShareFixture(t, m, db)
	_, err := m.decideShareHTTP(testContext(t, 6), &EditInput[PreviewShareDecision]{EntityID: "3", ID: first.ID.String(), Key: "reject-first", IfMatch: `"1"`, Body: PreviewShareDecision{Decision: "REJECT", Reason: "Replace"}})
	require.NoError(t, err)
	input := &CreateInput[PreviewShareBody]{EntityID: "3", Key: "share-request", Body: PreviewShareBody{PreviewID: preview.ID, RecipientID: 4, TTLHours: 1, Note: "Fixed source"}}
	created, err := m.requestShareHTTP(testContext(t, 2), input)
	require.NoError(t, err)
	replay, err := m.requestShareHTTP(testContext(t, 2), input)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data.ID, replay.Body.Data.ID)
	decision := &EditInput[PreviewShareDecision]{EntityID: "3", ID: created.Body.Data.ID.String(), Key: "approve-share", IfMatch: `"1"`, Body: PreviewShareDecision{Decision: "APPROVE", Reason: "Audience checked"}}
	_, err = m.decideShareHTTP(testContext(t, 6), decision)
	require.NoError(t, err)
	replay, err = m.decideShareHTTP(testContext(t, 6), decision)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	clients.revoked = true
	_, err = m.getShareHTTP(testContext(t, 4), &ObjectInput{EntityID: "3", ID: decision.ID})
	require.Error(t, err)
	clients.revoked = false
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: decision.ID}, {EntityID: "3", ID: "bad"}, {EntityID: "8", ID: decision.ID}, {EntityID: "3", ID: "999"}} {
		_, err = m.getShareHTTP(testContext(t, 4), in)
		require.Error(t, err)
		_, err = m.shareContentHTTP(testContext(t, 4), in)
		require.Error(t, err)
	}
	_, err = m.requestShareHTTP(testContext(t, 2), &CreateInput[PreviewShareBody]{EntityID: "bad"})
	require.Error(t, err)
	_, err = m.decideShareHTTP(testContext(t, 6), &EditInput[PreviewShareDecision]{EntityID: "bad"})
	require.Error(t, err)
}

func TestPreviewShareHTTPContentIsSandboxedAndExpires(t *testing.T) {
	m, db, _, clients := harness(t)
	_, share := previewShareFixture(t, m, db)
	_, err := m.decideShareHTTP(testContext(t, 6), &EditInput[PreviewShareDecision]{EntityID: "3", ID: share.ID.String(), Key: "approve-content", IfMatch: `"1"`, Body: PreviewShareDecision{Decision: "APPROVE", Reason: "Checked"}})
	require.NoError(t, err)
	original := m.files.OpenOn
	m.files.OpenOn = func(ctx context.Context, tx *gorm.DB, id guid.ID) (files.PinnedResourceRead, error) {
		file, e := original(ctx, tx, id)
		file.MIME = "text/html"
		return file, e
	}
	_, api := humatest.New(t)
	api.UseMiddleware(func(c huma.Context, next func(huma.Context)) { next(huma.WithContext(c, testContext(t, 4))) })
	huma.Register(api, huma.Operation{OperationID: "share-content-test", Method: "GET", Path: "/content", Responses: binaryContentResponses()}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return m.shareContentHTTP(ctx, &ObjectInput{EntityID: "3", ID: share.ID.String()})
	})
	response := api.Get("/content")
	require.Equal(t, 200, response.Code)
	require.Equal(t, "test", response.Body.String())
	require.Equal(t, "text/plain", response.Header().Get("Content-Type"))
	require.Contains(t, response.Header().Get("Content-Security-Policy"), "sandbox")
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.True(t, strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline"))
	clients.revoked = true
	require.NotEqual(t, 200, api.Get("/content").Code)
	clients.revoked = false
	q := query.BindPreviewShare(db, m.names)
	_, err = q.WithContext(testContext(t, 2)).Where(q.ID.Eq(share.ID)).Update(q.ExpiresAt, time.Now().Add(-time.Second))
	require.NoError(t, err)
	require.Equal(t, 410, api.Get("/content").Code)
}
