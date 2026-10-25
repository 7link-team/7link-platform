package collab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IntakeDispositionReceipts reads an actual shared reply and the original
// recipient's persisted read cursor. It never infers email or webhook delivery.
type IntakeDispositionReceipts func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string) (bool, error)

// IntakeDispositionNotice retains ordinary channel authority and Agent cursor
// interruption while posting the exact disposition as a real shared reply.
type IntakeDispositionNotice func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, string, string, *int64) (guid.ID, error)

type IntakeReplyFact struct{ ChannelID, RootID, RecipientID guid.ID }
type IntakeReplyRouting struct {
	ReadOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (IntakeReplyFact, error)
	ForwardOn func(context.Context, *gorm.DB, guid.ID, guid.ID, IntakeReplyForward) (guid.ID, error)
}

func (m *Module) provideDisposition(s *module.Services) error {
	if err := module.Provide[PackageDeliverySender](s, PackageDeliverySender{CheckOn: m.packageDeliveryChannelOn, SendOn: m.sendPackageDeliveryOn}); err != nil {
		return err
	}
	if err := module.Provide[IntakeDispositionNotice](s, m.intakeDispositionNoticeOn); err != nil {
		return err
	}
	if err := module.Provide[IntakeReplyRouting](s, IntakeReplyRouting{ReadOn: m.intakeReplyFactOn, ForwardOn: m.routeIntakeReplyOn}); err != nil {
		return err
	}
	return module.Provide[IntakeDispositionReceipts](s, m.intakeDispositionReceiptOn)
}

func (m *Module) intakeDispositionNoticeOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash, body string, seen *int64) (guid.ID, error) {
	if err := m.intakeConversationOn(ctx, tx, tenant, project, channel, root, recipient, hash); err != nil {
		return 0, err
	}
	message, err := m.postMessageOn(ctx, tx, tenant, project, channel, PostMessageInput{Body: body, Visibility: "CHANNEL", ReplyToMessageID: &root, SeenSeq: seen})
	return message.ID, err
}

func (m *Module) intakeReplyFactOn(ctx context.Context, tx *gorm.DB, tenant, project, message guid.ID) (IntakeReplyFact, error) {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return IntakeReplyFact{}, err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(message), q.ReplyToMessageID.IsNotNull(), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
	if err != nil {
		return IntakeReplyFact{}, err
	}
	if row.AuthorID <= 0 {
		return IntakeReplyFact{}, ErrNotFound
	}
	if err := m.routingRootOn(ctx, tx, tenant, project, row.ChannelID, *row.ReplyToMessageID, row.AuthorID, ""); err != nil {
		return IntakeReplyFact{}, err
	}
	return IntakeReplyFact{ChannelID: row.ChannelID, RootID: *row.ReplyToMessageID, RecipientID: row.AuthorID}, nil
}

// System forwarding is INTERNAL, even when the target is client-shared. The
// target's other contact never receives another contact's original reply.
func (m *Module) routeIntakeReplyOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in IntakeReplyForward) (guid.ID, error) {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return 0, err
	}
	if in.MessageID <= 0 || in.SourceRef <= 0 || in.SourceRootID == in.TargetRootID {
		return 0, ErrInvalidArgument
	}
	if err := m.routingRootOn(ctx, tx, tenant, project, in.SourceChannelID, in.SourceRootID, in.SourceRecipientID, in.SourceHash); err != nil {
		return 0, err
	}
	if err := m.routingRootOn(ctx, tx, tenant, project, in.TargetChannelID, in.TargetRootID, in.TargetRecipientID, in.TargetHash); err != nil {
		return 0, err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(in.SourceChannelID), q.ID.Eq(in.MessageID), q.AuthorID.Eq(in.SourceRecipientID), q.ReplyToMessageID.Eq(in.SourceRootID), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
	if err != nil {
		return 0, err
	}
	header := fmt.Sprintf("[From merged IN-%d; source message %s]", in.SourceRef, in.MessageID.String())
	view, err := m.postSystemCardOn(ctx, tx, SystemCardInput{TenantID: tenant, ProjectID: project, ChannelID: in.TargetChannelID, SourceID: in.MessageID, Kind: "EVENT", Body: header, Payload: map[string]any{"source_message_id": in.MessageID.String(), "source_ref": in.SourceRef, "target_root_id": in.TargetRootID.String()}})
	if err != nil {
		return 0, err
	}
	if view.ReplyToMessageID != nil && *view.ReplyToMessageID != in.TargetRootID {
		return 0, ErrInvalidArgument
	}
	if view.ReplyToMessageID != nil {
		return view.ID, nil
	}
	// Preserve the entire canonical reply, including an 8000-rune Unicode
	// message. The bounded header is a system annotation, not client input.
	_, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(in.TargetChannelID), q.ID.Eq(view.ID), q.Visibility.Eq("INTERNAL"), q.ReplyToMessageID.IsNull()).Updates(map[string]any{"reply_to_message_id": in.TargetRootID, "body": header + "\n" + row.Body})
	return view.ID, err
}

