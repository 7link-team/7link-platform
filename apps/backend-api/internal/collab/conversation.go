package collab

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrForbidden = errors.New("collab: forbidden")

func (m *Module) injectConversations(s *module.Services) error {
	var err error
	if m.tenantPolicy, err = module.Resolve[core.TenantConstraintOn](s); err != nil {
		return err
	}
	if m.policy, err = module.Resolve[core.EntityConstraintOn](s); err != nil {
		return err
	}
	if m.owner, err = module.Resolve[core.EntityOwnerResolver](s); err != nil {
		return err
	}
	if m.registrar, err = module.Resolve[*authz.Registrar](s); err != nil {
		return err
	}
	if m.publisher, err = module.Resolve[events.Publisher](s); err != nil {
		return err
	}
	if m.replay, err = module.Resolve[idempotency.Store](s); err != nil {
		return err
	}
	if m.registrar == nil || m.registrar.Registry() == nil {
		return ErrUnavailable
	}
	if err = m.registrar.Registry().Contribute([]authz.Action{
		{Resource: "channel", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "read conversation channels"},
		{Resource: "channel", Verb: "post", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "post conversation messages"},
		{Resource: "channel", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true, Summary: "manage conversation channels"},
	}, nil); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.Catalog](s)
	if err != nil {
		return err
	}
	if err = catalog.AddTypes(Name,
		events.TypeSpec{Name: "collab.system_updated", SchemaVersion: 1, SubjectKind: "message", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.system_posted", SchemaVersion: 1, SubjectKind: "message", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.media_requested", SchemaVersion: 1, SubjectKind: "channel", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.media_completed", SchemaVersion: 1, SubjectKind: "channel", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.message_edited", SchemaVersion: 1, SubjectKind: "message", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.message_retracted", SchemaVersion: 1, SubjectKind: "message", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.read_updated", SchemaVersion: 1, SubjectKind: "inbox", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.saved_updated", SchemaVersion: 1, SubjectKind: "inbox", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.message_posted", SchemaVersion: 1, SubjectKind: "message", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.channel_updated", SchemaVersion: 1, SubjectKind: "channel", Realtime: events.RealtimeSignal},
		events.TypeSpec{Name: "collab.member_updated", SchemaVersion: 1, SubjectKind: "channel", Realtime: events.RealtimeSignal},
	); err != nil {
		return err
	}
	return m.injectSystemProjector(s)
}

// authorizeChannel locks the principal before the IAM policy fence and channel.
// Explicit membership is mandatory even for administrators. External members
// additionally require a shared channel and can never access internal threads.
func (m *Module) authorizeChannel(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, permission string) (*model.Channel, *model.ChannelMember, error) {
	return m.authorizeChannelState(ctx, tx, tenant, project, channel, permission, false, false)
}

func (m *Module) authorizeChannelState(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, permission string, archived, departed bool) (*model.Channel, *model.ChannelMember, error) {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return nil, nil, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, nil, ErrForbidden
	}
	if m.policy == nil || m.principals.LockActiveRow == nil || m.members == nil {
		return nil, nil, ErrUnavailable
	}
	principal, ok, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return nil, nil, err
	}
	if !ok || principal.ID != actor {
		return nil, nil, ErrForbidden
	}
	active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return nil, nil, err
	}
	if !active {
		return nil, nil, ErrForbidden
	}
	constraint, err := m.policy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return nil, nil, err
	}
	c := query.BindChannels(tx, m.names)
	channelQuery := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.ID.Eq(channel), c.TenantID.Eq(tenant), c.ProjectID.Eq(project))
	if !archived {
		channelQuery = channelQuery.Where(c.ArchivedAt.IsNull())
	}
	row, err := channelQuery.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	cm := query.BindMembers(tx, m.names)
	memberQuery := cm.WithContext(ctx).Where(cm.ChannelID.Eq(channel), cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(actor))
	if !departed {
		memberQuery = memberQuery.Where(cm.LeftAt.IsNull())
	}
	member, err := memberQuery.First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if member.External && row.Visibility != "CLIENT_SHARED" {
		return nil, nil, ErrNotFound
	}
	// External clients have an explicit shared-channel read capability, not an
	// internal channel_read grant. All other operations retain ordinary IAM.
	if !(member.External && row.Visibility == "CLIENT_SHARED" && permission == "channel_read") &&
		(!constraint.AllowsEntityRows(project) || (!constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, project.String()) && !slices.Contains(constraint.OwnerIDs, actor.String()))) {
		return nil, nil, ErrForbidden
	}
	return row, member, nil
}

