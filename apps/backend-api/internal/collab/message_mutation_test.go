package collab

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMessageMutationRejectsInvalidBodiesVersionsAndFutureTimestamps(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "original", Visibility: "CHANNEL"})
	for _, tc := range []struct {
		version int
		body    string
	}{{0, "new"}, {1, ""}, {1, strings.Repeat("x", 8001)}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, tc.version, EditMessageInput{Body: tc.body}, false)
			return err
		}), ErrInvalidArgument)
	}
	m.now = func() time.Time { return posted.CreatedAt.Add(-time.Second) }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, 1, EditMessageInput{Body: "new"}, false)
		return err
	}), ErrMessageNotEditable)
	var stored model.Message
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", posted.ID).Take(&stored).Error)
	require.Equal(t, "original", stored.Body)
	require.Equal(t, 1, stored.Version)
}

func TestMessageEditLimitRevisionsAndRetractionRedaction(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "original secret", Visibility: "CHANNEL"})
	start := posted.CreatedAt
	m.now = func() time.Time { return start.Add(14 * time.Minute) }
	mutate := func(version int, body string, retract bool) (MessageView, error) {
		var out MessageView
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, version, EditMessageInput{Body: body}, retract)
			return e
		})
		return out, err
	}
	for version := 1; version <= 10; version++ {
		out, err := mutate(version, "edited", false)
		require.NoError(t, err)
		require.Equal(t, version+1, out.Version)
	}
	_, err := mutate(11, "eleventh", false)
	require.ErrorIs(t, err, ErrMessageNotEditable)
	_, err = mutate(1, "stale", false)
	require.ErrorIs(t, err, ErrMessageVersion)
	out, err := mutate(11, "", true)
	require.NoError(t, err)
	require.True(t, out.Retracted)
	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"body":null`)
	require.NotContains(t, string(encoded), "original secret")
	_, err = mutate(12, "", true)
	require.ErrorIs(t, err, ErrMessageNotEditable)
	var revisions []model.MessageRevision
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		revisions, e = m.messageRevisionsOn(conversationContext(t, 2), tx, 1, posted.ID)
		return e
	}))
	require.Len(t, revisions, 11)
	require.Equal(t, "original secret", revisions[0].Body)
	require.Equal(t, "edited", revisions[10].Body)
	var row model.Message
	require.NoError(t, db.Table("c_collab_messages").Where("id=?", posted.ID).First(&row).Error)
	require.Empty(t, row.Body)
	var page CursorPage[MessageView]
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listMessagesOn(conversationContext(t, 2), tx, 1, 3, ch.ID, MessageFilter{Limit: 20, Activity: "all"})
		return e
	}))
	require.Len(t, page.Items, 1)
	require.True(t, page.Items[0].Retracted)
	require.Empty(t, page.Items[0].Body)
}

func TestMessageEditStorageFailuresLeaveContentMentionsAndRevisionsAtomic(t *testing.T) {
	for _, scenario := range []struct{ operation, table string }{
		{"query", "c_collab_messages"}, {"query", "c_collab_channels"}, {"query", "c_collab_system_cards"},
		{"query", "c_collab_message_mentions"}, {"create", "c_collab_message_revisions"},
		{"delete", "c_collab_message_mentions"}, {"create", "c_collab_message_mentions"},
		{"update", "c_collab_messages"}, {"audit", "audit_probe"},
	} {
		t.Run(scenario.operation+scenario.table, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "INTERNAL")
			root := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "root", Visibility: "CHANNEL"})
			posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "before", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
			fault := errors.New("edit storage failure")
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == scenario.table {
					tx.AddError(fault)
				}
			}
			remove := func() {}
			switch scenario.operation {
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:edit", inject))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:edit")) }
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:edit", inject))
				remove = func() { require.NoError(t, db.Callback().Create().Remove("test:edit")) }
			case "delete":
				require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("test:edit", inject))
				remove = func() { require.NoError(t, db.Callback().Delete().Remove("test:edit")) }
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:edit", inject))
				remove = func() { require.NoError(t, db.Callback().Update().Remove("test:edit")) }
			}
			mentions := []guid.ID{2}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, 1, EditMessageInput{Body: "after", MentionPrincipalIDs: &mentions}, false)
				return err
			})
			remove()
			require.ErrorIs(t, err, fault)
			var stored model.Message
			require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", posted.ID).Take(&stored).Error)
			require.Equal(t, "before", stored.Body)
			require.Equal(t, 1, stored.Version)
			var n int64
			require.NoError(t, db.Table("c_collab_message_revisions").Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, db.Table("c_collab_message_mentions").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}

func TestMessageEditNotifiesOnlyNewAgentMentionsAndRollsBackOnObserverFailure(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	require.NoError(t, db.Table(m.names.Members.String()).Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 7, MemberKind: "AGENT", Source: "EXPLICIT", Role: "MEMBER", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}).Error)
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "before", Visibility: "CHANNEL"})
	fault := errors.New("inbox unavailable")
	var observed []MessageReference
	m.messageObserver = func(_ context.Context, _ *gorm.DB, ref MessageReference) error {
		observed = append(observed, ref)
		return fault
	}
	mentions := []guid.ID{2, 7}
	edit := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, 1, EditMessageInput{Body: "after", MentionPrincipalIDs: &mentions}, false)
			return err
		})
	}
	require.ErrorIs(t, edit(), fault)
	require.Len(t, observed, 1)
	require.Equal(t, guid.ID(7), observed[0].AgentPrincipalID)
	require.Equal(t, posted.ID, observed[0].MessageID)
	require.Equal(t, "MENTION", observed[0].Kind)
	var stored model.Message
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", posted.ID).Take(&stored).Error)
	require.Equal(t, "before", stored.Body)
	m.messageObserver = func(_ context.Context, _ *gorm.DB, ref MessageReference) error {
		observed = append(observed, ref)
		return nil
	}
	require.NoError(t, edit())
	require.Len(t, observed, 2)
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", posted.ID).Take(&stored).Error)
	require.Equal(t, 2, stored.Version)
}

func TestMessageMutationTimeAuthorityAndAuditRead(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "private", Visibility: "CHANNEL"})
	require.NoError(t, db.Table("c_collab_channel_members").Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 4, MemberKind: "HUMAN", Role: "MEMBER", Source: "EXPLICIT", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}).Error)
	policy := m.policy
	m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "channel_manage" || permission == "audit_read" {
			return authz.DataConstraint{}, nil
		}
		return policy(ctx, tx, tenant, project, permission, actor)
	}
	mutate := func(actor guid.ID, retract bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.mutateMessageOn(conversationContext(t, actor), tx, 1, posted.ID, 1, EditMessageInput{Body: "new"}, retract)
			return e
		})
	}
	require.ErrorIs(t, mutate(4, false), ErrForbidden)
	require.ErrorIs(t, mutate(4, true), ErrForbidden)
	m.now = func() time.Time { return posted.CreatedAt.Add(15*time.Minute + time.Millisecond) }
	require.ErrorIs(t, mutate(2, false), ErrMessageNotEditable)
	m.now = func() time.Time { return posted.CreatedAt.Add(24*time.Hour + time.Millisecond) }
	require.ErrorIs(t, mutate(2, true), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.messageRevisionsOn(conversationContext(t, 2), tx, 1, posted.ID)
		return e
	}), ErrForbidden)
	m.policy = policy
	require.NoError(t, mutate(4, true), "current channel manager can retract another author's old text")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.messageRevisionsOn(conversationContext(t, 5), tx, 1, posted.ID)
		return e
	}), "audit permission never bypasses explicit membership")
}

func TestMessageEditOutboxFailureRollsBackContentRevisionAndMentions(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "before", Visibility: "CHANNEL", MentionPrincipalIDs: []guid.ID{2}})
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:edit-outbox", func(tx *gorm.DB) {
		if tx.Statement.Table == "c_domain_events" {
			tx.AddError(errors.New("outbox unavailable"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:edit-outbox") })
	mentions := []guid.ID{}
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, 1, EditMessageInput{Body: "after", MentionPrincipalIDs: &mentions}, false)
		return e
	}), "outbox unavailable")
	var row model.Message
	require.NoError(t, db.Table("c_collab_messages").Where("id=?", posted.ID).First(&row).Error)
	require.Equal(t, "before", row.Body)
	require.Equal(t, 1, row.Version)
	var n int64
	require.NoError(t, db.Table("c_collab_message_revisions").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, db.Table("c_collab_message_mentions").Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.NoError(t, db.Table("audit_probe").Where("event_type=?", "collab.message_edited").Count(&n).Error)
	require.Zero(t, n)
}

func TestMessageEditMentionsPublishOnlyNewRecipientsAndPreserveSeenSeq(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	posted := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "before", Visibility: "CHANNEL"})
	mentions := []guid.ID{2, 2}
	var out MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.mutateMessageOn(conversationContext(t, 2), tx, 1, posted.ID, 1, EditMessageInput{Body: "after", MentionPrincipalIDs: &mentions}, false)
		return e
	}))
	require.Equal(t, posted.Seq, out.Seq)
	require.Equal(t, []guid.ID{2}, out.Mentions)
	var data string
	require.NoError(t, db.Table("c_domain_events").Select("data").Where("event_type=?", "collab.message_edited").Take(&data).Error)
	require.Contains(t, data, `"added_mention_ids":["2"]`)
	require.NotContains(t, data, "after")
}

func TestMessageManagerCanRetractWithoutPostPermission(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "INTERNAL")
	message := postConversation(t, m, db, ch.ID, PostMessageInput{Body: "text", Visibility: "CHANNEL"})
	require.NoError(t, db.Table("c_collab_channel_members").Create(&model.ChannelMember{TenantID: 1, ChannelID: ch.ID, PrincipalID: 4, MemberKind: "HUMAN", Role: "MEMBER", Source: "EXPLICIT", JoinedAt: m.now(), CreatedAt: m.now(), UpdatedAt: m.now()}).Error)
	m.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "channel_read" || permission == "channel_manage"}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.mutateMessageOn(conversationContext(t, 4), tx, 1, message.ID, 1, EditMessageInput{}, true)
		return err
	}))
}
