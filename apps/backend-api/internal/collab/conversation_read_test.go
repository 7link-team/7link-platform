package collab

import (
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExternalConversationVisibilityBeforePaginationAndUnread(t *testing.T) {
	m, db := conversationHarness(t)
	shared := createConversation(t, m, db, "CLIENT_SHARED")
	internal := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, shared.ID, 4, MemberInput{External: true})
		return e
	}))
	hidden := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Secret internal note", Visibility: "INTERNAL"})
	public := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Shared message", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{4}})
	postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Secret internal reply", Visibility: "CHANNEL", ReplyToMessageID: &hidden.ID})
	postConversation(t, m, db, shared.ID, PostMessageInput{Body: "Another shared message", Visibility: "CHANNEL"})
	client := conversationContext(t, 4)
	var page CursorPage[MessageView]
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listMessagesOn(client, tx, 1, 3, shared.ID, MessageFilter{Limit: 1, Activity: "all"})
		return e
	}))
	require.Len(t, page.Items, 1)
	require.Equal(t, "Another shared message", page.Items[0].Body)
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)
	cursor := page.NextCursor
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listMessagesOn(client, tx, 1, 3, shared.ID, MessageFilter{Limit: 1, Activity: "all", Cursor: cursor})
		return e
	}))
	require.Len(t, page.Items, 1)
	require.Equal(t, public.ID, page.Items[0].ID)
	require.False(t, page.HasMore)
	for _, filter := range []MessageFilter{{Limit: 10, Activity: "all", ThreadID: &hidden.ID}, {Limit: 10, Activity: "mentions", Cursor: cursor}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.listMessagesOn(client, tx, 1, 3, shared.ID, filter); return e }))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.listMessagesOn(client, tx, 1, 3, internal.ID, MessageFilter{Limit: 10, Activity: "all"})
		return e
	}), ErrNotFound)
	var channels CursorPage[ChannelView]
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		channels, e = m.listChannelsOn(client, tx, 1, 3, "", 10)
		return e
	}))
	require.Len(t, channels.Items, 1)
	require.Equal(t, shared.ID, channels.Items[0].ID)
	require.EqualValues(t, 2, channels.Items[0].Unread)
	require.EqualValues(t, 1, channels.Items[0].MentionUnread)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.markReadOn(client, tx, 1, 3, shared.ID, hidden.Seq); return e }), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.markReadOn(client, tx, 1, 3, shared.ID, public.Seq); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		seq, e := m.markReadOn(client, tx, 1, 3, shared.ID, 0)
		require.Equal(t, public.Seq, seq)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(client, tx, 1, 3, shared.ID, PostMessageInput{Body: "Internal?", Visibility: "INTERNAL"})
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(client, tx, 1, 3, shared.ID, hidden.ID, true) }), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(client, tx, 1, 3, shared.ID, public.ID, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(client, tx, 1, 3, shared.ID, public.ID, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listMessagesOn(client, tx, 1, 3, shared.ID, MessageFilter{Limit: 10, Activity: "saved"})
		return e
	}))
	require.Len(t, page.Items, 1)
	require.True(t, page.Items[0].Saved)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(client, tx, 1, 3, shared.ID, public.ID, false) }))
	require.NoError(t, db.Table("c_collab_channel_members").Where("principal_id=4").Update("left_at", m.now()).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.listMessagesOn(client, tx, 1, 3, shared.ID, MessageFilter{Limit: 10, Activity: "all"})
		return e
	}), ErrNotFound)
}
func TestReadBoundariesAndCursorValidation(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	msg := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Need @attention", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
	for _, f := range []MessageFilter{{Limit: 0, Activity: "all"}, {Limit: 101, Activity: "all"}, {Limit: 1, Activity: "bad"}, {Limit: 1, Activity: "all", AfterSeq: -1}, {Limit: 1, Activity: "all", Cursor: "broken"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, f)
			return e
		}), ErrInvalidArgument)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 10, Activity: "mentions"})
		require.Len(t, page.Items, 1)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 10, Activity: "unread", AfterSeq: msg.Seq})
		require.Empty(t, page.Items)
		return e
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 10, Activity: "all"})
		return e
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.listChannelsOn(conversationContext(t, 2), tx, 1, 3, "", 10)
		return e
	}), ErrForbidden)
	scope := cursorScope("a")
	c := nextCursor(42, scope)
	pos, e := readCursor(c, scope)
	require.NoError(t, e)
	require.EqualValues(t, 42, pos)
	_, e = readCursor(c, cursorScope("b"))
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = readCursor(nextCursor(0, scope), scope)
	require.ErrorIs(t, e, ErrInvalidArgument)
}

