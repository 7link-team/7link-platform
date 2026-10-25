package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeConversationKeepsOriginalRootAndCurrentClientVisibility(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return err
	}))
	var root MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		root, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "Original client request", Visibility: "CHANNEL"})
		return err
	}))
	s := module.NewServices()
	require.NoError(t, m.provideIntakeConversations(s))
	require.NoError(t, s.Seal())
	provider, err := module.Resolve[IntakeConversation](s)
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(root.Body))
	contentHash := hex.EncodeToString(digest[:])
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, provider.CheckOn(ctx, tx, 1, 3, ch.ID, root.ID, 4, contentHash))
		id, err := provider.ReplyOn(ctx, tx, 1, 3, ch.ID, root.ID, 4, contentHash, "Which output format do you need?")
		require.NoError(t, err)
		var row model.Message
		require.NoError(t, tx.Table(m.names.Messages.String()).Where("id=?", id).Take(&row).Error)
		require.Equal(t, root.ID, *row.ReplyToMessageID)
		require.Equal(t, ch.ID, row.ChannelID)
		require.Equal(t, "CHANNEL", row.Visibility)
		page, err := provider.ReadOn(ctx, tx, 1, 3, ch.ID, root.ID, 4, contentHash, "")
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		require.Equal(t, id, page.Items[0].ID)
		require.Equal(t, root.ID, *page.Items[0].ReplyToMessageID)
		_, err = provider.ReadOn(ctx, tx, 1, 3, ch.ID, root.ID, 4, contentHash, "foreign-cursor")
		require.Error(t, err)
		return nil
	}))
	for _, wrong := range []string{"tenant", "project", "channel", "author", "root", "withdrawn", "revoked"} {
		t.Run(wrong, func(t *testing.T) {
			abort := db.Transaction(func(tx *gorm.DB) error {
				tenant, project, channel, message, recipient := guid.ID(1), guid.ID(3), ch.ID, root.ID, guid.ID(4)
				switch wrong {
				case "tenant":
					tenant++
				case "project":
					project++
				case "channel":
					channel++
				case "author":
					recipient = 2
				case "root":
					message++
				case "withdrawn":
					require.NoError(t, tx.Table(m.names.Messages.String()).Where("id=?", root.ID).Update("withdrawn_at", m.now()).Error)
				case "revoked":
					require.NoError(t, tx.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=?", ch.ID, 4).Update("left_at", m.now()).Error)
				}
				_, err := provider.ReplyOn(ctx, tx, tenant, project, channel, message, recipient, contentHash, "Must not publish")
				require.Error(t, err)
				return ErrNotFound // undo injected source changes
			})
			require.ErrorIs(t, abort, ErrNotFound)
		})
	}
	require.ErrorIs(t, provider.CheckOn(ctx, db, 1, 3, ch.ID, root.ID, 4, contentHash), ErrInvalidArgument)
	var count int64
	require.NoError(t, db.Table(m.names.Messages.String()).Count(&count).Error)
	require.EqualValues(t, 2, count, "denied replies leave no message or sequence effect")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return provider.CheckOn(canceled, tx, 1, 3, ch.ID, root.ID, 4, contentHash) }), context.Canceled)
}

func TestMergedReplyForwardUsesActualSourceMessageAndTargetAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		return err
	}))
	post := func(actor guid.ID, body string, root *guid.ID) MessageView {
		var out MessageView
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.postMessageOn(conversationContext(t, actor), tx, 1, 3, ch.ID, PostMessageInput{Body: body, ReplyToMessageID: root, Visibility: "CHANNEL"})
			return err
		}))
		return out
	}
	source := post(4, "Actual source request", nil)
	target := post(4, "Actual target request", nil)
	reply := post(4, "Actual follow-up CSV detail", &source.ID)
	teamReply := post(2, "Internal team reply", &source.ID)
	hash := func(body string) string { sum := sha256.Sum256([]byte(body)); return hex.EncodeToString(sum[:]) }
	in := IntakeReplyForward{SourceChannelID: ch.ID, SourceRootID: source.ID, SourceRecipientID: 4, MessageID: reply.ID, SourceHash: hash(source.Body), TargetChannelID: ch.ID, TargetRootID: target.ID, TargetRecipientID: 4, TargetHash: hash(target.Body), SourceRef: 7}
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, err := m.forwardIntakeReplyOn(ctx, tx, 1, 3, in)
		require.NoError(t, err)
		var row model.Message
		require.NoError(t, tx.Table(m.names.Messages.String()).Where("id=?", id).First(&row).Error)
		require.Equal(t, guid.ID(2), row.AuthorID, "the forwarding team member never impersonates the client")
		require.Equal(t, target.ID, *row.ReplyToMessageID)
		require.Contains(t, row.Body, "From merged IN-7")
		require.Contains(t, row.Body, reply.ID.String())
		require.Contains(t, row.Body, reply.Body)
		return nil
	}))
	for _, scenario := range []string{"team_authored", "other_root", "withdrawn", "wrong_target_hash", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			before := int64(0)
			require.NoError(t, db.Table(m.names.Messages.String()).Count(&before).Error)
			err := db.Transaction(func(tx *gorm.DB) error {
				bad := in
				switch scenario {
				case "team_authored":
					bad.MessageID = teamReply.ID
				case "other_root":
					bad.MessageID = target.ID
				case "withdrawn":
					require.NoError(t, tx.Table(m.names.Messages.String()).Where("id=?", reply.ID).Update("withdrawn_at", m.now()).Error)
				case "wrong_target_hash":
					bad.TargetHash = hash("wrong source")
				case "revoked":
					require.NoError(t, tx.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=?", ch.ID, 4).Update("left_at", m.now()).Error)
				}
				_, err := m.forwardIntakeReplyOn(ctx, tx, 1, 3, bad)
				require.Error(t, err)
				return ErrNotFound
			})
			require.Error(t, err)
			after := int64(0)
			require.NoError(t, db.Table(m.names.Messages.String()).Count(&after).Error)
			require.Equal(t, before, after)
		})
	}
}