type AttachmentInput struct {
	SpaceID             guid.ID `json:"space_id"`
	FileID              guid.ID `json:"file_id"`
	VersionID           guid.ID `json:"version_id"`
	ExpectedFileVersion int64   `json:"expected_file_version" minimum:"1"`
}
type PostMessageInput struct {
	Kind                string            `json:"kind,omitempty" enum:"TEXT"`
	SeenSeq             *int64            `json:"seen_seq,omitempty" minimum:"0"`
	Body                string            `json:"body" minLength:"1" maxLength:"8000"`
	ReplyToMessageID    *guid.ID          `json:"reply_to_message_id,omitempty"`
	Visibility          string            `json:"visibility" enum:"CHANNEL,INTERNAL" default:"CHANNEL"`
	MentionPrincipalIDs []guid.ID         `json:"mention_principal_ids,omitempty" maxItems:"50"`
	Attachments         []AttachmentInput `json:"attachments,omitempty" maxItems:"5"`
}
type AttachmentView struct {
	ID        guid.ID `json:"id"`
	FileID    guid.ID `json:"file_id"`
	VersionID guid.ID `json:"version_id"`
}
type MessageView struct {
	Kind             string           `json:"kind" enum:"TEXT,EVENT,DECISION_CARD,DISPATCH_CARD,SEARCH_CARD"`
	FoldKey          string           `json:"fold_key,omitempty"`
	Attention        bool             `json:"attention"`
	Payload          map[string]any   `json:"payload,omitempty"`
	ID               guid.ID          `json:"id"`
	ChannelID        guid.ID          `json:"channel_id"`
	Seq              int64            `json:"seq"`
	StreamSeq        int64            `json:"stream_seq"`
	AuthorID         guid.ID          `json:"author_id"`
	Body             string           `json:"body" nullable:"true"`
	ReplyToMessageID *guid.ID         `json:"reply_to_message_id,omitempty"`
	Visibility       string           `json:"visibility"`
	Version          int              `json:"version"`
	CreatedAt        time.Time        `json:"created_at"`
	Attachments      []AttachmentView `json:"attachments"`
	Mentions         []guid.ID        `json:"mentions"`
	Retracted        bool             `json:"retracted"`
	Edited           bool             `json:"edited"`
	Saved            bool             `json:"saved"`
}