func TestMessageSearchUsesExistingPortableSearchNormalization(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Release notes", Visibility: "CHANNEL"})
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Other message", Visibility: "CHANNEL"})
	for _, term := range []string{"Release", "%Release%", "_Release_", `\Release\`} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			page, err := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 10, Activity: "all", Search: term})
			require.Len(t, page.Items, 1)
			require.Equal(t, "Release notes", page.Items[0].Body)
			return err
		}))
	}
}

func TestMessagePageRelatedReadsStayConstantAndPreservePerMessageFacts(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	var savedID guid.ID
	for i := 0; i < 11; i++ {
		v := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Page message", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
		if i == 5 {
			savedID = v.ID
		}
	}
	var card MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = m.postSystemCardOn(conversationContext(t, 2), tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 91, Kind: "DECISION_CARD", Body: "Review required", FoldKey: "work:91", Attention: true, Payload: map[string]any{"status": "NEEDS_HUMAN_REVIEW"}})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.saveMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, savedID, true)
	}))
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:page-query-count", func(*gorm.DB) { queries++ }))
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:page-query-count") })
	for _, limit := range []int{1, 12} {
		queries = 0
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			page, err := m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: limit, Activity: "all"})
			require.Len(t, page.Items, limit)
			cardSeen := false
			for _, message := range page.Items {
				if message.ID == card.ID {
					cardSeen = true
					require.Equal(t, "DECISION_CARD", message.Kind)
					require.Equal(t, "work:91", message.FoldKey)
					require.True(t, message.Attention)
					require.Equal(t, map[string]any{"status": "NEEDS_HUMAN_REVIEW"}, message.Payload)
					require.Empty(t, message.Mentions)
				} else {
					require.Equal(t, "TEXT", message.Kind)
					require.False(t, message.Attention)
					require.Empty(t, message.Payload)
					require.Equal(t, []guid.ID{2}, message.Mentions)
				}
				require.Equal(t, message.ID == savedID, message.Saved)
			}
			require.True(t, cardSeen, "both page sizes must hydrate the latest system card")
			return err
		}))
		// Two authorization reads + messages + four bounded related reads
		// (mentions, attachments, saves, and system cards).
		require.Equal(t, 7, queries, "page size %d must not introduce per-message queries", limit)
	}
}

func TestMessageReadExposesCurrentVisibleStreamPositionAcrossPages(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Root", Visibility: "CHANNEL"})
	ctx := conversationContext(t, 2)
	read := func(thread string) *MessageListOutput {
		t.Helper()
		v, e := m.listMessagesHTTP(ctx, &messageListInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, ThreadID: thread, Limit: 50, Activity: "all"})
		require.NoError(t, e)
		return v
	}
	empty := read(root.ID.String())
	require.Empty(t, empty.Body.Data.Items)
	require.Equal(t, root.Seq, empty.Body.Meta.MaxSeq)
	reply := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Reply", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Unrelated", Visibility: "CHANNEL"})
	thread := read(root.ID.String())
	require.Len(t, thread.Body.Data.Items, 1)
	require.Equal(t, reply.Seq, thread.Body.Meta.MaxSeq)
	require.NotEmpty(t, thread.Body.Meta.Next)
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Hidden", Visibility: "INTERNAL"})
	ctx = conversationContext(t, 4)
	visible := read("")
	require.EqualValues(t, 3, visible.Body.Meta.MaxSeq)
	missing := guid.ID(1)
	_, e := m.listMessagesHTTP(ctx, &messageListInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, ThreadID: missing.String(), Limit: 50, Activity: "all"})
	require.Error(t, e)
	var blank ChannelView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		blank, e = m.createChannelOn(conversationContext(t, 2), tx, 1, 3, CreateChannelInput{Name: "Empty shared channel", Visibility: "CLIENT_SHARED"})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, blank.ID, 4, MemberInput{External: true})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listMessagesOn(ctx, tx, 1, 3, blank.ID, MessageFilter{Limit: 50, Activity: "all"})
		require.Zero(t, page.MaxSeq)
		return e
	}))
}