func TestIntakeMessageWorkBindingUsesCanonicalCurrentWorkAuthority(t *testing.T) {
	m, db := conversationHarness(t)
	require.NoError(t, db.Exec("CREATE UNIQUE INDEX regression_single_creation_source ON "+m.names.MessageWorks.String()+" (tenant_id,work_item_id)").Error)
	ch := createConversation(t, m, db, "INTERNAL")
	var message MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		message, err = m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Actual forwarded source", Visibility: "CHANNEL"})
		return err
	}))
	allowed := true
	m.workReplay = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, guid.ID(90), work)
		if !allowed {
			return ErrForbidden
		}
		return nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, message.ID, 90)
	}))
	var link model.MessageWork
	require.NoError(t, db.Table(m.names.MessageWorks.String()).Where("message_id=?", message.ID).First(&link).Error)
	require.Equal(t, guid.ID(90), link.WorkItemID)
	require.Equal(t, guid.ID(2), link.CreatedBy)
	var followup MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		followup, err = m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Actual second question for the same work", Visibility: "CHANNEL"})
		if err != nil {
			return err
		}
		return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, followup.ID, 90)
	}))
	var links []model.MessageWork
	require.NoError(t, db.Table(m.names.MessageWorks.String()).Find(&links).Error)
	require.Equal(t, []model.MessageWork{link}, links, "follow-up questions preserve the canonical creation source; their binding audit belongs to the caller's transaction")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, message.ID, 90)
	}))
	for _, corrupt := range []map[string]any{{"project_id": 99}, {"work_item_id": 91}} {
		require.NoError(t, db.Table(m.names.MessageWorks.String()).Where("message_id=?", message.ID).Updates(corrupt).Error)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, message.ID, 90)
		}), ErrNotFound, "an existing source cannot be reassigned across projects or work items")
		require.NoError(t, db.Table(m.names.MessageWorks.String()).Where("message_id=?", message.ID).Updates(map[string]any{"project_id": 3, "work_item_id": 90}).Error)
	}
	require.NoError(t, db.Table(m.names.MessageWorks.String()).Where("message_id=?", message.ID).Update("project_id", 99).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, followup.ID, 90)
	}), ErrNotFound, "a followup cannot reuse a creation source in another project")
	require.NoError(t, db.Table(m.names.MessageWorks.String()).Where("message_id=?", message.ID).Update("project_id", 3).Error)
	for _, failAt := range []int{1, 2} {
		reads := 0
		outage := errors.New("creation source read unavailable")
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_binding_outage", func(tx *gorm.DB) {
			if tx.Statement.Table == m.names.MessageWorks.String() {
				reads++
				if reads == failAt {
					tx.AddError(outage)
				}
			}
		}))
		err := db.Transaction(func(tx *gorm.DB) error {
			return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, followup.ID, 90)
		})
		require.NoError(t, db.Callback().Query().Remove("regression_binding_outage"))
		require.ErrorIs(t, err, outage)
		require.Equal(t, failAt, reads)
	}
	require.ErrorIs(t, m.bindIntakeMessageWorkOn(conversationContext(t, 2), db, 1, 3, message.ID, 90), ErrInvalidArgument, "message/work linkage requires the caller's real command transaction")
	allowed = false
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.bindIntakeMessageWorkOn(conversationContext(t, 2), tx, 1, 3, message.ID, 90)
	}), ErrForbidden)
}
