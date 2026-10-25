package collab

import (
	"7link.com/platform/internal/iam/identity"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntakeDispositionReceiptTracksActualClientReadCursor(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	var root, notice MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true}); err != nil {
			return err
		}
		var err error
		root, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "Original client source", Visibility: "CHANNEL"})
		if err != nil {
			return err
		}
		notice, err = m.postMessageOn(conversationContext(t, 2), tx, 1, 3, ch.ID, PostMessageInput{Body: "Your request was merged", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
		return err
	}))
	digest := sha256.Sum256([]byte(root.Body))
	hash := hex.EncodeToString(digest[:])
	check := func() (bool, error) {
		var read bool
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			read, err = m.intakeDispositionReceiptOn(conversationContext(t, 2), tx, 1, 3, ch.ID, root.ID, 4, notice.ID, hash)
			return err
		})
		return read, err
	}
	read, err := check()
	require.NoError(t, err)
	require.False(t, read)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.markReadOn(conversationContext(t, 4), tx, 1, 3, ch.ID, notice.Seq)
		return err
	}))
	read, err = check()
	require.NoError(t, err)
	require.True(t, read)
	require.NoError(t, db.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=4", ch.ID).Update("left_at", m.now()).Error)
	_, err = check()
	require.Error(t, err, "revoked contact must not retain a readable delivery receipt")
}

func TestIntakeDispositionNoticeRetainsAgentConsumedCursorAndAtomicReply(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	var root MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true}); err != nil {
			return err
		}
		var err error
		root, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "Client request", Visibility: "CHANNEL"})
		return err
	}))
	require.NoError(t, db.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=2", ch.ID).Update("member_kind", "AGENT").Error)
	digest := sha256.Sum256([]byte(root.Body))
	hash := hex.EncodeToString(digest[:])
	post := func(seen *int64) (guid.ID, error) {
		var id guid.ID
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			id, err = m.intakeDispositionNoticeOn(conversationContext(t, 2), tx, 1, 3, ch.ID, root.ID, 4, hash, "Actual Agent disposition", seen)
			return err
		})
		return id, err
	}
	_, err := post(nil)
	require.ErrorIs(t, err, ErrSeenSeqRequired)
	stale := int64(0)
	_, err = post(&stale)
	var interrupted *Interrupted
	require.ErrorAs(t, err, &interrupted)
	var count int64
	require.NoError(t, db.Table(m.names.Messages.String()).Where("reply_to_message_id=?", root.ID).Count(&count).Error)
	require.Zero(t, count)
	id, err := post(&root.Seq)
	require.NoError(t, err)
	var stored model.Message
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id=?", id).Take(&stored).Error)
	require.Equal(t, "Actual Agent disposition", stored.Body)
	require.Equal(t, "CHANNEL", stored.Visibility)
	require.Equal(t, root.ID, *stored.ReplyToMessageID)
}

