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
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

type ChannelView struct {
	CreatedNow    bool     `json:"-"`
	ID            guid.ID  `json:"id"`
	ProjectID     *guid.ID `json:"project_id,omitempty"`
	Name          string   `json:"name"`
	Topic         *string  `json:"topic,omitempty"`
	Kind          string   `json:"kind"`
	Visibility    string   `json:"visibility"`
	Unread        int64    `json:"unread"`
	MentionUnread int64    `json:"mention_unread"`
	Version       int      `json:"version"`
	Archived      bool     `json:"archived,omitempty"`
}

func channelView(row *model.Channel) ChannelView {
	return ChannelView{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Topic: row.Topic, Kind: row.Kind, Visibility: row.Visibility, Version: row.Version, Archived: row.ArchivedAt != nil}
}

type CreateChannelInput struct {
	Kind            string  `json:"kind,omitempty" enum:"CUSTOM,AGENT_DIRECT"`
	PeerPrincipalID guid.ID `json:"peer_principal_id,omitempty"`
	Name            string  `json:"name,omitempty" minLength:"1" maxLength:"40"`
	Topic           string  `json:"topic,omitempty" maxLength:"200"`
	Visibility      string  `json:"visibility,omitempty" enum:"INTERNAL,CLIENT_SHARED" default:"INTERNAL"`
}

func (m *Module) createChannelOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in CreateChannelInput) (ChannelView, error) {
	if in.Kind == "AGENT_DIRECT" {
		return m.createAgentDirectOn(ctx, tx, tenant, project, in.PeerPrincipalID)
	}
	var empty ChannelView
	if (in.Kind != "" && in.Kind != "CUSTOM") || in.PeerPrincipalID != 0 {
		return empty, ErrInvalidArgument
	}
	if err := checkTransaction(tx, tenant, project); err != nil {
		return empty, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 || utf8.RuneCountInString(in.Topic) > 200 || (in.Visibility != "INTERNAL" && in.Visibility != "CLIENT_SHARED") {
		return empty, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return empty, ErrForbidden
	}
	if m.policy == nil || m.ids == nil || m.publisher == nil || m.audit == nil || m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil {
		return empty, ErrUnavailable
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrForbidden
	}
	human, ok, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return empty, err
	}
	if !ok || human.ID != actor {
		return empty, ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrForbidden
	}
	permission := "channel_post"
	if in.Visibility == "CLIENT_SHARED" {
		permission = "channel_manage"
	}
	c, err := m.policy(ctx, tx, tenant, project, permission, actor)
	if err != nil {
		return empty, err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return empty, ErrForbidden
	}
	id, err := m.ids.Next()
	if err != nil {
		return empty, err
	}
	if id <= 0 {
		return empty, ErrUnavailable
	}
	now := m.now()
	row := model.Channel{ID: guid.ID(id), TenantID: tenant, ProjectID: &project, Kind: "CUSTOM", Name: name, Topic: &in.Topic, Visibility: in.Visibility, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
	if err = query.BindChannels(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return empty, err
	}
	if err = query.BindMembers(tx, m.names).WithContext(ctx).Create(&model.ChannelMember{ChannelID: row.ID, TenantID: tenant, PrincipalID: actor, MemberKind: "HUMAN", Role: "OWNER", Source: "EXPLICIT", JoinedAt: now, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}); err != nil {
		return empty, err
	}
	if err = m.channelEvent(ctx, tx, tenant, project, row.ID, "collab.channel_updated", permission); err != nil {
		return empty, err
	}
	return channelView(&row), nil
}
func (m *Module) channelEvent(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, event, permission string, target ...guid.ID) error {
	if m.publisher == nil || m.audit == nil {
		return ErrUnavailable
	}
	authority := "IAM:" + permission
	if permission == "CHANNEL_OWNER" || permission == "CHANNEL_SELF" {
		authority = permission
	}
	member := guid.ID(0)
	if len(target) == 1 {
		member = target[0]
	}
	if err := m.appendAudit(ctx, tx, tenant, project, channel, event, member, ProvisioningAudit{AuthoritySource: authority}); err != nil {
		return err
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: event, Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "channel", ID: channel}, SubjectVersion: 1, ExtraTopics: conversationTopics(tenant, channel, nil), Data: map[string]any{"channel_id": channel.String()}})
	return err
}

