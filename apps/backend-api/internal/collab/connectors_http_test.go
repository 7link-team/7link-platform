package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestConnectorHTTPParsesEmbeddedPathParameters(t *testing.T) {
	_, api := humatest.New(t)
	var parsed *connectorPutHTTPInput
	huma.Register(api, huma.Operation{OperationID: "connector-path-input", Method: "PUT", Path: "/channels/{channel_id}/connectors/{kind}"}, func(_ context.Context, in *connectorPutHTTPInput) (*struct{ Body string }, error) {
		parsed = in
		return &struct{ Body string }{Body: "ok"}, nil
	})
	body := `{"version":0,"endpoint":"https://receiver.example/messages","enabled":true}`
	response := api.Put("/channels/123/connectors/HOOK", "Idempotency-Key: configure", strings.NewReader(body))
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotNil(t, parsed)
	require.Equal(t, "123", parsed.ChannelID)
	require.Equal(t, "HOOK", parsed.Kind)
	require.Equal(t, "configure", parsed.IdempotencyKey)
	require.Equal(t, "https://receiver.example/messages", parsed.Body.Endpoint)
	for _, path := range []string{"/channels/invalid/connectors/HOOK", "/channels/0/connectors/HOOK", "/channels/123/connectors/INVALID"} {
		parsed = nil
		response := api.Put(path, "Idempotency-Key: configure", strings.NewReader(body))
		require.Equal(t, 422, response.Code, response.Body.String())
		require.Nil(t, parsed, "invalid parameters must not reach the handler")
	}
}

func TestConnectorHTTPListsBoundedConfigurationAndActualDeliveryFacts(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	m.config.ConnectorEncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	in := &connectorPutHTTPInput{ConnectorKindPath: ConnectorKindPath{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Kind: "HOOK"}, IdempotencyKey: "configure-hook", Body: ConnectorInput{Endpoint: "https://receiver.example/messages", Secret: strings.Repeat("s", 32), Enabled: true}}
	configured, err := m.configureConnectorHTTP(ctx, in)
	require.NoError(t, err)
	replayed, err := m.configureConnectorHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, configured.Body.Data, replayed.Body.Data)
	configureTestConnector(t, m, db, ch.ID, "HTTP_BRIDGE")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "queued payload", Visibility: "CHANNEL"})
	page, err := m.listConnectorsHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Body.Data.Total)
	require.Len(t, page.Body.Data.Items, 1)
	filtered, err := m.listConnectorsHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 10, Search: "HOOK"})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Body.Data.Total)
	require.Equal(t, "HOOK", filtered.Body.Data.Items[0].Kind)
	raw, err := json.Marshal(filtered.Body.Data)
	require.NoError(t, err)
	require.NotContains(t, string(raw), strings.Repeat("s", 32))
	last, err := m.listConnectorsHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, last.Body.Data.Items, 1)
	require.NotEqual(t, page.Body.Data.Items[0].ID, last.Body.Data.Items[0].ID)
	deliveries, err := m.listConnectorDeliveriesHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 1, Search: "PENDING"})
	require.NoError(t, err)
	require.EqualValues(t, 2, deliveries.Body.Data.Total)
	require.Len(t, deliveries.Body.Data.Items, 1)
	require.Equal(t, "PENDING", deliveries.Body.Data.Items[0].State)
	require.Zero(t, deliveries.Body.Data.Items[0].Attempts)
	for _, limit := range []int{0, 201} {
		_, err = m.listConnectorsHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: limit})
		var response huma.StatusError
		require.ErrorAs(t, err, &response)
		require.Equal(t, 422, response.GetStatus())
		_, err = m.listConnectorDeliveriesHTTP(ctx, &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: limit})
		require.ErrorAs(t, err, &response)
		require.Equal(t, 422, response.GetStatus())
	}
	bridge := &connectorBridgeHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, IdempotencyKey: "inbound-bridge"}
	bridge.Body.Body = "actual upstream message"
	message, err := m.receiveBridgeHTTP(ctx, bridge)
	require.NoError(t, err)
	require.Equal(t, bridge.Body.Body, message.Body.Data.Body)
	var rows []model.ChannelDelivery
	require.NoError(t, query.BindDeliveries(db, m.names).Where(&model.ChannelDelivery{MessageID: message.Body.Data.ID}).Find(&rows).Error)
	require.Len(t, rows, 1, "inbound bridge skips its origin while still delivering to other hooks")
	require.Equal(t, configured.Body.Data.ID, rows[0].ConnectorID)
}

func TestConnectorHTTPPropagatesReadFailuresAndRejectsForeignChannel(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	for _, kind := range []string{"config", "delivery"} {
		for _, operation := range []string{"count", "rows"} {
			failure := errors.New("private connector storage failure")
			table := m.names.Connectors.String()
			if kind == "delivery" {
				table = m.names.Deliveries.String()
			}
			reads := 0
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("connector_http_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					reads++
					if operation == "count" && reads == 1 || operation == "rows" && reads == 2 {
						tx.AddError(failure)
					}
				}
			}))
			in := &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 10}
			var err error
			if kind == "config" {
				_, err = m.listConnectorsHTTP(ctx, in)
			} else {
				_, err = m.listConnectorDeliveriesHTTP(ctx, in)
			}
			require.Error(t, err)
			require.NoError(t, db.Callback().Query().Remove("connector_http_failure"))
		}
	}
	_, err := m.listConnectorsHTTP(conversationContext(t, 4), &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 10})
	var response huma.StatusError
	require.ErrorAs(t, err, &response)
	require.Equal(t, 404, response.GetStatus(), "a nonmember cannot discover the channel")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{})
		return err
	}))
	_, err = m.listConnectorsHTTP(conversationContext(t, 4), &connectorListHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, Limit: 10})
	require.ErrorAs(t, err, &response)
	require.Equal(t, 403, response.GetStatus())
}

func TestChannelConnectorHTTPPathsUseBoundedStringIDs(t *testing.T) {
	// Domain-backed HTTP handlers reject malformed identifiers before DB access.
	m := &Module{}
	_, e := m.configureConnectorHTTP(t.Context(), &connectorPutHTTPInput{ConnectorKindPath: ConnectorKindPath{ConversationChannelInput: ConversationChannelInput{ChannelID: "invalid"}}})
	require.Error(t, e)
	_, e = m.receiveBridgeHTTP(t.Context(), &connectorBridgeHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "0"}})
	require.Error(t, e)
}
