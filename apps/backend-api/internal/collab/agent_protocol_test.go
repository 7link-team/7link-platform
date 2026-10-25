package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestInterruptedRetrySameKeyExcludesOwnAndOtherThreadWrites(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Root", Visibility: "CHANNEL"})
	other := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Other thread", Visibility: "CHANNEL"})
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Unrelated reply", Visibility: "CHANNEL", ReplyToMessageID: &other.ID})
	ctx := conversationContext(t, 2)
	var zero int64
	input := messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, IdempotencyKey: "same-logical-action", Body: PostMessageInput{Body: "Answer", Visibility: "CHANNEL", SeenSeq: &zero, ReplyToMessageID: &root.ID}}
	// Simulate another author on the root so it interrupts our actor.
	q := query.BindMessages(db, m.names)
	_, e := q.WithContext(ctx).Where(q.ID.Eq(root.ID)).Update(q.AuthorID, guid.ID(4))
	require.NoError(t, e)
	result, e := m.postMessageHTTP(ctx, &input)
	require.Nil(t, result)
	require.Error(t, e)
	input.Body.SeenSeq = &root.Seq
	result, e = m.postMessageHTTP(ctx, &input)
	require.NoError(t, e)
	require.EqualValues(t, 4, result.Body.Data.Seq)
	again, e := m.postMessageHTTP(ctx, &input)
	require.NoError(t, e)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, result.Body.Data.ID, again.Body.Data.ID)
	// Own writes and unrelated thread replies are not stale-context updates.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.checkSeenOn(ctx, tx, 1, ch.ID, 2, false, 4, input.Body) }))
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.EqualValues(t, 4, count)
}

func TestInterruptedThreadRootEditAdvancesFenceWithoutReorderingMessages(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, query.BindMembers(db, m.names).WithContext(t.Context()).Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 4, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Original root", Visibility: "CHANNEL"})
	other := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Other thread", Visibility: "CHANNEL"})
	seen := other.Seq
	ctx := conversationContext(t, 4)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, seen, PostMessageInput{SeenSeq: &seen, ReplyToMessageID: &root.ID})
	}))
	var edited MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		edited, err = m.mutateMessageOn(conversationContext(t, 2), tx, 1, root.ID, root.Version, EditMessageInput{Body: "Changed requirement"}, false)
		return err
	}))
	require.Equal(t, root.Seq, edited.Seq)
	require.Greater(t, edited.StreamSeq, seen)
	var interrupted *Interrupted
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		err := m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, edited.StreamSeq, PostMessageInput{SeenSeq: &seen, ReplyToMessageID: &root.ID})
		require.ErrorAs(t, err, &interrupted)
		require.Equal(t, edited.StreamSeq, interrupted.CurrentSeq)
		require.Len(t, interrupted.NewerEvents, 1)
		require.Equal(t, "Changed requirement", interrupted.NewerEvents[0].Body)
		return nil
	}))
	seen = interrupted.CurrentSeq
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, seen, PostMessageInput{SeenSeq: &seen, ReplyToMessageID: &root.ID})
	}))
	// Root changes in another thread cannot fence this thread or a top-level write.
	old := other.Seq
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, seen, PostMessageInput{SeenSeq: &old, ReplyToMessageID: &other.ID})
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, seen, PostMessageInput{SeenSeq: &old})
	}))
	// The real HTTP command must leave a stale attempt unrecorded so the same
	// logical key can be retried after reading the edited root.
	input := messagePostInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, IdempotencyKey: "edited-root-retry", Body: PostMessageInput{Body: "Read the changed requirement", Visibility: "CHANNEL", SeenSeq: &old, ReplyToMessageID: &root.ID}}
	result, err := m.postMessageHTTP(ctx, &input)
	require.Nil(t, result)
	require.Error(t, err)
	input.Body.SeenSeq = &seen
	result, err = m.postMessageHTTP(ctx, &input)
	require.NoError(t, err)
	again, err := m.postMessageHTTP(ctx, &input)
	require.NoError(t, err)
	require.Equal(t, "true", again.Replayed)
	require.Equal(t, result.Body.Data.ID, again.Body.Data.ID)
}

func TestInterruptedChannelIgnoresWithdrawnTopLevelMessages(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Superseded context", Visibility: "CHANNEL"})
	ctx := conversationContext(t, 4)
	zero := int64(0)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var interrupted *Interrupted
		require.ErrorAs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, message.Seq, PostMessageInput{SeenSeq: &zero}), &interrupted)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.mutateMessageOn(conversationContext(t, 2), tx, 1, message.ID, 1, EditMessageInput{}, true)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, message.Seq, PostMessageInput{SeenSeq: &zero})
	}))
	// A withdrawn root is still relevant to a reply on that exact thread.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var interrupted *Interrupted
		require.ErrorAs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, message.Seq, PostMessageInput{SeenSeq: &zero, ReplyToMessageID: &message.ID}), &interrupted)
		return nil
	}))
}

