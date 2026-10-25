package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"unicode/utf8"
)

// A DIRECT belongs to its two fixed participants, never to a selected project.
func (m *Module) humanOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (identity.Principal, error) {
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil {
		return identity.Principal{}, ErrUnavailable
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, id)
	if err != nil {
		return identity.Principal{}, err
	}
	if !active || p.ID != id {
		return identity.Principal{}, ErrMemberInactive
	}
	h, human, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return identity.Principal{}, err
	}
	if !human || h.ID != id {
		return identity.Principal{}, ErrForbidden
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, id)
	if err != nil {
		return identity.Principal{}, err
	}
	if !active {
		return identity.Principal{}, ErrMemberInactive
	}
	return p, nil
}

type DirectChannelResult struct {
	ChannelView
	Created bool `json:"created"`
}

// Registered Agents may participate in DIRECT, but arbitrary service accounts
// cannot. Current principal and tenant membership are checked before the kind.
func (m *Module) directParticipantOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (string, error) {
	if m.principals.LockActiveRow == nil || m.principals.HumanByLogin == nil || m.members == nil {
		return "", ErrUnavailable
	}
	p, active, err := m.principals.LockActiveRow(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if !active || p.ID != id {
		return "", ErrMemberInactive
	}
	active, err = m.members.IsMemberActiveOn(ctx, tx, tenant, id)
	if err != nil {
		return "", err
	}
	if !active {
		return "", ErrMemberInactive
	}
	h, human, err := m.principals.HumanByLogin(ctx, tx, p.LoginName)
	if err != nil {
		return "", err
	}
	if human {
		if h.ID != id {
			return "", ErrForbidden
		}
		return "HUMAN", nil
	}
	if m.agents.ByPrincipalOn == nil {
		return "", ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, id)
	if err != nil {
		return "", err
	}
	if !found || a.ID <= 0 || a.TenantID != tenant || a.PrincipalID != id {
		return "", ErrForbidden
	}
	if a.State != "ACTIVE" {
		return "", ErrMemberInactive
	}
	return "AGENT", nil
}

func (m *Module) createDirectOn(ctx context.Context, tx *gorm.DB, tenant, peer guid.ID) (DirectChannelResult, error) {
	var out DirectChannelResult
	actor := authn.PrincipalIDFromContext(ctx)
	if err := checkTransaction(tx, tenant, peer); err != nil {
		return out, err
	}
	if actor <= 0 || actor == peer || authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrInvalidArgument
	}
	ids := []guid.ID{actor, peer}
	slices.Sort(ids)
	kinds := map[guid.ID]string{}
	for _, id := range ids {
		kind, err := m.directParticipantOn(ctx, tx, tenant, id)
		if err != nil {
			return out, err
		}
		kinds[id] = kind
	}
	if kinds[actor] != "HUMAN" {
		return out, ErrForbidden
	}
	if kinds[peer] == "AGENT" {
		if m.tenantPolicy == nil {
			return out, ErrUnavailable
		}
		a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, peer)
		if err != nil {
			return out, err
		}
		if !found {
			return out, ErrMemberInactive
		}
		constraint, err := m.tenantPolicy(ctx, tx, tenant, "agent_read", actor)
		if err != nil {
			return out, err
		}
		if !constraint.AllowAll && !slices.Contains(constraint.ResourceIDs, a.ID.String()) && !slices.Contains(constraint.OwnerIDs, a.OwnerPrincipalID.String()) {
			return out, ErrForbidden
		}
	}
	pair := fmt.Sprintf("%s:%s", ids[0], ids[1])
	c := query.BindChannels(tx, m.names)
	row, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.Kind.Eq("DIRECT"), c.ProjectID.IsNull(), c.DirectPairKey.Eq(pair)).First()
	if err == nil {
		// Never replace historical conversations or silently repair participants.
		cm := query.BindMembers(tx, m.names)
		members, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(row.ID)).Order(cm.PrincipalID).Find()
		if e != nil {
			return out, e
		}
		if len(members) != 2 {
			return out, ErrUnavailable
		}
		for i, member := range members {
			if member.PrincipalID != ids[i] || member.MemberKind != kinds[ids[i]] || member.External || member.LeftAt != nil {
				return out, ErrForbidden
			}
		}
		return DirectChannelResult{ChannelView: channelView(row)}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	if m.ids == nil || m.audit == nil || m.publisher == nil {
		return out, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	now := m.now()
	row = &model.Channel{ID: guid.ID(id), TenantID: tenant, Kind: "DIRECT", Visibility: "INTERNAL", Name: pair, DirectPairKey: &pair, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
	if err = c.WithContext(ctx).Create(row); err != nil {
		return out, err
	}
	for _, participant := range ids {
		if err = query.BindMembers(tx, m.names).WithContext(ctx).Create(&model.ChannelMember{ChannelID: row.ID, TenantID: tenant, PrincipalID: participant, MemberKind: kinds[participant], Role: "MEMBER", Source: "EXPLICIT", JoinedAt: now, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}); err != nil {
			return out, err
		}
	}
	if err = m.directEventOn(ctx, tx, tenant, row.ID, 0, "collab.channel_updated", actor); err != nil {
		return out, err
	}
	return DirectChannelResult{ChannelView: channelView(row), Created: true}, nil
}

func (m *Module) directEventOn(ctx context.Context, tx *gorm.DB, tenant, channel, message guid.ID, event string, actor guid.ID) error {
	if m.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	const authority = "DIRECT_PARTICIPANT"
	if err := m.appendAudit(ctx, tx, tenant, 0, channel, event, 0, ProvisioningAudit{AuthoritySource: authority}); err != nil {
		return err
	}
	subject := events.Subject{Kind: "channel", ID: channel}
	if message > 0 {
		subject = events.Subject{Kind: "message", ID: message}
	}
	data := map[string]any{"channel_id": channel.String()}
	if event == "collab.read_updated" {
		subject = events.Subject{Kind: "inbox", ID: actor}
		data = map[string]any{}
	}
	kind := "HUMAN"
	if event == "collab.message_posted" {
		cm := query.BindMembers(tx, m.names)
		member, err := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(actor)).First()
		if err != nil {
			return err
		}
		kind = member.MemberKind
		data["channel_kind"] = "DIRECT"
	}
	_, err := m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, Type: event, Actor: events.Actor{Kind: kind, ID: actor, AuthoritySource: authority}, Subject: subject, SubjectVersion: 1, Data: data})
	return err
}