func messageView(row *model.Message) MessageView {
	v := MessageView{Kind: "TEXT", ID: row.ID, ChannelID: row.ChannelID, Seq: row.Seq, AuthorID: row.AuthorID, Body: row.Body, ReplyToMessageID: row.ReplyToMessageID, Visibility: row.Visibility, Version: row.Version, CreatedAt: row.CreatedAt, Retracted: row.WithdrawnAt != nil, Edited: row.Version > 1, Attachments: []AttachmentView{}, Mentions: []guid.ID{}}
	v.StreamSeq = max(row.Seq, row.ContextSeq)
	if v.Retracted {
		v.Body = ""
	}
	return v
}
func (m *Module) postMessageOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, in PostMessageInput) (MessageView, error) {
	var empty MessageView
	in.Body = strings.TrimSpace(in.Body)
	if (in.Kind != "" && in.Kind != "TEXT") || in.Body == "" || utf8.RuneCountInString(in.Body) > 8000 || len(in.Attachments) > 5 || len(in.MentionPrincipalIDs) > 50 || (in.Visibility != "CHANNEL" && in.Visibility != "INTERNAL") {
		return empty, ErrInvalidArgument
	}
	for _, a := range in.Attachments {
		if a.FileID <= 0 || a.SpaceID <= 0 || a.VersionID <= 0 || a.ExpectedFileVersion <= 0 {
			return empty, ErrInvalidArgument
		}
	}
	if len(in.Attachments) > 0 && m.fileResources.PinOn == nil {
		return empty, ErrUnavailable
	}
	if m.ids == nil || m.audit == nil || m.publisher == nil {
		return empty, ErrUnavailable
	}
	if authn.PrincipalIDFromContext(ctx) <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return empty, ErrForbidden
	}
	candidate := query.BindChannels(tx, m.names)
	facts, err := candidate.WithContext(ctx).Where(candidate.TenantID.Eq(tenant), candidate.ProjectID.Eq(project), candidate.ID.Eq(channel)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if facts.Kind == "AGENT_DIRECT" {
		if err = m.checkAgentDirectMembersOn(ctx, tx, tenant, project, channel); err != nil {
			return empty, err
		}
		if in.Visibility != "CHANNEL" {
			return empty, ErrForbidden
		}
		if m.messageObserver == nil {
			return empty, ErrUnavailable
		}
	}
	ch, member, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_post")
	if err != nil {
		return empty, err
	}
	if ch.Kind == "DIRECT" || (member.External && in.Visibility == "INTERNAL") {
		return empty, ErrForbidden
	}
	if in.ReplyToMessageID != nil {
		if *in.ReplyToMessageID <= 0 {
			return empty, ErrInvalidArgument
		}
		q := query.BindMessages(tx, m.names)
		parent, err := q.WithContext(ctx).Where(q.ID.Eq(*in.ReplyToMessageID), q.TenantID.Eq(tenant), q.ChannelID.Eq(channel)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return empty, ErrNotFound
		}
		if err != nil {
			return empty, err
		}
		if parent.ReplyToMessageID != nil || (member.External && parent.Visibility == "INTERNAL") {
			return empty, ErrNotFound
		}
		// Internal ancestry is never downgraded, including by an internal author.
		if parent.Visibility == "INTERNAL" {
			in.Visibility = "INTERNAL"
		}
	}
	// Checking inside this transaction leaves interrupted reservations rolled
	// back; seen_seq is excluded from the logical request's idempotency hash.
	if member.MemberKind == "AGENT" || in.SeenSeq != nil {
		if err := m.checkSeenOn(ctx, tx, tenant, channel, authn.PrincipalIDFromContext(ctx), member.External, ch.LastSeq, in); err != nil {
			return empty, err
		}
	}
	mentions := slices.Clone(in.MentionPrincipalIDs)
	slices.Sort(mentions)
	mentions = slices.Compact(mentions)
	cm := query.BindMembers(tx, m.names)
	for _, id := range mentions {
		target, err := cm.WithContext(ctx).Where(cm.ChannelID.Eq(channel), cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(id), cm.LeftAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) || id <= 0 {
			return empty, ErrInvalidArgument
		}
		if err != nil {
			return empty, err
		}
		if target.External && in.Visibility == "INTERNAL" {
			return empty, ErrForbidden
		}
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	now := m.now()
	actor := authn.PrincipalIDFromContext(ctx)
	row := model.Message{ID: guid.ID(id), TenantID: tenant, ChannelID: channel, Seq: ch.LastSeq + 1, ContextSeq: ch.LastSeq + 1, AuthorID: actor, Body: in.Body, ReplyToMessageID: in.ReplyToMessageID, Visibility: in.Visibility, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err = query.BindMessages(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	if err = m.queueChannelDeliveriesOn(ctx, tx, &row); err != nil {
		return empty, err
	}
	view := messageView(&row)
	view.Attachments, err = m.pinAttachmentsOn(ctx, tx, tenant, project, &row, in.Attachments)
	if err != nil {
		return empty, err
	}
	view.Mentions = mentions
	for _, id := range mentions {
		if err = query.BindMentions(tx, m.names).WithContext(ctx).Create(&model.MessageMention{MessageID: row.ID, PrincipalID: id, TenantID: tenant, ChannelID: channel}); err != nil {
			return empty, err
		}
	}
	c := query.BindChannels(tx, m.names)
	result, err := c.WithContext(ctx).Where(c.ID.Eq(channel), c.TenantID.Eq(tenant), c.LastSeq.Eq(ch.LastSeq)).Updates(map[string]any{"last_seq": row.Seq, "last_message_at": now, "updated_at": now, "version": ch.Version + 1})
	if err != nil {
		return empty, err
	}
	if result.RowsAffected != 1 {
		return empty, ErrUnavailable
	}
	if err = m.appendAudit(ctx, tx, tenant, project, channel, "collab.message_posted", 0, ProvisioningAudit{AuthoritySource: "IAM:channel_post"}); err != nil {
		return empty, err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "collab.message_posted", Actor: events.Actor{Kind: member.MemberKind, ID: actor, AuthoritySource: "IAM:channel_post"}, Subject: events.Subject{Kind: "message", ID: row.ID}, SubjectVersion: 1, ExtraTopics: conversationTopics(tenant, channel, row.ReplyToMessageID), Data: map[string]any{"channel_id": channel.String()}})
	if err != nil {
		return empty, err
	}
	if m.messageObserver != nil {
		recipients, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.LeftAt.IsNull(), cm.MemberKind.Eq("AGENT")).Order(cm.PrincipalID).Find()
		if e != nil {
			return empty, e
		}
		for _, recipient := range recipients {
			if recipient.PrincipalID == actor || (recipient.External && row.Visibility == "INTERNAL") {
				continue
			}
			kind := "MESSAGE"
			if slices.Contains(mentions, recipient.PrincipalID) {
				kind = "MENTION"
			}
			if e = m.messageObserver(ctx, tx, MessageReference{TenantID: tenant, AgentPrincipalID: recipient.PrincipalID, MessageID: row.ID, ChannelID: channel, ThreadID: row.ReplyToMessageID, Seq: row.Seq, Kind: kind}); e != nil {
				return empty, e
			}
		}
	}
	return view, nil
}

// A retracted text is absent from every normal projection, including replays.
func (v MessageView) MarshalJSON() ([]byte, error) {
	type plain MessageView
	var body *string
	if !v.Retracted {
		body = &v.Body
	}
	return json.Marshal(struct {
		plain
		Body *string `json:"body"`
	}{plain: plain(v), Body: body})
}
