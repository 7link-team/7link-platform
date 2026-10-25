package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestChannelDispatchConnectorRejectsMissingAndDuplicateBindings(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideChannelDispatch(s))
	require.NoError(t, s.Seal())
	connector, e := module.Resolve[ChannelDispatchConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
}

func TestChannelDispatchRequiresHumanSourceAndExplicitMention(t *testing.T) {
	for _, scenario := range []string{"success", "no_mention", "external_author", "agent_author", "self_request"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			members := query.BindMembers(db, m.names)
			require.NoError(t, members.WithContext(t.Context()).Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 7, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
			input := PostMessageInput{Body: "Please prepare a proposal", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{7}}
			if scenario == "no_mention" {
				input.MentionPrincipalIDs = nil
			}
			message := postConversation(t, m, db, ch.ID, input)
			if scenario == "external_author" {
				_, e := members.WithContext(t.Context()).Where(members.ChannelID.Eq(ch.ID), members.PrincipalID.Eq(guid.ID(2))).Update(members.External, true)
				require.NoError(t, e)
			}
			if scenario == "agent_author" {
				_, e := members.WithContext(t.Context()).Where(members.ChannelID.Eq(ch.ID), members.PrincipalID.Eq(guid.ID(2))).Update(members.MemberKind, "AGENT")
				require.NoError(t, e)
			}
			called := false
			m.channelDispatch = func(_ context.Context, _ *gorm.DB, in ChannelDispatchRequest) (ChannelDispatchResult, error) {
				called = true
				require.EqualValues(t, 2, in.RequestedBy)
				require.Equal(t, message.ID, in.SourceMessageID)
				return ChannelDispatchResult{ProposalID: 91, RequiresHuman: true}, nil
			}
			actor := guid.ID(7)
			if scenario == "self_request" {
				actor = 2
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.channelDispatchOn(conversationContext(t, actor), tx, 1, 3, ch.ID, message.ID, 11, 1)
				return e
			})
			if scenario == "success" {
				require.NoError(t, err)
				require.True(t, called)
			} else {
				require.ErrorIs(t, err, ErrForbidden)
				require.False(t, called)
			}
		})
	}
}
