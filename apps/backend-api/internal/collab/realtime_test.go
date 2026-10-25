package collab

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRealtimeChannelAndThreadReuseCurrentVisibility(t *testing.T) {
	m, db := conversationHarness(t)
	shared := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, shared.ID, 4, MemberInput{External: true})
		return err
	}))
	public := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "public", Visibility: "CHANNEL"})
	hidden := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "private", Visibility: "INTERNAL"})
	reply := postConversation(t, m, db, shared.ID, PostMessageInput{Body: "reply", Visibility: "CHANNEL", ReplyToMessageID: &public.ID})
	for _, tc := range []struct {
		actor  guid.ID
		id     guid.ID
		thread bool
		want   bool
	}{
		{2, shared.ID, false, true}, {4, shared.ID, false, true},
		{2, hidden.ID, true, true}, {4, hidden.ID, true, false},
		{4, public.ID, true, true}, {4, reply.ID, true, false},
		{8, shared.ID, false, false}, {9, shared.ID, false, false},
		{4, 9999, true, false}, {4, 9999, false, false},
	} {
		allowed, err := m.authorizeRealtime(conversationContext(t, tc.actor), 1, tc.actor, tc.id, false, tc.thread)
		require.NoError(t, err)
		require.Equal(t, tc.want, allowed, "%+v", tc)
	}
	allowed, err := m.authorizeRealtimeChannel(conversationContext(t, 2), 1, 4, shared.ID, false)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = m.authorizeRealtimeChannel(conversationContext(t, 4), 1, 4, shared.ID, true)
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=4", shared.ID).Update("left_at", m.now()).Error)
	allowed, err = m.authorizeRealtimeThread(conversationContext(t, 4), 1, 4, public.ID, false)
	require.NoError(t, err)
	require.False(t, allowed)
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("policy unavailable")
	}
	_, err = m.authorizeRealtimeChannel(conversationContext(t, 2), 1, 2, shared.ID, false)
	require.ErrorContains(t, err, "policy unavailable")
}

func TestRealtimeSignalsAreContentFreeAtomicAndPersonal(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "confidential body", Visibility: "CHANNEL"})
	postConversation(t, m, db, ch.ID, PostMessageInput{Body: "confidential reply", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
	var rows []struct{ EventType, Topics, Data, RealtimeMode, ActorKind string }
	require.NoError(t, db.Table("c_domain_events").Where("event_type=?", "collab.message_posted").Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "SIGNAL", row.RealtimeMode)
		require.Contains(t, row.Topics, "tenant/1/channel/"+ch.ID.String())
		require.NotContains(t, row.Data, "confidential")
	}
	require.Contains(t, rows[1].Topics, "tenant/1/thread/"+root.ID.String())
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Table("c_collab_channel_members").Where("channel_id=? AND principal_id=2", ch.ID).Update("member_kind", "AGENT").Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(ctx, tx, 1, 3, ch.ID, root.ID, true) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.markReadOn(ctx, tx, 1, 3, ch.ID, root.Seq); return err }))
	rows = nil
	require.NoError(t, db.Table("c_domain_events").Where("event_type IN ?", []string{"collab.saved_updated", "collab.read_updated"}).Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "SIGNAL", row.RealtimeMode)
		require.JSONEq(t, `["tenant/1/inbox/2"]`, row.Topics)
		require.Equal(t, "AGENT", row.ActorKind)
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("realtime:fail", func(tx *gorm.DB) {
		if tx.Statement.Table == "c_domain_events" {
			tx.AddError(errors.New("outbox unavailable"))
		}
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.saveMessageOn(ctx, tx, 1, 3, ch.ID, root.ID, false) }))
	var count int64
	require.NoError(t, db.Table("c_collab_saved_messages").Count(&count).Error)
	require.EqualValues(t, 1, count, "failed signal must roll back the save mutation")
}

func TestRealtimeUnavailableAndWithdrawnResourcesFailClosed(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "root", Visibility: "CHANNEL"})
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Table("c_collab_messages").Where("id=?", root.ID).Update("withdrawn_at", m.now()).Error)
	allowed, err := m.authorizeRealtimeThread(ctx, 1, 2, root.ID, false)
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, db.Table("c_collab_channels").Where("id=?", ch.ID).Updates(map[string]any{"project_id": nil, "kind": "DIRECT"}).Error)
	allowed, err = m.authorizeRealtimeChannel(ctx, 1, 2, ch.ID, false)
	require.NoError(t, err)
	require.False(t, allowed, "project-less DIRECT channels require their own read contract")
	m.publisher = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.personalConversationEvent(ctx, tx, 1, 3, ch.ID, "collab.saved_updated", "HUMAN")
	}), ErrUnavailable)
	m.runtime = nil
	_, err = m.authorizeRealtimeChannel(ctx, 1, 2, ch.ID, false)
	require.ErrorIs(t, err, ErrUnavailable)
}
