package collab

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMentionNotificationOwnerRequiresCurrentExplicitMembership(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "private mention", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.notificationMentionsOn(t.Context(), tx, 1, message.ID)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{2}, ids)
		yes, err := m.notificationMentionVisibleOn(t.Context(), tx, 1, message.ID, 4)
		require.NoError(t, err)
		require.False(t, yes)
		yes, err = m.notificationMentionVisibleOn(t.Context(), tx, 2, message.ID, 2)
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
	q := query.BindMembers(db, m.names)
	now := m.now()
	_, err := q.WithContext(t.Context()).Where(q.TenantID.Eq(guid.ID(1)), q.ChannelID.Eq(ch.ID), q.PrincipalID.Eq(guid.ID(2))).Update(q.LeftAt, &now)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		yes, err := m.notificationMentionVisibleOn(t.Context(), tx, 1, message.ID, 2)
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
}

func TestMentionNotificationsFailClosedOnInvalidScopeAndReadFailures(t *testing.T) {
	for _, stage := range []string{"mentions", "message", "channel", "policy"} {
		t.Run(stage, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "current mention", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
			failure := errors.New("notification source unavailable")
			fired := false
			if stage == "policy" {
				m.policy = core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
					fired = true
					return authz.DataConstraint{}, failure
				})
			} else {
				target := m.names.Messages.String()
				switch stage {
				case "mentions":
					target = m.names.Mentions.String()
				case "channel":
					target = m.names.Channels.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("mention_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == target {
						fired = true
						_ = tx.AddError(failure)
					}
				}))
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				ids, err := m.notificationMentionsOn(t.Context(), tx, 1, message.ID)
				require.Nil(t, ids)
				return err
			})
			require.True(t, fired)
			require.ErrorIs(t, err, failure)
		})
	}
	m, db := conversationHarness(t)
	for _, invalid := range []struct{ tenant, message, principal guid.ID }{{0, 1, 2}, {1, 0, 2}, {1, 1, 0}} {
		ok, err := m.notificationMentionVisibleOn(t.Context(), db, invalid.tenant, invalid.message, invalid.principal)
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.False(t, ok)
	}
	ids, err := m.notificationMentionsOn(t.Context(), nil, 1, 1)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Nil(t, ids)
	ok, err := m.notificationMentionVisibleOn(t.Context(), db, 1, 999, 2)
	require.NoError(t, err)
	require.False(t, ok)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return e
	}))
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "internal note", Visibility: "INTERNAL"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		visible, err := m.notificationMentionVisibleOn(t.Context(), tx, 1, message.ID, 4)
		require.NoError(t, err)
		require.False(t, visible)
		return nil
	}))
}
