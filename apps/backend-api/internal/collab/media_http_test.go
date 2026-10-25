package collab

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChannelMediaHTTPRejectsMalformedAttachmentPaths(t *testing.T) {
	m := &Module{}
	_, e := m.transcribeHTTP(t.Context(), &AttachmentPath{ConversationChannelInput: ConversationChannelInput{ChannelID: "invalid"}})
	require.Error(t, e)
	_, e = m.searchMediaHTTP(t.Context(), &mediaSearchHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "0"}})
	require.Error(t, e)
}

func TestMediaHTTPValidatesEachPathAndMapsMissingResources(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	for _, path := range []AttachmentPath{
		{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: "bad", AttachmentID: "7"},
		{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: "6", AttachmentID: "bad"},
		{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: "6", AttachmentID: "7"},
	} {
		out, err := m.transcribeHTTP(ctx, &path)
		require.Nil(t, out)
		var status huma.StatusError
		require.ErrorAs(t, err, &status)
		require.Contains(t, []int{422, 404}, status.GetStatus())
	}
	in := &mediaSearchHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, IdempotencyKey: "http-search"}
	in.Body.Query, in.Body.Approved = "query", true
	out, err := m.searchMediaHTTP(ctx, in)
	require.Nil(t, out)
	var status huma.StatusError
	require.ErrorAs(t, err, &status)
	require.Equal(t, 404, status.GetStatus())
	configureTestConnector(t, m, db, ch.ID, "SEARCH")
	client := &http.Client{Transport: connectorTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"sources":[{"title":"source","url":"https://example.com"}],"conclusions":[]}`))}, nil
	})}
	card, err := m.searchChannelRequest(ctx, ch.ID, "query", true, client, "http-search")
	require.NoError(t, err)
	out, err = m.searchMediaHTTP(ctx, in)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Greater(t, card.MessageID, guid.ID(0))
	require.Equal(t, card, out.Body.Data)
}