type ChannelMemberView struct {
	PrincipalID guid.ID   `json:"principal_id"`
	MemberKind  string    `json:"member_kind"`
	Role        string    `json:"role"`
	External    bool      `json:"external"`
	JoinedAt    time.Time `json:"joined_at"`
}
type MemberInput struct {
	External bool   `json:"external" doc:"Explicitly invited client. Requires a CLIENT_SHARED channel."`
	Role     string `json:"role,omitempty" enum:"MEMBER,OWNER"`
}

func (m *Module) putMemberOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, principal guid.ID, in MemberInput) (ChannelMemberView, error) {
	ctx = mutationContext(ctx)
	var empty ChannelMemberView
	if principal <= 0 || (in.Role != "" && in.Role != "MEMBER" && in.Role != "OWNER") {
		return empty, ErrInvalidArgument
	}
	// Lock all affected principals in ascending order before policy/channel locks.
	if m.principals.LockActiveRow == nil {
		return empty, ErrUnavailable
	}
	ids := []guid.ID{authn.PrincipalIDFromContext(ctx), principal}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		_, ok, err := m.principals.LockActiveRow(ctx, tx, id)
		if err != nil {
			return empty, err
		}
		if !ok {
			return empty, ErrMemberInactive
		}
	}
	ch, actor, err := m.authorizeChannel(ctx, tx, tenant, project, channel, "channel_read")
	if err != nil {
		return empty, err
	}
	if actor.External || ch.Kind != "CUSTOM" || (in.External && ch.Visibility != "CLIENT_SHARED") {
		return empty, ErrForbidden
	}
	if err = m.channelManagerOn(ctx, tx, tenant, project, actor); err != nil {
		return empty, err
	}
	active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrMemberInactive
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, principal)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, ErrMemberInactive
	}
	human, ok, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return empty, err
	}
	kind := "HUMAN"
	if !ok || human.ID != principal {
		if in.External || m.agents.ByPrincipalOn == nil {
			return empty, ErrMemberInactive
		}
		agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, principal)
		if err != nil {
			return empty, err
		}
		if !found || agent.PrincipalID != principal || agent.TenantID != tenant {
			return empty, ErrMemberInactive
		}
		kind = "AGENT"
	}
	cm := query.BindMembers(tx, m.names)
	request := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(principal))
	old, err := request.First()
	now := m.now()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return empty, err
	}
	role := in.Role
	if role == "" {
		role = "MEMBER"
		if old != nil {
			role = old.Role
		}
	}
	if role == "OWNER" && (in.External || kind != "HUMAN") {
		return empty, ErrInvalidArgument
	}
	if old != nil {
		// A client classification cannot be silently downgraded on reinvitation.
		if old.External && !in.External {
			return empty, ErrForbidden
		}
		if old.LeftAt == nil && old.External == in.External && old.Role == role {
			return ChannelMemberView{PrincipalID: principal, MemberKind: old.MemberKind, Role: old.Role, External: old.External, JoinedAt: old.JoinedAt}, nil
		}
		if old.LeftAt == nil && old.Role == "OWNER" && role != "OWNER" {
			owners, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.Role.Eq("OWNER"), cm.LeftAt.IsNull()).Count()
			if e != nil {
				return empty, e
			}
			if owners <= 1 {
				return empty, ErrLastOwner
			}
		}
		_, err = cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(principal)).Updates(map[string]any{"external": in.External, "left_at": nil, "joined_at": now, "updated_at": now, "member_kind": kind, "role": role})
		if err != nil {
			return empty, err
		}
	} else {
		err = cm.WithContext(ctx).Create(&model.ChannelMember{TenantID: tenant, ChannelID: channel, PrincipalID: principal, MemberKind: kind, Role: role, Source: "EXPLICIT", External: in.External, JoinedAt: now, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return empty, err
		}
	}
	permission := "channel_manage"
	if actor.Role == "OWNER" {
		permission = "CHANNEL_OWNER"
	}
	if err = m.channelEvent(ctx, tx, tenant, project, channel, "collab.member_updated", permission, principal); err != nil {
		return empty, err
	}
	return ChannelMemberView{PrincipalID: principal, MemberKind: kind, Role: role, External: in.External, JoinedAt: now}, nil
}
func (m *Module) listChannelsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, cursor string, limit int) (CursorPage[ChannelView], error) {
	return m.listChannelsStateOn(ctx, tx, tenant, project, cursor, limit, false)
}

