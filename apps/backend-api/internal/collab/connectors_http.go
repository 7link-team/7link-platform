package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"time"
)

// ConnectorKindPath stays exported so Huma traverses its embedded path fields.
type ConnectorKindPath struct {
	ConversationChannelInput
	Kind string `path:"kind" enum:"HOOK,HTTP_BRIDGE,TRANSCRIPTION,SEARCH"`
}
type connectorPutHTTPInput struct {
	ConnectorKindPath
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           ConnectorInput
}
type connectorListHTTPInput struct {
	ConversationChannelInput
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Offset int    `query:"offset" default:"0" minimum:"0"`
	Search string `query:"search" maxLength:"200"`
}
type ConnectorPage struct {
	Items  []ConnectorView `json:"items"`
	Total  int64           `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}
type connectorBridgeHTTPInput struct {
	ConversationChannelInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		Body string `json:"body" minLength:"1" maxLength:"8000"`
	}
}

func (m *Module) registerConnectorsHTTP(api huma.API) {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"channels"}, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.connector.deliveries", http.MethodGet, "/channels/{channel_id}/connector-deliveries", "Read actual channel connector delivery attempts and outcomes"), "channel_id", m.channelOwner, m.listConnectorDeliveriesHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.connector.list", http.MethodGet, "/channels/{channel_id}/connectors", "List channel-specific outbound hooks and HTTP bridges"), "channel_id", m.channelOwner, m.listConnectorsHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.connector.configure", http.MethodPut, "/channels/{channel_id}/connectors/{kind}", "Configure a signed channel-specific hook or HTTP bridge"), "channel_id", m.channelOwner, m.configureConnectorHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, op("channel.bridge.receive", http.MethodPost, "/channels/{channel_id}/bridge-messages", "Post authenticated HTTP bridge content without echoing to its source"), "channel_id", m.channelOwner, m.receiveBridgeHTTP)
}
func (m *Module) configureConnectorHTTP(ctx context.Context, in *connectorPutHTTPInput) (*commandOutput[ConnectorView], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.connector.configure", "/channels/{channel_id}/connectors/{kind}", map[string]string{"channel_id": in.ChannelID, "kind": in.Kind}, in.Body, 200, func(tx *gorm.DB) (ConnectorView, error) {
		return m.configureConnectorOn(ctx, tx, tenant, project, channel, in.Kind, in.Body)
	}, func(tx *gorm.DB, _ ConnectorView) error { return m.connectorOwnerOn(ctx, tx, tenant, project, channel) })
}
func (m *Module) listConnectorsHTTP(ctx context.Context, in *connectorListHTTPInput) (*respx.Body[ConnectorPage], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	out := ConnectorPage{Items: []ConnectorView{}, Limit: in.Limit, Offset: in.Offset}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, authn.TenantIDFromContext(ctx), project, channel); e != nil {
			return e
		}
		if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
			return ErrInvalidArgument
		}
		q := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: authn.TenantIDFromContext(ctx), ChannelID: channel})
		if in.Search != "" {
			q = q.Where("kind = ?", in.Search)
		}
		if e := q.Count(&out.Total).Error; e != nil {
			return e
		}
		var rows []model.ChannelConnector
		if e := q.Order("id").Limit(in.Limit).Offset(in.Offset).Find(&rows).Error; e != nil {
			return e
		}
		for _, r := range rows {
			out.Items = append(out.Items, connectorView(r))
		}
		return nil
	})
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) receiveBridgeHTTP(ctx context.Context, in *connectorBridgeHTTPInput) (*commandOutput[MessageView], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return conversationCommand(ctx, m, in.IdempotencyKey, "channel.bridge.receive", "/channels/{channel_id}/bridge-messages", map[string]string{"channel_id": in.ChannelID}, in.Body, 200, func(tx *gorm.DB) (MessageView, error) {
		if e := m.connectorOwnerOn(ctx, tx, tenant, project, channel); e != nil {
			return MessageView{}, e
		}
		var c model.ChannelConnector
		if e := query.BindConnectors(tx, m.names).Where(&model.ChannelConnector{TenantID: tenant, ChannelID: channel, Kind: "HTTP_BRIDGE", Enabled: true}).First(&c).Error; e != nil {
			return MessageView{}, e
		}
		return m.postMessageOn(context.WithValue(ctx, bridgeOriginKey{}, c.ID), tx, tenant, project, channel, PostMessageInput{Body: in.Body.Body, Visibility: "CHANNEL"})
	}, func(tx *gorm.DB, _ MessageView) error { return m.connectorOwnerOn(ctx, tx, tenant, project, channel) })
}

type ConnectorDeliveryView struct {
	ID          guid.ID   `json:"id"`
	ConnectorID guid.ID   `json:"connector_id"`
	MessageID   guid.ID   `json:"message_id"`
	State       string    `json:"state" enum:"PENDING,DELIVERED,FAILED,CANCELLED"` //nolint:misspell // Preserve the published domain or serialized contract spelling.
	Attempts    int       `json:"attempts"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type ConnectorDeliveryPage struct {
	Items  []ConnectorDeliveryView `json:"items"`
	Total  int64                   `json:"total"`
	Limit  int                     `json:"limit"`
	Offset int                     `json:"offset"`
}

func (m *Module) listConnectorDeliveriesHTTP(ctx context.Context, in *connectorListHTTPInput) (*respx.Body[ConnectorDeliveryPage], error) {
	channel, e := guid.RequireID(in.ChannelID)
	if e != nil {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	project, e := m.projectForChannel(ctx, channel)
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	db, e := m.database()
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	out := ConnectorDeliveryPage{Items: []ConnectorDeliveryView{}, Limit: in.Limit, Offset: in.Offset}
	e = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.connectorOwnerOn(ctx, tx, authn.TenantIDFromContext(ctx), project, channel); e != nil {
			return e
		}
		if in.Limit < 1 || in.Limit > 200 || in.Offset < 0 {
			return ErrInvalidArgument
		}
		q := query.BindDeliveries(tx, m.names).Where(&model.ChannelDelivery{TenantID: authn.TenantIDFromContext(ctx), ChannelID: channel})
		if in.Search != "" {
			q = q.Where("state = ?", in.Search)
		}
		if e := q.Count(&out.Total).Error; e != nil {
			return e
		}
		var rows []model.ChannelDelivery
		if e := q.Order("id DESC").Limit(in.Limit).Offset(in.Offset).Find(&rows).Error; e != nil {
			return e
		}
		for _, r := range rows {
			out.Items = append(out.Items, ConnectorDeliveryView{r.ID, r.ConnectorID, r.MessageID, r.State, r.Attempts, r.UpdatedAt})
		}
		return nil
	})
	if e != nil {
		return nil, conversationHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
