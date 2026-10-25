package collab

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	platformquery "7link.com/platform/pkg/query"
	"gorm.io/gorm"
)

// Cursor scope includes actor and every filter. It conveys position only;
// authorization is re-evaluated inside every request's transaction.
type conversationCursor struct {
	Position int64  `json:"position"`
	Scope    string `json:"scope"`
}

func cursorScope(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}
func readCursor(raw, scope string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > 512 {
		return 0, ErrInvalidArgument
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, ErrInvalidArgument
	}
	var c conversationCursor
	if json.Unmarshal(b, &c) != nil || c.Position <= 0 || c.Scope != scope {
		return 0, ErrInvalidArgument
	}
	return c.Position, nil
}
func nextCursor(pos int64, scope string) string {
	b, _ := json.Marshal(conversationCursor{Position: pos, Scope: scope})
	return base64.RawURLEncoding.EncodeToString(b)
}

type CursorPage[T any] struct {
	MaxSeq     int64  `json:"-"`
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
type MessageFilter struct {
	Cursor   string
	Limit    int
	AfterSeq int64
	ThreadID *guid.ID
	Activity string
	Search   string
}

func (m *Module) listMessagesOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, in MessageFilter) (CursorPage[MessageView], error) {
	out := CursorPage[MessageView]{Items: []MessageView{}}
	if in.Limit < 1 || in.Limit > 100 || in.AfterSeq < 0 || len([]rune(in.Search)) > 200 || (in.Activity != "all" && in.Activity != "unread" && in.Activity != "mentions" && in.Activity != "saved") {
		return out, ErrInvalidArgument
	}
	ch, member, err := m.authorizeChannelState(ctx, tx, tenant, project, channel, "channel_read", true, false)
	if err != nil {
		return out, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	thread := ""
	if in.ThreadID != nil {
		thread = in.ThreadID.String()
	}
	scope := cursorScope("messages", tenant.String(), project.String(), channel.String(), actor.String(), thread, in.Activity, in.Search, strconv.FormatInt(in.AfterSeq, 10), strconv.FormatInt(member.LastReadSeq, 10), strconv.FormatBool(member.External), ch.Visibility)
	before, err := readCursor(in.Cursor, scope)
	if err != nil {
		return out, err
	}
	q := query.BindMessages(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel))
	out.MaxSeq = ch.LastSeq
	if member.External {
		request = request.Where(q.Visibility.Eq("CHANNEL"))
	}
	if in.ThreadID != nil {
		parent, err := q.WithContext(ctx).Where(q.ID.Eq(*in.ThreadID), q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.ReplyToMessageID.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && member.External && parent.Visibility == "INTERNAL") {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		request = request.Where(q.ReplyToMessageID.Eq(*in.ThreadID))
		out.MaxSeq = parent.Seq
	}
	if member.External || in.ThreadID != nil {
		latest, e := request.Order(q.Seq.Desc()).Limit(1).Find()
		if e != nil {
			return out, e
		}
		if len(latest) > 0 {
			out.MaxSeq = latest[0].Seq
		} else if in.ThreadID == nil {
			out.MaxSeq = 0
		}
	}
	if before > 0 {
		request = request.Where(q.Seq.Lt(before))
	}
	if in.AfterSeq > 0 {
		request = request.Where(q.Seq.Gt(in.AfterSeq))
	}
	if in.Activity == "unread" {
		request = request.Where(q.Seq.Gt(member.LastReadSeq))
	}
	if in.Activity == "mentions" {
		mentions := query.BindMentions(tx, m.names)
		request = request.Where(q.WithContext(ctx).Columns(q.ID).In(mentions.WithContext(ctx).Select(mentions.MessageID).Where(mentions.TenantID.Eq(tenant), mentions.ChannelID.Eq(channel), mentions.PrincipalID.Eq(actor))))
	}
	if in.Activity == "saved" {
		saved := query.BindSaved(tx, m.names)
		request = request.Where(q.WithContext(ctx).Columns(q.ID).In(saved.WithContext(ctx).Select(saved.MessageID).Where(saved.TenantID.Eq(tenant), saved.ChannelID.Eq(channel), saved.PrincipalID.Eq(actor))))
	}
	if term := platformquery.EscapeLike(in.Search); term != "" {
		request = request.Where(q.Body.Like("%" + term + "%"))
	}
	rows, err := request.Order(q.Seq.Desc()).Limit(in.Limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > in.Limit
	if out.HasMore {
		rows = rows[:in.Limit]
	}
	if len(rows) == 0 {
		return out, nil
	}
	// Hydrate only this authorized, bounded page. Related reads remain constant
	// as the page grows, and hidden messages never enter the ID set.
	ids := make([]driver.Valuer, len(rows))
	positions := make(map[guid.ID]int, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		positions[row.ID] = i
		out.Items = append(out.Items, messageView(row))
	}
	a := query.BindAttachments(tx, m.names)
	attachments, err := a.WithContext(ctx).Where(a.TenantID.Eq(tenant), a.ChannelID.Eq(channel), a.MessageID.In(ids...)).Order(a.ID).Find()
	if err != nil {
		return out, err
	}
	for _, attachment := range attachments {
		v := &out.Items[positions[attachment.MessageID]]
		v.Attachments = append(v.Attachments, AttachmentView{ID: attachment.ID, FileID: attachment.FileID, VersionID: attachment.VersionID})
	}
	mm := query.BindMentions(tx, m.names)
	mentions, err := mm.WithContext(ctx).Where(mm.TenantID.Eq(tenant), mm.ChannelID.Eq(channel), mm.MessageID.In(ids...)).Order(mm.PrincipalID).Find()
	if err != nil {
		return out, err
	}
	for _, mention := range mentions {
		v := &out.Items[positions[mention.MessageID]]
		v.Mentions = append(v.Mentions, mention.PrincipalID)
	}
	saved := query.BindSaved(tx, m.names)
	saves, err := saved.WithContext(ctx).Where(saved.TenantID.Eq(tenant), saved.ChannelID.Eq(channel), saved.MessageID.In(ids...), saved.PrincipalID.Eq(actor)).Find()
	if err != nil {
		return out, err
	}
	for _, save := range saves {
		out.Items[positions[save.MessageID]].Saved = true
	}
	if err = m.hydrateSystemCardsOn(tx, tenant, out.Items); err != nil {
		return out, err
	}
	if out.HasMore {
		out.NextCursor = nextCursor(rows[len(rows)-1].Seq, scope)
	}
	return out, nil
}

// Read markers only move forward and cannot acknowledge a hidden internal
// message. Consequently an external reader cannot probe internal sequence heads.
func (m *Module) markReadOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, seq int64) (int64, error) {
	_, member, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if err != nil {
		return 0, err
	}
	if seq < 0 {
		return 0, ErrInvalidArgument
	}
	if seq <= member.LastReadSeq {
		return member.LastReadSeq, nil
	}
	q := query.BindMessages(tx, m.names)
	request := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.Seq.Eq(seq))
	if member.External {
		request = request.Where(q.Visibility.Eq("CHANNEL"))
	}
	n, err := request.Count()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrNotFound
	}
	cm := query.BindMembers(tx, m.names)
	_, err = cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(member.PrincipalID), cm.LastReadSeq.Lt(seq)).Updates(map[string]any{"last_read_seq": seq, "updated_at": m.now()})
	if err != nil {
		return 0, err
	}
	return seq, m.personalConversationEvent(ctx, tx, tenant, project, channel, "collab.read_updated", member.MemberKind)
}
func (m *Module) saveMessageOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, message guid.ID, save bool) error {
	_, member, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if err != nil {
		return err
	}
	q := query.BindMessages(tx, m.names)
	request := q.WithContext(ctx).Where(q.ID.Eq(message), q.TenantID.Eq(tenant), q.ChannelID.Eq(channel))
	if member.External {
		request = request.Where(q.Visibility.Eq("CHANNEL"))
	}
	n, err := request.Count()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	s := query.BindSaved(tx, m.names)
	existing := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.ChannelID.Eq(channel), s.MessageID.Eq(message), s.PrincipalID.Eq(member.PrincipalID))
	if !save {
		result, e := existing.Delete()
		if e != nil || result.RowsAffected == 0 {
			return e
		}
		return m.personalConversationEvent(ctx, tx, tenant, project, channel, "collab.saved_updated", member.MemberKind)
	}
	n, err = existing.Count()
	if err != nil || n > 0 {
		return err
	}
	if err = s.WithContext(ctx).Create(&model.SavedMessage{MessageID: message, ChannelID: channel, TenantID: tenant, PrincipalID: member.PrincipalID, CreatedAt: m.now()}); err != nil {
		return err
	}
	return m.personalConversationEvent(ctx, tx, tenant, project, channel, "collab.saved_updated", member.MemberKind)
}
