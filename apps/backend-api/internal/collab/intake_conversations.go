package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// IntakeConversation exposes only real client-shared roots and the canonical
// reply command. It does not attest delivery through email or a webhook.
type IntakeConversation struct {
	BindWorkOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	ForwardOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, IntakeReplyForward) (guid.ID, error)
	ReadOn     func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string) (CursorPage[MessageView], error)
	CheckOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) error
	ReplyOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string) (guid.ID, error)
}

func (m *Module) provideIntakeConversations(s *module.Services) error {
	return module.Provide[IntakeConversation](s, IntakeConversation{BindWorkOn: m.bindIntakeMessageWorkOn, ForwardOn: m.forwardIntakeReplyOn, ReadOn: m.readIntakeConversationOn, CheckOn: m.intakeConversationOn, ReplyOn: m.replyIntakeConversationOn})
}

func (m *Module) readIntakeConversationOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash, cursor string) (CursorPage[MessageView], error) {
	if err := m.intakeConversationOn(ctx, tx, tenant, project, channel, root, recipient, hash); err != nil {
		return CursorPage[MessageView]{}, err
	}
	return m.listMessagesOn(ctx, tx, tenant, project, channel, MessageFilter{ThreadID: &root, Cursor: cursor, Limit: 50, Activity: "all"})
}

func (m *Module) intakeConversationOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, contentHash string) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || channel <= 0 || root <= 0 || recipient <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	ch, _, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if err != nil {
		return err
	}
	if ch.Visibility != "CLIENT_SHARED" {
		return ErrNotFound
	}
	if _, err := m.threadAccessOn(ctx, tx, tenant, project, root, recipient); err != nil {
		return err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(root), q.ChannelID.Eq(channel), q.AuthorID.Eq(recipient), q.Visibility.Eq("CHANNEL"), q.ReplyToMessageID.IsNull(), q.WithdrawnAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(row.Body))
	if contentHash != hex.EncodeToString(digest[:]) {
		return ErrNotFound
	}
	return nil
}

func (m *Module) replyIntakeConversationOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, contentHash, body string) (guid.ID, error) {
	if err := m.intakeConversationOn(ctx, tx, tenant, project, channel, root, recipient, contentHash); err != nil {
		return 0, err
	}
	view, err := m.postMessageOn(ctx, tx, tenant, project, channel, PostMessageInput{Body: body, ReplyToMessageID: &root, Visibility: "CHANNEL"})
	if err != nil {
		return 0, err
	}
	return view.ID, nil
}

// IntakeReplyForward names actual stored messages. A team member forwards the
// original client's reply; the created message never impersonates that client.
type IntakeReplyForward struct {
	SourceChannelID, SourceRootID, SourceRecipientID, MessageID guid.ID
	TargetChannelID, TargetRootID, TargetRecipientID            guid.ID
	SourceHash, TargetHash                                      string
	SourceRef                                                   int
}

func (m *Module) forwardIntakeReplyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in IntakeReplyForward) (guid.ID, error) {
	if in.MessageID <= 0 || in.SourceRef < 1 || in.SourceRootID == in.TargetRootID {
		return 0, ErrInvalidArgument
	}
	if err := m.intakeConversationOn(ctx, tx, tenant, project, in.SourceChannelID, in.SourceRootID, in.SourceRecipientID, in.SourceHash); err != nil {
		return 0, err
	}
	if err := m.intakeConversationOn(ctx, tx, tenant, project, in.TargetChannelID, in.TargetRootID, in.TargetRecipientID, in.TargetHash); err != nil {
		return 0, err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(in.SourceChannelID), q.ID.Eq(in.MessageID), q.AuthorID.Eq(in.SourceRecipientID), q.ReplyToMessageID.Eq(in.SourceRootID), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	body := fmt.Sprintf("[From merged IN-%d; source message %s]\n%s", in.SourceRef, in.MessageID.String(), row.Body)
	view, err := m.postMessageOn(ctx, tx, tenant, project, in.TargetChannelID, PostMessageInput{Body: body, ReplyToMessageID: &in.TargetRootID, Visibility: "CHANNEL"})
	if err != nil {
		return 0, err
	}
	return view.ID, nil
}

func (m *Module) bindIntakeMessageWorkOn(ctx context.Context, tx *gorm.DB, tenant, project, message, work guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || message <= 0 || work <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	if m.workReplay == nil {
		return ErrUnavailable
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(message), q.WithdrawnAt.IsNull()).First()
	if err != nil {
		return err
	}
	if _, err = m.conversionSourceOn(ctx, tx, tenant, project, row.ChannelID, message); err != nil {
		return err
	}
	if err = m.workReplay(ctx, tx, tenant, project, work); err != nil {
		return err
	}
	links := query.BindMessageWorks(tx, m.names)
	existing, err := links.WithContext(ctx).Where(links.TenantID.Eq(tenant), links.MessageID.Eq(message)).Take()
	if err == nil {
		if existing.ProjectID != project || existing.WorkItemID != work {
			return ErrNotFound
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// MessageWork is the single canonical creation source for a work item.
	// Follow-up questions and forwarded replies retain that source; the caller
	// records their exact message/work association in its transactional audit.
	existing, err = links.WithContext(ctx).Where(links.TenantID.Eq(tenant), links.WorkItemID.Eq(work)).Take()
	if err == nil {
		if existing.ProjectID != project {
			return ErrNotFound
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return links.WithContext(ctx).Create(&model.MessageWork{TenantID: tenant, ProjectID: project, MessageID: message, ChannelID: row.ChannelID, WorkItemID: work, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now()})
}