func TestInterruptedChannelFiltersSystemEventsBeforePagingAndHydratesDecisionCards(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Already read", Visibility: "CHANNEL"})
	seen := root.Seq
	for i := 0; i < 55; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.postSystemCardOn(conversationContext(t, 2), tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: guid.ID(700 + i), Kind: "EVENT", Body: "Status event", Payload: map[string]any{"state": "RUNNING"}})
			return err
		}))
	}
	ctx := conversationContext(t, 4)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, 56, PostMessageInput{SeenSeq: &seen})
	}))
	var card MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = m.postSystemCardOn(conversationContext(t, 2), tx, SystemCardInput{TenantID: 1, ProjectID: 3, ChannelID: ch.ID, SourceID: 900, Kind: "DECISION_CARD", Body: "Decision changes context", Payload: map[string]any{"decision_id": "901"}})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var interrupted *Interrupted
		require.ErrorAs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, false, card.Seq, PostMessageInput{SeenSeq: &seen}), &interrupted)
		require.Len(t, interrupted.NewerEvents, 1)
		require.Equal(t, "DECISION_CARD", interrupted.NewerEvents[0].Kind)
		require.Equal(t, "901", interrupted.NewerEvents[0].Payload["decision_id"])
		require.False(t, interrupted.Truncated)
		return nil
	}))
}

func TestMessageObserverPrivacyAndRollback(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	// Channel membership kinds are the source projection list.
	for _, id := range []guid.ID{7, 9} {
		require.NoError(t, query.BindMembers(db, m.names).WithContext(t.Context()).Create(&model.ChannelMember{ChannelID: ch.ID, PrincipalID: id, TenantID: 1, MemberKind: "AGENT", External: id == 9, Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
	}
	var recipients []guid.ID
	observer := func(_ context.Context, _ *gorm.DB, r MessageReference) error {
		recipients = append(recipients, r.AgentPrincipalID)
		return nil
	}
	require.NoError(t, m.bindMessageObserver(observer))
	require.ErrorIs(t, m.bindMessageObserver(observer), ErrInvalidArgument)
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Private", Visibility: "INTERNAL"})
	require.Equal(t, []guid.ID{7}, recipients)
	m.messageObserver = func(context.Context, *gorm.DB, MessageReference) error { return errors.New("projection failed") }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Must rollback", Visibility: "CHANNEL"})
		return e
	}))
	var count int64
	require.NoError(t, db.Table("c_collab_messages").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestThreadAnchorRechecksBothMembershipsAndVisibility(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Anchor", Visibility: "CHANNEL"})
	internal := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Private anchor", Visibility: "INTERNAL"})
	for _, id := range []guid.ID{7, 9} {
		require.NoError(t, query.BindMembers(db, m.names).WithContext(t.Context()).Create(&model.ChannelMember{ChannelID: ch.ID, PrincipalID: id, TenantID: 1, MemberKind: "AGENT", External: id == 9, Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}))
	}
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		seq, e := m.threadAccessOn(ctx, tx, 1, 3, root.ID, 7)
		require.NoError(t, e)
		require.Equal(t, root.Seq, seq)
		_, e = m.threadAccessOn(ctx, tx, 1, 3, root.ID, 9)
		require.NoError(t, e)
		_, e = m.threadAccessOn(ctx, tx, 1, 3, internal.ID, 9)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.threadAccessOn(ctx, tx, 1, 3, root.ID, 99)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.threadAccessOn(ctx, tx, 1, 3, 1, 7)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.threadAccessOn(ctx, tx, 1, -1, root.ID, 7)
		require.ErrorIs(t, e, ErrInvalidArgument)
		// Zero selects private team scope; a project conversation cannot be read through it.
		_, e = m.threadAccessOn(ctx, tx, 1, 0, root.ID, 7)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.threadAccessOn(ctx, tx, 2, 3, root.ID, 7)
		require.ErrorIs(t, e, ErrNotFound)
		_, e = m.threadAccessOn(conversationContext(t, 99), tx, 1, 3, root.ID, 7)
		require.Error(t, e)
		return nil
	}))
	// A revoked target is checked again rather than trusting an existing anchor.
	require.NoError(t, db.Table("c_collab_channel_members").Where("principal_id = ?", 7).Update("left_at", m.now()).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.threadAccessOn(ctx, tx, 1, 3, root.ID, 7)
		require.ErrorIs(t, e, ErrNotFound)
		return nil
	}))
}

func TestInterruptedPayloadIsVisibleBoundedAndCarriesRetryMetadata(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	for range 52 {
		postConversation(t, m, db, ch.ID, PostMessageInput{Body: "New context", Visibility: "CHANNEL"})
	}
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "Hidden context", Visibility: "INTERNAL"})
	ctx := conversationContext(t, 4)
	var zero int64
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := m.checkSeenOn(ctx, tx, 1, ch.ID, 4, true, 53, PostMessageInput{SeenSeq: &zero})
		var interrupted *Interrupted
		require.ErrorAs(t, e, &interrupted)
		require.True(t, interrupted.Truncated)
		require.Len(t, interrupted.NewerEvents, 50)
		require.EqualValues(t, 52, interrupted.CurrentSeq)
		raw, e := json.Marshal(conversationHTTPError(ctx, interrupted))
		require.NoError(t, e)
		var response map[string]any
		require.NoError(t, json.Unmarshal(raw, &response))
		require.Contains(t, response["meta"], "next")
		require.Contains(t, string(raw), "RETRY_WITH_SAME_IDEMPOTENCY_KEY")
		require.Contains(t, string(raw), `"state":"interrupted"`)
		require.NotContains(t, string(raw), "Hidden context")
		require.ErrorIs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, true, 53, PostMessageInput{}), ErrSeenSeqRequired)
		negative := int64(-1)
		require.ErrorIs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, true, 53, PostMessageInput{SeenSeq: &negative}), ErrInvalidArgument)
		future := int64(54)
		require.ErrorIs(t, m.checkSeenOn(ctx, tx, 1, ch.ID, 4, true, 53, PostMessageInput{SeenSeq: &future}), ErrInvalidArgument)
		return nil
	}))
}
