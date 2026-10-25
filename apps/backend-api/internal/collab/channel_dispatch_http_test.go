package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestChannelDispatchHTTPRejectsMalformedSource(t *testing.T) {
	m := &Module{}
	_, e := m.channelDispatchHTTP(t.Context(), &channelDispatchHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: "invalid"}})
	require.Error(t, e)
}

func TestChannelDispatchHTTPReplaysWithCurrentSourceAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Table(m.names.Members.String()).Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 7, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}).Error)
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "prepare a proposal", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{7}})
	calls, checks := 0, 0
	m.channelDispatch = func(_ context.Context, _ *gorm.DB, in ChannelDispatchRequest) (ChannelDispatchResult, error) {
		require.Equal(t, guid.ID(2), in.RequestedBy)
		require.Equal(t, guid.ID(11), in.WorkItemID)
		if in.CheckOnly {
			checks++
		} else {
			calls++
		}
		return ChannelDispatchResult{ProposalID: 91, RequiresHuman: true}, nil
	}
	in := &channelDispatchHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), IdempotencyKey: "proposal"}
	in.Body.WorkItemID, in.Body.ExpectedWorkVersion = 11, 1
	for range 2 {
		out, err := m.channelDispatchHTTP(conversationContext(t, 7), in)
		require.NoError(t, err)
		require.Equal(t, guid.ID(91), out.Body.Data.ProposalID)
		require.True(t, out.Body.Data.RequiresHuman)
	}
	require.Equal(t, 1, calls)
	require.Positive(t, checks)
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", message.ID).Update("withdrawn_at", m.now()).Error)
	out, err := m.channelDispatchHTTP(conversationContext(t, 7), in)
	require.Error(t, err)
	require.Nil(t, out)
	require.Equal(t, 1, calls)
	in.MessageID = "bad"
	_, err = m.channelDispatchHTTP(conversationContext(t, 7), in)
	require.Error(t, err)
	in.ChannelID, in.MessageID = "999", "1"
	_, err = m.channelDispatchHTTP(conversationContext(t, 7), in)
	require.Error(t, err)
}