func (m *Module) listChannelsStateOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, cursor string, limit int, archived bool) (CursorPage[ChannelView], error) {
	out := CursorPage[ChannelView]{Items: []ChannelView{}}
	if err := checkTransaction(tx, tenant, project); err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 {
		return out, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrForbidden
	}
	if m.policy == nil {
		return out, ErrUnavailable
	}
	c, err := m.policy(ctx, tx, tenant, project, "channel_read", actor)
	if err != nil {
		return out, err
	}
	internalAllowed := c.AllowsEntityRows(project) && (c.AllowAll || slices.Contains(c.ResourceIDs, project.String()) || slices.Contains(c.OwnerIDs, actor.String()))
	scope := cursorScope("channels", tenant.String(), project.String(), actor.String())
	if archived {
		scope += ":archived"
	}
	after, err := readCursor(cursor, scope)
	if err != nil {
		return out, err
	}
	q := query.BindChannels(tx, m.names)
	cm := query.BindMembers(tx, m.names)
	archiveCondition := q.ArchivedAt.IsNull()
	if archived {
		archiveCondition = q.ArchivedAt.IsNotNull()
	}
	req := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.In("PROJECT_DEFAULT", "CUSTOM"), archiveCondition, q.ID.Gt(guid.ID(after)))
	// Each side of the membership/visibility predicate is evaluated in SQL,
	// before the keyset limit, to avoid leaking internal channel existence.
	internal := cm.WithContext(ctx).Select(cm.ChannelID).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(actor), cm.LeftAt.IsNull(), cm.External.Is(false))
	external := cm.WithContext(ctx).Select(cm.ChannelID).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(actor), cm.LeftAt.IsNull(), cm.External.Is(true))
	shared := q.WithContext(ctx).Where(q.Visibility.Eq("CLIENT_SHARED")).Where(q.WithContext(ctx).Columns(q.ID).In(external))
	if internalAllowed {
		req = req.Where(q.WithContext(ctx).Where(q.WithContext(ctx).Columns(q.ID).In(internal)).Or(shared))
	} else {
		// Keep the existing denial for internal readers without channel_read.
		// The external exception requires a current explicit shared membership,
		// independently of this page's cursor position.
		eligible, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.In("PROJECT_DEFAULT", "CUSTOM"), archiveCondition).Where(shared).Count()
		if err != nil {
			return out, err
		}
		if eligible == 0 {
			return out, ErrForbidden
		}
		req = req.Where(shared)
	}
	rows, err := req.Order(q.ID).Limit(limit + 1).Find()
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		_, member, err := m.authorizeChannelState(ctx, tx, tenant, project, row.ID, "channel_read", archived, false)
		if err != nil {
			return out, err
		}
		v := channelView(row)
		msg := query.BindMessages(tx, m.names)
		unread := msg.WithContext(ctx).Where(msg.TenantID.Eq(tenant), msg.ChannelID.Eq(row.ID), msg.Seq.Gt(member.LastReadSeq))
		if member.External {
			unread = unread.Where(msg.Visibility.Eq("CHANNEL"))
		}
		v.Unread, err = unread.Count()
		if err != nil {
			return out, err
		}
		mentions := query.BindMentions(tx, m.names)
		v.MentionUnread, err = unread.Where(msg.WithContext(ctx).Columns(msg.ID).In(mentions.WithContext(ctx).Select(mentions.MessageID).Where(mentions.TenantID.Eq(tenant), mentions.ChannelID.Eq(row.ID), mentions.PrincipalID.Eq(actor)))).Count()
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	if out.HasMore {
		out.NextCursor = nextCursor(int64(rows[len(rows)-1].ID), scope)
	}
	return out, nil
}
