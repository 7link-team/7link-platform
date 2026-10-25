package collab

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

type channelDispatchHTTPInput struct {
	ConversationChannelInput
	MessageID      string `path:"message_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		WorkItemID          guid.ID `json:"work_item_id"`
		ExpectedWorkVersion int     `json:"expected_work_version" minimum:"1"`
	}
}

func (m *Module) registerChannelDispatchHTTP(api huma.API) {
	authz.RegisterOwnedTenantMember(m.registrar, api, huma.Operation{OperationID: "channel.dispatch.propose", Method: http.MethodPost, Path: "/channels/{channel_id}/messages/{message_id}/dispatch-proposals", Summary: "Create a Human-confirmable proposal from a Human message mentioning the lead Agent", Tags: []string{"channels"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, "channel_id", m.channelOwner, m.channelDispatchHTTP)
}
func (m *Module) channelDispatchHTTP(ctx context.Context, in *channelDispatchHTTPInput) (*commandOutput[ChannelDispatchResult], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	message, e := guid.RequireID(in.MessageID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.dispatch.propose", "/channels/{channel_id}/messages/{message_id}/dispatch-proposals", map[string]string{"channel_id": in.ChannelID, "message_id": in.MessageID}, in.Body, 200, func(tx *gorm.DB) (ChannelDispatchResult, error) {
		return m.channelDispatchOn(ctx, tx, tenant, project, channel, message, in.Body.WorkItemID, in.Body.ExpectedWorkVersion)
	}, func(tx *gorm.DB, _ ChannelDispatchResult) error {
		_, e := m.channelDispatchOn(ctx, tx, tenant, project, channel, message, in.Body.WorkItemID, in.Body.ExpectedWorkVersion, true)
		return e
	})
}