func (m *Module) directAccessOn(ctx context.Context, tx *gorm.DB, tenant, channel guid.ID, write bool) (*model.Channel, error) {
	if err := checkTransaction(tx, tenant, channel); err != nil {
		return nil, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrForbidden
	}
	cm := query.BindMembers(tx, m.names)
	members, err := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel)).Order(cm.PrincipalID).Find()
	if err != nil {
		return nil, err
	}
	if len(members) != 2 {
		return nil, ErrNotFound
	}
	participates := false
	humans := 0
	for _, member := range members {
		if (member.MemberKind != "HUMAN" && member.MemberKind != "AGENT") || member.External {
			return nil, ErrNotFound
		}
		if member.MemberKind == "HUMAN" {
			humans++
		}
		if member.PrincipalID == actor {
			participates = member.LeftAt == nil
		}
	}
	if !participates || humans == 0 {
		return nil, ErrNotFound
	}
	// Lock participants in the same order as creation. Reading history requires
	// current authority only for its reader; sending checks both participants.
	for _, member := range members {
		if write || member.PrincipalID == actor {
			if member.LeftAt != nil {
				return nil, ErrMemberInactive
			}
			kind, e := m.directParticipantOn(ctx, tx, tenant, member.PrincipalID)
			if e != nil {
				return nil, e
			}
			if kind != member.MemberKind {
				return nil, ErrForbidden
			}
		}
	}
	c := query.BindChannels(tx, m.names)
	row, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ID.Eq(channel), c.Kind.Eq("DIRECT"), c.ProjectID.IsNull(), c.Visibility.Eq("INTERNAL")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if write && row.ArchivedAt != nil {
		return nil, ErrForbidden
	}
	return row, nil
}

type DirectTextInput struct {
	Body    string `json:"body" minLength:"1" maxLength:"8000"`
	SeenSeq *int64 `json:"seen_seq,omitempty" minimum:"0" doc:"Required for Agent sends; stale context interrupts the command."`
}