func TestMergedClientReplyRoutesOnceToInternalTargetThreadWithExactBytes(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	var source, target, reply MessageView
	longBody := strings.Repeat("界", 8000)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true}); err != nil {
			return err
		}
		var err error
		source, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "Source request", Visibility: "CHANNEL"})
		if err != nil {
			return err
		}
		target, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "Target request", Visibility: "CHANNEL"})
		if err != nil {
			return err
		}
		reply, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: longBody, ReplyToMessageID: &source.ID, Visibility: "CHANNEL"})
		return err
	}))
	digest := sha256.Sum256([]byte(source.Body))
	targetDigest := sha256.Sum256([]byte(target.Body))
	in := IntakeReplyForward{SourceChannelID: ch.ID, SourceRootID: source.ID, SourceRecipientID: 4, MessageID: reply.ID, SourceHash: hex.EncodeToString(digest[:]), SourceRef: 9, TargetChannelID: ch.ID, TargetRootID: target.ID, TargetRecipientID: 4, TargetHash: hex.EncodeToString(targetDigest[:])}
	var forwarded guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		forwarded, err = m.routeIntakeReplyOn(t.Context(), tx, 1, 3, in)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		id, err := m.routeIntakeReplyOn(t.Context(), tx, 1, 3, in)
		require.Equal(t, forwarded, id)
		return err
	}))
	var stored model.Message
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id=?", forwarded).First(&stored).Error)
	require.Equal(t, target.ID, *stored.ReplyToMessageID)
	require.Equal(t, "INTERNAL", stored.Visibility)
	require.Zero(t, stored.AuthorID)
	require.True(t, strings.HasPrefix(stored.Body, "[From merged IN-9;"))
	require.True(t, strings.HasSuffix(stored.Body, longBody))
	var n int64
	require.NoError(t, db.Table(m.names.Messages.String()).Count(&n).Error)
	require.EqualValues(t, 4, n)
	for _, scenario := range []string{"foreign_sender", "foreign_target", "withdrawn", "hash", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			bad := in
			err := db.Transaction(func(tx *gorm.DB) error {
				switch scenario {
				case "foreign_sender":
					bad.SourceRecipientID = 2
				case "foreign_target":
					bad.TargetRootID = reply.ID
				case "withdrawn":
					require.NoError(t, tx.Table(m.names.Messages.String()).Where("id=?", reply.ID).Update("withdrawn_at", m.now()).Error)
				case "hash":
					bad.TargetHash = strings.Repeat("f", 64)
				case "revoked":
					require.NoError(t, tx.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=4", ch.ID).Update("left_at", m.now()).Error)
				}
				_, err := m.routeIntakeReplyOn(t.Context(), tx, 1, 3, bad)
				require.Error(t, err)
				return ErrNotFound
			})
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestPackageDeliveryPostsActualSharedMessageAndRechecksRecipient(t *testing.T) {
	m, db := conversationHarness(t)
	shared := createConversation(t, m, db, "CLIENT_SHARED")
	internal := createConversation(t, m, db, "INTERNAL")
	ctx := conversationContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(ctx, tx, 1, 3, shared.ID, 4, MemberInput{External: true})
		return err
	}))
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.sendPackageDeliveryOn(ctx, tx, 1, 3, shared.ID, 4, "Delivery 51\nFixed manifest")
		return err
	}))
	require.Positive(t, id)
	var stored model.Message
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id=?", id).Take(&stored).Error)
	require.Equal(t, "CHANNEL", stored.Visibility)
	require.Equal(t, "Delivery 51\nFixed manifest", stored.Body)
	require.Equal(t, guid.ID(2), stored.AuthorID)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.sendPackageDeliveryOn(ctx, tx, 1, 3, internal.ID, 4, "Never public")
		return err
	}))
	require.NoError(t, db.Table(m.names.Members.String()).Where("channel_id=? AND principal_id=4", shared.ID).Update("left_at", m.now()).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.sendPackageDeliveryOn(ctx, tx, 1, 3, shared.ID, 4, "Revoked")
		return err
	}))
	var n int64
	require.NoError(t, db.Table(m.names.Messages.String()).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestIntakeReplyFactsRequireAnActiveExternalThreadInTheProject(t *testing.T) {
	m, db := conversationHarness(t)
	ch := createConversation(t, m, db, "CLIENT_SHARED")
	var root, reply MessageView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.putMemberOn(conversationContext(t, 2), tx, 1, 3, ch.ID, 4, MemberInput{External: true})
		if err != nil {
			return err
		}
		root, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "request", Visibility: "CHANNEL"})
		if err != nil {
			return err
		}
		reply, err = m.postMessageOn(conversationContext(t, 4), tx, 1, 3, ch.ID, PostMessageInput{Body: "additional evidence", Visibility: "CHANNEL", ReplyToMessageID: &root.ID})
		return err
	}))
	read := func(project, id guid.ID) (IntakeReplyFact, error) {
		var fact IntakeReplyFact
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			fact, err = m.intakeReplyFactOn(t.Context(), tx, 1, project, id)
			return err
		})
		return fact, err
	}
	fact, err := read(3, reply.ID)
	require.NoError(t, err)
	require.Equal(t, IntakeReplyFact{ChannelID: ch.ID, RootID: root.ID, RecipientID: 4}, fact)
	for _, id := range []guid.ID{root.ID, 999} {
		_, err = read(3, id)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	_, err = read(9, reply.ID)
	require.Error(t, err)
	_, err = m.intakeReplyFactOn(t.Context(), nil, 1, 3, reply.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", reply.ID).Update("author_id", 0).Error)
	_, err = read(3, reply.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, db.Table(m.names.Messages.String()).Where("id = ?", reply.ID).Update("author_id", 4).Error)
	require.NoError(t, db.Table(m.names.Members.String()).Where("channel_id = ? AND principal_id = ?", ch.ID, 4).Update("left_at", m.now()).Error)
	_, err = read(3, reply.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestPackageDeliveryRecipientFailuresNeverPersistANotice(t *testing.T) {
	for _, scenario := range []string{"invalid", "missing_capability", "provider_error", "inactive", "not_member"} {
		t.Run(scenario, func(t *testing.T) {
			m, db := conversationHarness(t)
			ch := createConversation(t, m, db, "CLIENT_SHARED")
			fault := errors.New("principal unavailable")
			recipient := guid.ID(4)
			expected := ErrNotFound
			switch scenario {
			case "invalid":
				recipient = 0
				expected = ErrInvalidArgument
			case "missing_capability":
				m.principals.LockActiveRow = nil
				expected = ErrUnavailable
			case "provider_error":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
				expected = fault
			case "inactive":
				recipient = 8
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, err := m.sendPackageDeliveryOn(conversationContext(t, 2), tx, 1, 3, ch.ID, recipient, "delivery notice")
				return err
			})
			require.ErrorIs(t, err, expected)
			var n int64
			require.NoError(t, db.Table(m.names.Messages.String()).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