func (m *Module) routingRootOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient guid.ID, hash string) error {
	c := query.BindChannels(tx, m.names)
	if _, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.ID.Eq(channel), c.Visibility.Eq("CLIENT_SHARED"), c.ArchivedAt.IsNull()).First(); err != nil {
		return err
	}
	members := query.BindMembers(tx, m.names)
	if _, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ChannelID.Eq(channel), members.PrincipalID.Eq(recipient), members.External.Is(true), members.LeftAt.IsNull()).First(); err != nil {
		return err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(root), q.ChannelID.Eq(channel), q.AuthorID.Eq(recipient), q.ReplyToMessageID.IsNull(), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(row.Body))
	if hash != "" && hash != hex.EncodeToString(digest[:]) {
		return ErrNotFound
	}
	return nil
}

func (m *Module) intakeDispositionReceiptOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, root, recipient, message guid.ID, hash string) (bool, error) {
	if message <= 0 {
		return false, ErrInvalidArgument
	}
	if err := m.intakeConversationOn(ctx, tx, tenant, project, channel, root, recipient, hash); err != nil {
		return false, err
	}
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.ID.Eq(message), q.ReplyToMessageID.Eq(root), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
	if err != nil {
		return false, err
	}
	members := query.BindMembers(tx, m.names)
	member, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ChannelID.Eq(channel), members.PrincipalID.Eq(recipient), members.External.Is(true), members.LeftAt.IsNull()).First()
	if err != nil {
		return false, err
	}
	return member.LastReadSeq >= row.Seq, nil
}

// PackageDeliverySender posts an actual shared-channel notice. Success means the
// message was persisted in the recipient's channel, never email delivery/read.
type PackageDeliverySender struct {
	CheckOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error
	SendOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) (guid.ID, error)
}

func (m *Module) packageDeliveryChannelOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, recipient guid.ID) error {
	if recipient <= 0 {
		return ErrInvalidArgument
	}
	ch, member, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_post")
	if err != nil {
		return err
	}
	if ch.Visibility != "CLIENT_SHARED" || ch.Kind == "DIRECT" || member.External {
		return ErrForbidden
	}
	if m.principals.LockActiveRow == nil {
		return ErrUnavailable
	}
	principal, active, err := m.principals.LockActiveRow(ctx, tx, recipient)
	if err != nil {
		return err
	}
	if !active || principal.ID != recipient {
		return ErrNotFound
	}
	q := query.BindMembers(tx, m.names)
	_, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.PrincipalID.Eq(recipient), q.External.Is(true), q.LeftAt.IsNull()).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
func (m *Module) sendPackageDeliveryOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, recipient guid.ID, body string) (guid.ID, error) {
	if err := m.packageDeliveryChannelOn(ctx, tx, tenant, project, channel, recipient); err != nil {
		return 0, err
	}
	message, err := m.postMessageOn(ctx, tx, tenant, project, channel, PostMessageInput{Body: body, Visibility: "CHANNEL"})
	return message.ID, err
}