func (m *Module) postDirectOn(ctx context.Context, tx *gorm.DB, tenant, channel guid.ID, in DirectTextInput) (MessageView, error) {
	var out MessageView
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || !utf8.ValidString(in.Body) || utf8.RuneCountInString(in.Body) > 8000 {
		return out, ErrInvalidArgument
	}
	row, err := m.directAccessOn(ctx, tx, tenant, channel, true)
	if err != nil {
		return out, err
	}
	memberQuery := query.BindMembers(tx, m.names)
	sender, err := memberQuery.WithContext(ctx).Where(memberQuery.TenantID.Eq(tenant), memberQuery.ChannelID.Eq(channel), memberQuery.PrincipalID.Eq(authn.PrincipalIDFromContext(ctx))).First()
	if err != nil {
		return out, err
	}
	if sender.MemberKind == "AGENT" {
		if err = m.checkSeenOn(ctx, tx, tenant, channel, sender.PrincipalID, false, row.LastSeq, PostMessageInput{SeenSeq: in.SeenSeq}); err != nil {
			return out, err
		}
	}
	if m.ids == nil {
		return out, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return out, err
	}
	if id <= 0 {
		return out, ErrUnavailable
	}
	actor, now := authn.PrincipalIDFromContext(ctx), m.now()
	message := &model.Message{ID: guid.ID(id), TenantID: tenant, ChannelID: channel, Seq: row.LastSeq + 1, ContextSeq: row.LastSeq + 1, AuthorID: actor, Body: in.Body, Visibility: "CHANNEL", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err = query.BindMessages(tx, m.names).WithContext(ctx).Create(message); err != nil {
		return out, err
	}
	c := query.BindChannels(tx, m.names)
	changed, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(channel), c.LastSeq.Eq(row.LastSeq)).Updates(map[string]any{"last_seq": message.Seq, "last_message_at": now, "updated_at": now, "version": row.Version + 1})
	if err != nil {
		return out, err
	}
	if changed.RowsAffected != 1 {
		return out, ErrUnavailable
	}
	if err = m.directEventOn(ctx, tx, tenant, channel, message.ID, "collab.message_posted", actor); err != nil {
		return out, err
	}
	cm := query.BindMembers(tx, m.names)
	peer, err := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Neq(actor)).First()
	if err != nil {
		return out, err
	}
	if err = query.BindMentions(tx, m.names).WithContext(ctx).Create(&model.MessageMention{TenantID: tenant, ChannelID: channel, MessageID: message.ID, PrincipalID: peer.PrincipalID}); err != nil {
		return out, err
	}
	if peer.MemberKind == "AGENT" {
		if m.messageObserver == nil {
			return out, ErrUnavailable
		}
		if err = m.messageObserver(ctx, tx, MessageReference{TenantID: tenant, AgentPrincipalID: peer.PrincipalID, MessageID: message.ID, ChannelID: channel, Seq: message.Seq, Kind: "MENTION"}); err != nil {
			return out, err
		}
	}
	out = messageView(message)
	out.Mentions = []guid.ID{peer.PrincipalID}
	return out, nil
}

