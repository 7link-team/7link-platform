package collab

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrMessageNotEditable = errors.New("collab: message not editable")
var ErrMessageVersion = errors.New("collab: stale message version")

type EditMessageInput struct {
	Body                string     `json:"body" minLength:"1" maxLength:"8000"`
	MentionPrincipalIDs *[]guid.ID `json:"mention_principal_ids,omitempty" maxItems:"50"`
}

// Existing message producers persist text only. Structured system cards must
// use their own immutable producer, never this author command.
func (m *Module) messageAccessOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string) (*model.Message, *model.Channel, string, error) {
	q := query.BindMessages(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant)).First()
	if err != nil {
		return nil, nil, "", err
	}
	c := query.BindChannels(tx, m.names)
	ch, err := c.WithContext(ctx).Where(c.ID.Eq(row.ChannelID), c.TenantID.Eq(tenant)).First()
	if err != nil {
		return nil, nil, "", err
	}
	kind := "HUMAN"
	if ch.Kind == "DIRECT" {
		ch, err = m.directAccessOn(ctx, tx, tenant, ch.ID, permission != "channel_read" && permission != "audit_read")
		if err == nil {
			members := query.BindMembers(tx, m.names)
			member, e := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ChannelID.Eq(ch.ID), members.PrincipalID.Eq(authn.PrincipalIDFromContext(ctx))).First()
			if e != nil {
				return nil, nil, "", e
			}
			kind = member.MemberKind
		}
		if err == nil && permission == "audit_read" {
			if m.policy == nil {
				return nil, nil, "", ErrUnavailable
			}
			allowed, e := m.policy(ctx, tx, tenant, 0, "audit_read", authn.PrincipalIDFromContext(ctx))
			if e != nil {
				return nil, nil, "", e
			}
			if !allowed.AllowAll {
				return nil, nil, "", ErrForbidden
			}
		}
	} else {
		if ch.ProjectID == nil {
			return nil, nil, "", ErrNotFound
		}
		var member *model.ChannelMember
		ch, member, err = m.authorizeChannel(ctx, tx, tenant, *ch.ProjectID, ch.ID, permission)
		if err == nil {
			if member.External && row.Visibility == "INTERNAL" {
				return nil, nil, "", ErrNotFound
			}
			kind = member.MemberKind
		}
	}
	if err != nil {
		return nil, nil, "", err
	}
	// Principal/policy/channel locks precede the message lock in every command.
	row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(id), q.TenantID.Eq(tenant), q.ChannelID.Eq(ch.ID)).First()
	return row, ch, kind, err
}

