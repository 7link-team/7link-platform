package agents

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type AgentReadInput struct {
	AgentPath
	Cursor string `query:"cursor" maxLength:"512"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}

type AgentDirectMessagesInput struct {
	AgentReadInput
	ChannelID string `path:"channel_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

func (m *Module) registerDirectMessagesHTTP(api huma.API) {
	op := func(id, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodGet, Path: agentPath + path, Summary: summary, Tags: []string{"tenants"}, Errors: []int{401, 403, 404, 422, 500, 503}}
	}
	authz.RegisterTenantMember(m.registrar, api, op("agent_work.list", "/work", "Read current work assigned to an authorized Agent"), m.selectedWorkHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_direct.list", "/direct-channels", "Inspect an authorized Agent's direct conversations read only"), m.agentDirectChannelsHTTP)
	authz.RegisterTenantMember(m.registrar, api, op("agent_direct.messages", "/direct-channels/{channel_id}/messages", "Inspect authorized Agent direct message content read only"), m.agentDirectMessagesHTTP)
}

func (m *Module) selectedWorkHTTP(ctx context.Context, in *AgentReadInput) (*ProtocolBody[delivery.PersonalWorkPage], error) {
	tenant, agent, err := pathIDs(ctx, in.AgentPath)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (delivery.PersonalWorkPage, error) {
		if _, e := m.authorize(ctx, tx, tenant, agent, "agent_workspace_read", true); e != nil {
			return delivery.PersonalWorkPage{}, e
		}
		if m.selectedWork.ListOn == nil {
			return delivery.PersonalWorkPage{}, ErrUnavailable
		}
		page, e := m.selectedWork.ListOn(ctx, tx, tenant, agent, in.Cursor, in.Limit)
		switch {
		case errors.Is(e, delivery.ErrInvalidArgument):
			return page, ErrInvalid
		case errors.Is(e, delivery.ErrWorkForbidden):
			return page, ErrForbidden
		case errors.Is(e, delivery.ErrUnavailable):
			return page, ErrUnavailable
		default:
			return page, e
		}
	})
}

func directPosition(raw string) (guid.ID, error) {
	if raw == "" {
		return 0, nil
	}
	id, err := guid.RequireID(raw)
	if err != nil {
		return 0, ErrInvalid
	}
	return id, nil
}

func directReadError(err error) error {
	if errors.Is(err, collab.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, collab.ErrInvalidArgument) {
		return ErrInvalid
	}
	return err
}

func (m *Module) agentDirectChannelsHTTP(ctx context.Context, in *AgentReadInput) (*ProtocolBody[Page[collab.ChannelView]], error) {
	tenant, agent, err := pathIDs(ctx, in.AgentPath)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	after, err := directPosition(in.Cursor)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Page[collab.ChannelView], error) {
		page := Page[collab.ChannelView]{Items: []collab.ChannelView{}, Next: []Next{}}
		if _, e := m.authorize(ctx, tx, tenant, agent, "agent_workspace_read", true); e != nil {
			return page, e
		}
		if in.Limit < 1 || in.Limit > 100 {
			return page, ErrInvalid
		}
		if m.directReader.ChannelsOn == nil {
			return page, ErrUnavailable
		}
		items, e := m.directReader.ChannelsOn(ctx, tx, tenant, agent, after, in.Limit+1)
		if e != nil {
			return page, directReadError(e)
		}
		page.HasMore = len(items) > in.Limit
		if page.HasMore {
			items = items[:in.Limit]
			page.NextCursor = items[len(items)-1].ID.String()
		}
		page.Items = items
		return page, nil
	})
}

func (m *Module) agentDirectMessagesHTTP(ctx context.Context, in *AgentDirectMessagesInput) (*ProtocolBody[Page[collab.MessageView]], error) {
	tenant, agent, err := pathIDs(ctx, in.AgentPath)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	channel, err := guid.RequireID(in.ChannelID)
	if err != nil {
		return nil, agentHTTPError(ctx, ErrInvalid)
	}
	before, err := directPosition(in.Cursor)
	if err != nil {
		return nil, agentHTTPError(ctx, err)
	}
	return readHTTP(ctx, m, func(tx *gorm.DB) (Page[collab.MessageView], error) {
		page := Page[collab.MessageView]{Items: []collab.MessageView{}, Next: []Next{}}
		if _, e := m.authorize(ctx, tx, tenant, agent, "agent_workspace_read", true); e != nil {
			return page, e
		}
		if in.Limit < 1 || in.Limit > 100 {
			return page, ErrInvalid
		}
		if m.directReader.MessagesOn == nil {
			return page, ErrUnavailable
		}
		items, e := m.directReader.MessagesOn(ctx, tx, tenant, agent, channel, before, in.Limit+1)
		if e != nil {
			return page, directReadError(e)
		}
		page.HasMore = len(items) > in.Limit
		if page.HasMore {
			items = items[:in.Limit]
			page.NextCursor = items[len(items)-1].ID.String()
		}
		page.Items = items
		return page, nil
	})
}