type DirectReadInput struct {
	ConversationChannelInput
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

type ConversationHubInput struct {
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Search string `query:"search" maxLength:"200"`
}

// hubOn projects only the caller's actual memberships. Project permissions
// are checked before the visible page boundary; owner status grants no DM reads.
func (m *Module) hubOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in ConversationHubInput) (CursorPage[ChannelView], error) {
	out := CursorPage[ChannelView]{Items: []ChannelView{}}
	actor := authn.PrincipalIDFromContext(ctx)
	if err := checkTransaction(tx, tenant, actor); err != nil {
		return out, err
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return out, ErrForbidden
	}
	if in.Limit < 1 || in.Limit > 100 || !utf8.ValidString(in.Search) || utf8.RuneCountInString(in.Search) > 200 {
		return out, ErrInvalidArgument
	}
	if _, err := m.humanOn(ctx, tx, tenant, actor); err != nil {
		return out, err
	}
	scope := cursorScope("conversation-hub", tenant.String(), actor.String(), in.Search)
	after, err := readCursor(in.Cursor, scope)
	if err != nil {
		return out, err
	}
	c, cm := query.BindChannels(tx, m.names), query.BindMembers(tx, m.names)
	membership := cm.WithContext(ctx).Select(cm.ChannelID).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(actor), cm.LeftAt.IsNull())
	for scanned := 0; scanned < 1000; scanned += 100 {
		r := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Gt(guid.ID(after)), c.Kind.In("DIRECT", "PROJECT_DEFAULT", "CUSTOM")).Where(c.WithContext(ctx).Columns(c.ID).In(membership))
		if in.Search != "" {
			r = r.Where(c.Name.Like("%" + in.Search + "%"))
		}
		rows, e := r.Order(c.ID).Limit(100).Find()
		if e != nil {
			return out, e
		}
		for _, row := range rows {
			after = int64(row.ID)
			if row.ProjectID == nil {
				_, e = m.directAccessOn(ctx, tx, tenant, row.ID, false)
			} else if row.ArchivedAt == nil {
				_, _, e = m.authorizeChannel(ctx, tx, tenant, *row.ProjectID, row.ID, "channel_read")
			} else {
				continue
			}
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrMemberInactive) {
				continue
			}
			if e != nil {
				return out, e
			}
			if len(out.Items) == in.Limit {
				out.HasMore = true
				out.NextCursor = nextCursor(int64(out.Items[len(out.Items)-1].ID), scope)
				return out, nil
			}
			member, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(row.ID), cm.PrincipalID.Eq(actor)).First()
			if e != nil {
				return out, e
			}
			v := channelView(row)
			msg, mentions := query.BindMessages(tx, m.names), query.BindMentions(tx, m.names)
			unread := msg.WithContext(ctx).Where(msg.TenantID.Eq(tenant), msg.ChannelID.Eq(row.ID), msg.Seq.Gt(member.LastReadSeq), msg.WithdrawnAt.IsNull())
			if member.External {
				unread = unread.Where(msg.Visibility.Eq("CHANNEL"))
			}
			v.Unread, e = unread.Count()
			if e != nil {
				return out, e
			}
			v.MentionUnread, e = unread.Where(msg.WithContext(ctx).Columns(msg.ID).In(mentions.WithContext(ctx).Select(mentions.MessageID).Where(mentions.TenantID.Eq(tenant), mentions.ChannelID.Eq(row.ID), mentions.PrincipalID.Eq(actor)))).Count()
			if e != nil {
				return out, e
			}
			out.Items = append(out.Items, v)
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
	return CursorPage[ChannelView]{}, ErrUnavailable
}

type directCreateInput struct {
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           struct {
		PeerPrincipalID guid.ID `json:"peer_principal_id"`
	}
}
type directPostInput struct {
	ConversationChannelInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           DirectTextInput
}
type directCreateOutput struct {
	Status   int
	Replayed string `header:"Idempotent-Replayed"`
	Body     respx.Envelope[DirectChannelResult]
}

