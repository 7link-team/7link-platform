package collab

import (
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMessageConversionAtomicReplayAndVisibility(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	internal := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "internal", Visibility: "INTERNAL"})
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "source", Visibility: "CHANNEL"})
	require.NoError(t, db.Exec("CREATE TABLE work_probe(id BIGINT PRIMARY KEY,body TEXT)").Error)
	created := 0
	require.NoError(t, m.bindWorkCreator(func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, in ConvertWorkInput, body string) (guid.ID, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		created++
		return 55, tx.Exec("INSERT INTO work_probe VALUES(55,?)", body).Error
	}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }))
	require.Error(t, m.bindWorkCreator(nil, nil))
	input := &convertWorkHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), IdempotencyKey: "convert-message-1", Body: ConvertWorkInput{Title: "Work", Objective: "Deliver", AccountablePrincipalID: 2, Criteria: []WorkCriterion{{Text: "done", VerificationMethod: "test"}}}}
	first, e := m.convertWorkHTTP(conversationContext(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, guid.ID(55), first.Body.Data.WorkItemID)
	replay, e := m.convertWorkHTTP(conversationContext(t, 2), input)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, 1, created)
	var count int64
	require.NoError(t, db.Table("c_collab_message_work_items").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	input.MessageID = internal.ID.String()
	input.IdempotencyKey = "hidden-message"
	_, e = m.convertWorkHTTP(conversationContext(t, 4), input)
	require.Error(t, e)
	require.Equal(t, 1, created)
	require.NoError(t, db.Exec("DELETE FROM work_probe").Error)
	require.NoError(t, db.Exec("CREATE TRIGGER fail_source_link BEFORE INSERT ON c_collab_message_work_items BEGIN SELECT RAISE(ABORT,'failed link'); END").Error)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.convertWorkOn(conversationContext(t, 2), tx, 1, 3, ch.ID, internal.ID, input.Body)
		return e
	})
	require.Error(t, err)
	require.NoError(t, db.Table("work_probe").Count(&count).Error)
	require.Zero(t, count)
	m.workCreator = func(context.Context, *gorm.DB, guid.ID, guid.ID, ConvertWorkInput, string) (guid.ID, error) {
		return 0, errors.New("private error")
	}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.convertWorkOn(conversationContext(t, 2), tx, 1, 3, ch.ID, internal.ID, input.Body)
		return e
	}))
}

func TestConversionRejectsInvalidRoutesAndRevokedReplayAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "source", Visibility: "CHANNEL"})
	ctx := conversationContext(t, 2)
	for _, in := range []*convertWorkHTTPInput{{ConversationChannelInput: ConversationChannelInput{ChannelID: "bad"}, MessageID: "1"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: "bad"}, {ConversationChannelInput: ConversationChannelInput{ChannelID: "999"}, MessageID: "1"}} {
		_, e := m.convertWorkHTTP(ctx, in)
		require.Error(t, e)
	}
	input := ConvertWorkInput{Title: "work", Objective: "objective", AccountablePrincipalID: 2, Criteria: []WorkCriterion{{Text: "done", VerificationMethod: "test"}}}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.convertWorkOn(ctx, tx, 1, 3, ch.ID, message.ID, input); return e }), ErrUnavailable)
	require.NoError(t, m.bindWorkCreator(func(context.Context, *gorm.DB, guid.ID, guid.ID, ConvertWorkInput, string) (guid.ID, error) {
		return 56, nil
	}, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }))
	in := &convertWorkHTTPInput{ConversationChannelInput: ConversationChannelInput{ChannelID: ch.ID.String()}, MessageID: message.ID.String(), IdempotencyKey: "revoked-replay", Body: input}
	_, e := m.convertWorkHTTP(ctx, in)
	require.NoError(t, e)
	_, e = m.convertWorkHTTP(ctx, in)
	require.Error(t, e)
	m.workReplay = nil
	_, e = m.convertWorkHTTP(ctx, in)
	require.Error(t, e)
	input.AccountablePrincipalID = 0
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.convertWorkOn(ctx, tx, 1, 3, ch.ID, message.ID, input); return e }), ErrInvalidArgument)
	input.AccountablePrincipalID = 8
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.convertWorkOn(ctx, tx, 1, 3, ch.ID, message.ID, input); return e }), ErrForbidden)
	m.principals.LockActiveRow = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.convertWorkOn(ctx, tx, 1, 3, ch.ID, message.ID, input); return e }), ErrUnavailable)
	require.Error(t, conversationHTTPError(ctx, ErrWorkReadOnly))
}
