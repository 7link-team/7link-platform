package collab

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type HubActivityInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Kind     string `query:"kind" enum:"MENTIONS,REPLIES" default:"MENTIONS"`
	Cursor   string `query:"cursor" maxLength:"512"`
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type HubActivity struct {
	Message MessageView `json:"message"`
	Channel ChannelView `json:"channel"`
}

func (m *Module) hubActivityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in HubActivityInput) (CursorPage[HubActivity], error) {
	out := CursorPage[HubActivity]{Items: []HubActivity{}}
	actor := authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || tenant != authn.TenantIDFromContext(ctx) || in.Limit < 1 || in.Limit > 100 || (in.Kind != "MENTIONS" && in.Kind != "REPLIES") {
		return out, ErrInvalidArgument
	}
	if _, err := m.directParticipantOn(ctx, tx, tenant, actor); err != nil {
		return out, err
	}
	scope := cursorScope("hub-activity", tenant.String(), actor.String(), in.Kind)
	before, err := readCursor(in.Cursor, scope)
	if err != nil {
		return out, err
	}
	q := query.BindMessages(tx, m.names)
	c := query.BindChannels(tx, m.names)
	members := query.BindMembers(tx, m.names)
	membership := members.WithContext(ctx).Select(members.ChannelID).Where(members.TenantID.Eq(tenant), members.PrincipalID.Eq(actor), members.LeftAt.IsNull())
	for scanned := 0; scanned < 10000; scanned += 100 {
		read := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.WithdrawnAt.IsNull(), q.AuthorID.Neq(actor), q.WithContext(ctx).Columns(q.ChannelID).In(membership))
		if before > 0 {
			read = read.Where(q.ID.Lt(guid.ID(before)))
		}
		if in.Kind == "MENTIONS" {
			mentions := query.BindMentions(tx, m.names)
			read = read.Where(q.WithContext(ctx).Columns(q.ID).In(mentions.WithContext(ctx).Select(mentions.MessageID).Where(mentions.TenantID.Eq(tenant), mentions.PrincipalID.Eq(actor))))
		} else {
			own := query.BindMessages(tx, m.names)
			parents := own.WithContext(ctx).Select(own.ID).Where(own.TenantID.Eq(tenant), own.AuthorID.Eq(actor), own.WithdrawnAt.IsNull())
			read = read.Where(q.WithContext(ctx).Columns(q.ReplyToMessageID).In(parents))
		}
		rows, e := read.Order(q.ID.Desc()).Limit(100).Find()
		if e != nil {
			return out, e
		}
		for _, row := range rows {
			before = int64(row.ID)
			ch, e := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(row.ChannelID)).First()
			if e != nil {
				return out, e
			}
			var member *model.ChannelMember
			if ch.ProjectID == nil {
				_, e = m.directAccessOn(ctx, tx, tenant, ch.ID, false)
			} else {
				_, member, e = m.authorizeChannelState(ctx, tx, tenant, *ch.ProjectID, ch.ID, "channel_read", true, false)
			}
			if errors.Is(e, ErrForbidden) || errors.Is(e, ErrNotFound) || errors.Is(e, ErrMemberInactive) {
				continue
			}
			if e != nil {
				return out, e
			}
			if member != nil && member.External {
				if row.Visibility != "CHANNEL" || ch.Visibility != "CLIENT_SHARED" {
					continue
				}
				if row.ReplyToMessageID != nil {
					parent, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(ch.ID), q.ID.Eq(*row.ReplyToMessageID), q.Visibility.Eq("CHANNEL"), q.WithdrawnAt.IsNull()).First()
					if errors.Is(e, gorm.ErrRecordNotFound) {
						continue
					}
					if e != nil {
						return out, e
					}
					_ = parent
				}
			}
			if len(out.Items) == in.Limit {
				out.HasMore = true
				out.NextCursor = nextCursor(int64(out.Items[len(out.Items)-1].Message.ID), scope)
				return out, nil
			}
			out.Items = append(out.Items, HubActivity{Message: messageView(row), Channel: channelView(ch)})
		}
		if len(rows) < 100 {
			return out, nil
		}
	}
	return CursorPage[HubActivity]{}, ErrUnavailable
}

func (m *Module) registerHubActivityHTTP(api huma.API) {
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{OperationID: "conversation.activity.list", Method: http.MethodGet, Path: "/conversations/activity", Summary: "Read your currently visible mentions or replies across project and direct conversations", Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}, func(ctx context.Context, in *HubActivityInput) (*respx.Body[CursorPage[HubActivity]], error) {
		tenant, err := guid.RequireID(in.TenantID)
		if err != nil {
			return nil, conversationHTTPError(ctx, ErrInvalidArgument)
		}
		db, err := m.database()
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		var out CursorPage[HubActivity]
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.hubActivityOn(ctx, tx, tenant, *in); return e })
		if err != nil {
			return nil, conversationHTTPError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	})
}