func (m *Module) registerConversationsHTTP(api huma.API) error {
	m.registerHubActivityHTTP(api)
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{401, 403, 404, 409, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("conversation.hub.list", "GET", "/conversations", "List your authorized project channels and private conversations", 200), func(ctx context.Context, in *ConversationHubInput) (*respx.Body[CursorPage[ChannelView]], error) {
		db, err := m.database()
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		var page CursorPage[ChannelView]
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var e error
			page, e = m.hubOn(ctx, tx, authn.TenantIDFromContext(ctx), *in)
			return e
		})
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		return respx.OK(ctx, page), nil
	})
	authz.RegisterTenantMember(m.registrar, api, op("channel.direct.create", "POST", "/direct-channels", "Open a private conversation with an active Human or registered Agent", 201), func(ctx context.Context, in *directCreateInput) (*directCreateOutput, error) {
		tenant := authn.TenantIDFromContext(ctx)
		result, err := conversationCommand(ctx, m, in.IdempotencyKey, "channel.direct.create", "/direct-channels", map[string]string{"tenant_id": tenant.String()}, in.Body, 201, func(tx *gorm.DB) (DirectChannelResult, error) {
			return m.createDirectOn(ctx, tx, tenant, in.Body.PeerPrincipalID)
		}, func(tx *gorm.DB, v DirectChannelResult) error {
			current, e := m.createDirectOn(ctx, tx, tenant, in.Body.PeerPrincipalID)
			if e == nil && current.ID != v.ID {
				return ErrForbidden
			}
			return e
		})
		if err != nil {
			return nil, err
		}
		status := 200
		if result.Body.Data.Created {
			status = 201
		}
		return &directCreateOutput{Status: status, Replayed: result.Replayed, Body: result.Body}, nil
	})
	authz.RegisterTenantMember(m.registrar, api, op("channel.direct.messages", "GET", "/direct-channels/{channel_id}/messages", "Read your private conversation history", 200), m.readDirectHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("channel.direct.read-marker", "PUT", "/direct-channels/{channel_id}/read-marker", "Advance your private conversation read marker", 200), func(ctx context.Context, in *readInput) (*respx.Body[int64], error) {
		channel, err := guid.RequireID(in.ChannelID)
		if err != nil || in.Body.Seq < 0 {
			return nil, conversationHTTPError(ctx, ErrInvalidArgument)
		}
		db, err := m.database()
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		var seq int64
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var e error
			seq, e = m.directReadMarkerOn(ctx, tx, authn.TenantIDFromContext(ctx), channel, in.Body.Seq)
			return e
		})
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		return respx.OK(ctx, seq), nil
	})
	authz.RegisterTenantMember(m.registrar, api, op("channel.direct.post", "POST", "/direct-channels/{channel_id}/messages", "Send private text to an active Human or registered Agent", 201), func(ctx context.Context, in *directPostInput) (*commandOutput[MessageView], error) {
		channel, err := guid.RequireID(in.ChannelID)
		if err != nil {
			return nil, conversationHTTPError(ctx, ErrInvalidArgument)
		}
		tenant := authn.TenantIDFromContext(ctx)
		return conversationCommand(ctx, m, in.IdempotencyKey, "channel.direct.post", "/direct-channels/{channel_id}/messages", map[string]string{"tenant_id": tenant.String(), "channel_id": in.ChannelID}, in.Body, 201, func(tx *gorm.DB) (MessageView, error) { return m.postDirectOn(ctx, tx, tenant, channel, in.Body) }, func(tx *gorm.DB, v MessageView) error {
			_, e := m.directAccessOn(ctx, tx, tenant, channel, true)
			return e
		})
	})
	if !m.registrar.IsDeclarationOnly() {
		for _, path := range []string{"/tenants/{tenant_id}/direct-channels", "/tenants/{tenant_id}/direct-channels/{channel_id}/messages"} {
			if err := authz.RegisterRequestContract(api, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Module) directReadMarkerOn(ctx context.Context, tx *gorm.DB, tenant, channel guid.ID, seq int64) (int64, error) {
	if _, err := m.directAccessOn(ctx, tx, tenant, channel, false); err != nil {
		return 0, err
	}
	if seq < 0 {
		return 0, ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	cm := query.BindMembers(tx, m.names)
	member, err := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(actor)).First()
	if err != nil {
		return 0, err
	}
	if seq <= member.LastReadSeq {
		return member.LastReadSeq, nil
	}
	q := query.BindMessages(tx, m.names)
	n, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.Seq.Eq(seq), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).Count()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrNotFound
	}
	if _, err = cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.ChannelID.Eq(channel), cm.PrincipalID.Eq(actor), cm.LastReadSeq.Lt(seq)).Updates(map[string]any{"last_read_seq": seq, "updated_at": m.now()}); err != nil {
		return 0, err
	}
	return seq, m.directEventOn(ctx, tx, tenant, channel, 0, "collab.read_updated", actor)
}

func (m *Module) readDirectHTTP(ctx context.Context, in *DirectReadInput) (*respx.Body[CursorPage[MessageView]], error) {
	channel, err := guid.RequireID(in.ChannelID)
	if err != nil || in.Limit < 1 || in.Limit > 100 {
		return nil, conversationHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	scope := cursorScope("direct-messages", tenant.String(), actor.String(), channel.String())
	before, err := readCursor(in.Cursor, scope)
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	db, err := m.database()
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	page := CursorPage[MessageView]{Items: []MessageView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.directAccessOn(ctx, tx, tenant, channel, false); e != nil {
			return e
		}
		q := query.BindMessages(tx, m.names)
		r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.Visibility.Eq("CHANNEL"))
		if before > 0 {
			r = r.Where(q.Seq.Lt(before))
		}
		rows, e := r.Order(q.Seq.Desc()).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			page.HasMore = true
			rows = rows[:in.Limit]
			page.NextCursor = nextCursor(rows[len(rows)-1].Seq, scope)
		}
		for _, row := range rows {
			page.Items = append(page.Items, messageView(row))
		}
		return nil
	})
	if err != nil {
		return nil, conversationHTTPError(ctx, err)
	}
	return respx.OK(ctx, page), nil
}

