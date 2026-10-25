package collab

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"net/http"
)

type mediaSearchHTTPInput struct {
	ConversationChannelInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Query    string `json:"query" minLength:"1" maxLength:"200"`
		Approved bool   `json:"approved" doc:"Human approval to send this exact query to the configured external provider, including any confidential data."`
	}
}

func (m *Module) registerMediaHTTP(api huma.API) {
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodPost, Path: path, Summary: summary, Tags: []string{"channels"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.media.transcribe", "/channels/{channel_id}/messages/{message_id}/attachments/{attachment_id}/transcription", "Transcribe authorized pinned audio with a configured provider"), "channel_id", m.channelOwner, m.transcribeHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.media.search", "/channels/{channel_id}/search-cards", "Search with explicit Human approval and cited provider results"), "channel_id", m.channelOwner, m.searchMediaHTTP)
}
func (m *Module) transcribeHTTP(ctx context.Context, in *AttachmentPath) (*respx.Body[TranscriptionResult], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	message, e := guid.RequireID(in.MessageID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	attachment, e := guid.RequireID(in.AttachmentID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	out, e := m.transcribeAttachment(ctx, channel, message, attachment, connectorClient())
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) searchMediaHTTP(ctx context.Context, in *mediaSearchHTTPInput) (*respx.Body[SearchCard], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	out, e := m.searchChannelRequest(ctx, channel, in.Body.Query, in.Body.Approved, connectorClient(), in.IdempotencyKey)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