func (m *Module) mutateMessageOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, expected int, in EditMessageInput, retract bool) (MessageView, error) {
	ctx = mutationContext(ctx)
	var empty MessageView
	if expected < 1 {
		return empty, ErrInvalidArgument
	}
	if !retract {
		in.Body = strings.TrimSpace(in.Body)
		if in.Body == "" || utf8.RuneCountInString(in.Body) > 8000 || (in.MentionPrincipalIDs != nil && len(*in.MentionPrincipalIDs) > 50) {
			return empty, ErrInvalidArgument
		}
	}
	required := "channel_post"
	if retract {
		required = "channel_read"
	}
	row, ch, kind, err := m.messageAccessOn(ctx, tx, tenant, id, required)
	if err != nil {
		return empty, err
	}
	var systemCards int64
	if err = query.BindSystemCards(tx, m.names).Where(&model.SystemCard{TenantID: tenant, MessageID: id}).Count(&systemCards).Error; err != nil {
		return empty, err
	}
	if systemCards > 0 {
		return empty, ErrMessageNotEditable
	}
	actor, now := authn.PrincipalIDFromContext(ctx), m.now()
	permission := required
	if row.Version != expected {
		return empty, ErrMessageVersion
	}
	if row.WithdrawnAt != nil {
		return empty, ErrMessageNotEditable
	}
	age := now.Sub(row.CreatedAt)
	if age < 0 {
		return empty, ErrMessageNotEditable
	}
	if !retract {
		if row.AuthorID != actor {
			return empty, ErrForbidden
		}
		if age > 15*time.Minute || row.Version > 10 {
			return empty, ErrMessageNotEditable
		}
	} else if row.AuthorID != actor || age > 24*time.Hour {
		// Human-only DIRECT participants never gain moderation over one another.
		if ch.Kind == "DIRECT" || ch.ProjectID == nil {
			return empty, ErrForbidden
		}
		if _, _, e := m.authorizeChannel(ctx, tx, tenant, *ch.ProjectID, ch.ID, "channel_manage"); e != nil {
			return empty, e
		}
		permission = "channel_manage"
	}
	if m.audit == nil || m.publisher == nil {
		return empty, ErrUnavailable
	}
	// Revision N is the exact prior content of message version N. Retraction
	// preserves the last version too, then clears the ordinary persisted body.
	revision := model.MessageRevision{TenantID: tenant, MessageID: id, Revision: row.Version, Body: row.Body, EditedBy: actor, EditedAt: now}
	if err = query.BindRevisions(tx.WithContext(ctx), m.names).Create(&revision).Error; err != nil {
		return empty, err
	}
	added := []guid.ID{}
	mm := query.BindMentions(tx, m.names)
	existing, err := mm.WithContext(ctx).Where(mm.TenantID.Eq(tenant), mm.MessageID.Eq(id)).Find()
	if err != nil {
		return empty, err
	}
	mentions := []guid.ID{}
	for _, mention := range existing {
		mentions = append(mentions, mention.PrincipalID)
	}
	if !retract && in.MentionPrincipalIDs != nil {
		next := slices.Clone(*in.MentionPrincipalIDs)
		slices.Sort(next)
		next = slices.Compact(next)
		cm := query.BindMembers(tx, m.names)
		for _, target := range next {
			member, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(ch.ID), cm.PrincipalID.Eq(target), cm.LeftAt.IsNull()).First()
			if errors.Is(e, gorm.ErrRecordNotFound) || target <= 0 {
				return empty, ErrInvalidArgument
			}
			if e != nil {
				return empty, e
			}
			if member.External && row.Visibility == "INTERNAL" {
				return empty, ErrForbidden
			}
			if !slices.Contains(mentions, target) {
				added = append(added, target)
			}
		}
		if _, err = mm.WithContext(ctx).Where(mm.TenantID.Eq(tenant), mm.MessageID.Eq(id)).Delete(); err != nil {
			return empty, err
		}
		for _, target := range next {
			if err = mm.WithContext(ctx).Create(&model.MessageMention{TenantID: tenant, ChannelID: ch.ID, MessageID: id, PrincipalID: target}); err != nil {
				return empty, err
			}
		}
		mentions = next
	}
	event := "collab.message_edited"
	changes := map[string]any{"body": in.Body, "version": row.Version + 1, "updated_at": now}
	// Keep creation order stable. Root mutations advance the channel sequence
	// independently so an Agent that read the old root must reread the thread.
	if row.ReplyToMessageID == nil {
		row.ContextSeq = ch.LastSeq + 1
		changes["context_seq"] = row.ContextSeq
		c := query.BindChannels(tx, m.names)
		changed, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(ch.ID), c.LastSeq.Eq(ch.LastSeq)).Updates(map[string]any{"last_seq": row.ContextSeq, "version": ch.Version + 1, "updated_at": now})
		if e != nil {
			return empty, e
		}
		if changed.RowsAffected != 1 {
			return empty, ErrMessageVersion
		}
	}
	if retract {
		event = "collab.message_retracted"
		changes["body"] = ""
		changes["withdrawn_at"] = now
	}
	q := query.BindMessages(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.Version.Eq(expected), q.WithdrawnAt.IsNull()).Updates(changes)
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrMessageVersion
	}
	project := guid.ID(0)
	authority := "DIRECT_PARTICIPANT"
	if ch.ProjectID != nil {
		project = *ch.ProjectID
		authority = "IAM:" + permission
	}
	auditEvent := audit.NewDomainEvent(ctx, tenant, event, "message", id)
	auditEvent.Detail["channel_id"] = ch.ID.String()
	auditEvent.Detail["project_id"] = project.String()
	auditEvent.Detail["version"] = row.Version + 1
	auditEvent.Detail["reason"] = ""
	auditEvent.Detail["evidence_refs"] = []string{}
	auditEvent.Detail["authority_source"] = authority
	if err = m.audit(ctx, tx, auditEvent); err != nil {
		return empty, err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: event, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Subject: events.Subject{Kind: "message", ID: id}, SubjectVersion: int64(row.Version + 1), ExtraTopics: conversationTopics(tenant, ch.ID, row.ReplyToMessageID), Data: map[string]any{"channel_id": ch.ID.String(), "added_mention_ids": added}})
	if err != nil {
		return empty, err
	}
	if m.messageObserver != nil && len(added) > 0 {
		cm := query.BindMembers(tx, m.names)
		for _, target := range added {
			member, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(ch.ID), cm.PrincipalID.Eq(target), cm.MemberKind.Eq("AGENT"), cm.LeftAt.IsNull()).First()
			if errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			}
			if e != nil {
				return empty, e
			}
			if member.PrincipalID == actor {
				continue
			}
			if e = m.messageObserver(ctx, tx, MessageReference{TenantID: tenant, AgentPrincipalID: target, MessageID: id, ChannelID: ch.ID, ThreadID: row.ReplyToMessageID, Seq: row.Seq, Kind: "MENTION"}); e != nil {
				return empty, e
			}
		}
	}
	row.Version++
	row.UpdatedAt = now
	row.Body = in.Body
	if retract {
		row.WithdrawnAt = &now
		row.Body = ""
	}
	out := messageView(row)
	out.Mentions = mentions
	return out, nil
}

func (m *Module) messageRevisionsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) ([]model.MessageRevision, error) {
	if _, _, _, err := m.messageAccessOn(ctx, tx, tenant, id, "audit_read"); err != nil {
		return nil, err
	}
	rows := []model.MessageRevision{}
	err := query.BindRevisions(tx.WithContext(ctx), m.names).Where(map[string]any{"tenant_id": tenant, "message_id": id}).Order("revision").Find(&rows).Error
	return rows, err
}
