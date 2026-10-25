package collab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRelationshipWritesCarryMutationIntentAndPreserveHistory(t *testing.T) {
	for _, command := range []string{"retract", "channel_update", "member_remove", "member_put", "connector_configure"} {
		t.Run(command, func(t *testing.T) {
			m, db := conversationHarness(t)
			ctx := conversationContext(t, 2)
			channel := createConversation(t, m, db, "INTERNAL")
			message := postConversation(t, m, db, channel.ID, PostMessageInput{Body: "Preserve historical content", Visibility: "CHANNEL"})
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.putMemberOn(ctx, tx, 1, 3, channel.ID, 4, MemberInput{Role: "MEMBER"})
				return err
			}))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				row, _, err := m.authorizeChannel(ctx, tx, 1, 3, channel.ID, "channel_read")
				if err == nil {
					channel = channelView(row)
				}
				return err
			}))
			m.config.ConnectorEncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
			archived := true
			blocked := errors.New("project state fence: read only")
			m.policy = func(policyCtx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
				_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
				require.True(t, active, "state fencing shares the domain transaction")
				require.Equal(t, guid.ID(1), tenant)
				require.Equal(t, guid.ID(3), project)
				if archived && core.EntityMutationFromContext(policyCtx) {
					return authz.DataConstraint{}, blocked
				}
				// Existing author, OWNER and self-leave semantics need no manage grant.
				return authz.DataConstraint{AllowAll: permission == "channel_read"}, nil
			}
			run := func(tx *gorm.DB) error {
				switch command {
				case "retract":
					_, err := m.mutateMessageOn(ctx, tx, 1, message.ID, message.Version, EditMessageInput{}, true)
					return err
				case "channel_update":
					name := "Renamed discussion"
					_, err := m.updateChannelOn(ctx, tx, 1, 3, channel.ID, ChannelUpdateInput{Version: channel.Version, Name: &name})
					return err
				case "member_remove":
					return m.removeChannelMemberOn(conversationContext(t, 4), tx, 1, 3, channel.ID, 4)
				case "member_put":
					_, err := m.putMemberOn(ctx, tx, 1, 3, channel.ID, 5, MemberInput{Role: "MEMBER"})
					return err
				default:
					_, err := m.configureConnectorOn(ctx, tx, 1, 3, channel.ID, "HOOK", ConnectorInput{Endpoint: "https://receiver.example/messages", Secret: strings.Repeat("s", 32), Enabled: true})
					return err
				}
			}
			snapshot := func() string {
				state := map[string][]map[string]any{}
				for _, table := range []string{"c_collab_channels", "c_collab_channel_members", "c_collab_messages", "c_collab_message_revisions", "c_collab_channel_connectors", "audit_probe", "c_domain_events"} {
					var rows []map[string]any
					require.NoError(t, db.Table(table).Find(&rows).Error)
					state[table] = rows
				}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				return string(encoded)
			}
			before := snapshot()
			require.ErrorIs(t, db.Transaction(run), blocked)
			require.Equal(t, before, snapshot(), "rejected writes preserve business rows, audit and outbox")
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, _, err := m.authorizeChannel(ctx, tx, 1, 3, channel.ID, "channel_read")
				return err
			}), "historical reads remain available with current authorization")
			archived = false
			require.NoError(t, db.Transaction(run), "active-project relationship permissions remain sufficient")
			require.NotEqual(t, before, snapshot())
		})
	}
}