// ClientProjectAccess reuses explicit CLIENT_SHARED membership, active IAM Human
// identity and current project channel permission. It grants no internal reads.
type ClientProjectAccess interface {
	ChannelOn(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, bool, error)
	ProjectsOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) ([]guid.ID, error)
	IsClientOn(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	IsExternalOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
}
type clientAccess struct{ m *Module }

func (m *Module) provideDirectConversations(s *module.Services) error {
	if err := module.Provide[AgentDirectReader](s, AgentDirectReader{ChannelsOn: m.agentDirectChannelsOn, MessagesOn: m.agentDirectMessagesOn}); err != nil {
		return err
	}
	return module.Provide[ClientProjectAccess](s, clientAccess{m})
}

// AgentDirectReader is consumed only after the caller authorizes current human
// ownership or agent_workspace_read. It never exposes human-only conversations.
type AgentDirectReader struct {
	ChannelsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int) ([]ChannelView, error)
	MessagesOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, int) ([]MessageView, error)
}

func (m *Module) agentDirectChannelsOn(ctx context.Context, tx *gorm.DB, tenant, agent, after guid.ID, limit int) ([]ChannelView, error) {
	if err := checkTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 || after < 0 {
		return nil, ErrInvalidArgument
	}
	c, cm := query.BindChannels(tx, m.names), query.BindMembers(tx, m.names)
	membership := cm.WithContext(ctx).Select(cm.ChannelID).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(agent), cm.MemberKind.Eq("AGENT"), cm.External.Is(false), cm.LeftAt.IsNull())
	items := make([]ChannelView, 0, limit)
	for scanned := 0; scanned < 10000; scanned += 100 {
		rows, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.Kind.In("DIRECT", "AGENT_DIRECT"), c.Visibility.Eq("INTERNAL"), c.ArchivedAt.IsNull(), c.ID.Gt(after)).Where(c.WithContext(ctx).Columns(c.ID).In(membership)).Order(c.ID).Limit(100).Find()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			after = row.ID
			if row.ProjectID != nil {
				if err := m.inspectAgentProjectOn(ctx, tx, tenant, *row.ProjectID); errors.Is(err, ErrForbidden) {
					continue
				} else if err != nil {
					return nil, err
				}
			}
			items = append(items, channelView(row))
			if len(items) == limit {
				return items, nil
			}
		}
		if len(rows) < 100 {
			return items, nil
		}
	}
	return nil, ErrUnavailable
}

func (m *Module) agentDirectMessagesOn(ctx context.Context, tx *gorm.DB, tenant, agent, channel, before guid.ID, limit int) ([]MessageView, error) {
	if err := checkTransaction(tx, tenant, agent); err != nil {
		return nil, err
	}
	if channel <= 0 || before < 0 || limit < 1 || limit > 101 {
		return nil, ErrInvalidArgument
	}
	c, cm := query.BindChannels(tx, m.names), query.BindMembers(tx, m.names)
	membership := cm.WithContext(ctx).Select(cm.ChannelID).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(agent), cm.MemberKind.Eq("AGENT"), cm.External.Is(false), cm.LeftAt.IsNull())
	selected, err := c.WithContext(ctx).Where(c.ID.Eq(channel), c.TenantID.Eq(tenant), c.Kind.In("DIRECT", "AGENT_DIRECT"), c.Visibility.Eq("INTERNAL"), c.ArchivedAt.IsNull()).Where(c.WithContext(ctx).Columns(c.ID).In(membership)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if selected.ProjectID != nil {
		if err = m.inspectAgentProjectOn(ctx, tx, tenant, *selected.ProjectID); err != nil {
			return nil, err
		}
	}
	q := query.BindMessages(tx, m.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.WithdrawnAt.IsNull())
	if before > 0 {
		base = base.Where(q.ID.Lt(before))
	}
	rows, err := base.Order(q.ID.Desc()).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	items := make([]MessageView, 0, len(rows))
	for _, row := range rows {
		items = append(items, messageView(row))
	}
	return items, nil
}
func (a clientAccess) IsClientOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) (bool, error) {
	m := a.m
	if tx == nil || m == nil || m.members == nil || m.principals.ActiveHumanByID == nil || m.policy == nil {
		return false, ErrUnavailable
	}
	human, active, e := m.principals.ActiveHumanByID(ctx, tx, principal)
	if e != nil {
		return false, e
	}
	if !active || human.ID != principal {
		return false, nil
	}
	active, e = m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	if e != nil || !active {
		return false, e
	}
	c := query.BindChannels(tx, m.names)
	cm := query.BindMembers(tx, m.names)
	channels := c.WithContext(ctx).Select(c.ID).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.Visibility.Eq("CLIENT_SHARED"), c.ArchivedAt.IsNull())
	row, e := cm.WithContext(ctx).Where(cm.TenantID.Eq(tenant), cm.PrincipalID.Eq(principal), cm.External.Is(true), cm.LeftAt.IsNull()).Where(cm.WithContext(ctx).Columns(cm.ChannelID).In(channels)).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	_ = row
	constraint, e := m.policy(ctx, tx, tenant, project, "channel_read", principal)
	if e != nil {
		return false, e
	}
	return constraint.AllowsEntityRows(project) && (constraint.AllowAll || containsClientProject(constraint.ResourceIDs, project.String()) || containsClientProject(constraint.OwnerIDs, principal.String())), nil
}
func containsClientProject(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// External classification survives channel revocation: removing an invitation
// must never turn a client into an internal tenant reader.
func (a clientAccess) IsExternalOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) (bool, error) {
	if a.m == nil || tx == nil {
		return false, ErrUnavailable
	}
	q := query.BindMembers(tx, a.m.names)
	req := q.WithContext(ctx).Where(q.PrincipalID.Eq(principal), q.External.Is(true))
	if tenant > 0 {
		req = req.Where(q.TenantID.Eq(tenant))
	}
	r, e := req.First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return r != nil, e
}

// ProjectsOn starts from explicit active shared-channel membership, never tenant-wide project grants.
func (a clientAccess) ProjectsOn(ctx context.Context, tx *gorm.DB, tenant, actor, after guid.ID, limit int) ([]guid.ID, error) {
	if a.m == nil || tx == nil {
		return nil, ErrUnavailable
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalidArgument
	}
	c := query.BindChannels(tx, a.m.names)
	m := query.BindMembers(tx, a.m.names)
	members := m.WithContext(ctx).Select(m.ChannelID).Where(m.TenantID.Eq(tenant), m.PrincipalID.Eq(actor), m.External.Is(true), m.LeftAt.IsNull())
	rows, e := c.WithContext(ctx).Distinct(c.ProjectID).Where(c.TenantID.Eq(tenant), c.Visibility.Eq("CLIENT_SHARED"), c.ArchivedAt.IsNull(), c.ProjectID.Gt(after)).Where(c.WithContext(ctx).Columns(c.ID).In(members)).Order(c.ProjectID).Limit(limit).Find()
	if e != nil {
		return nil, e
	}
	out := make([]guid.ID, 0, len(rows))
	for _, r := range rows {
		if r.ProjectID != nil {
			out = append(out, *r.ProjectID)
		}
	}
	return out, nil
}

func (a clientAccess) ChannelOn(ctx context.Context, tx *gorm.DB, id, actor guid.ID) (guid.ID, bool, error) {
	c := query.BindChannels(tx, a.m.names)
	row, e := c.WithContext(ctx).Where(c.ID.Eq(id), c.ArchivedAt.IsNull()).First()
	if e != nil {
		return 0, false, e
	}
	if row.ProjectID == nil {
		return row.TenantID, false, nil
	}
	m := query.BindMembers(tx, a.m.names)
	_, e = m.WithContext(ctx).Where(m.TenantID.Eq(row.TenantID), m.ChannelID.Eq(id), m.PrincipalID.Eq(actor), m.External.Is(true), m.LeftAt.IsNull()).First()
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return row.TenantID, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	return row.TenantID, row.Visibility == "CLIENT_SHARED", nil
}
