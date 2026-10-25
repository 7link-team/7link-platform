package collab

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

type ChannelDispatchRequest struct {
	TenantID, ProjectID, ChannelID, SourceMessageID, RequestedBy, WorkItemID guid.ID
	ExpectedWorkVersion                                                      int
	CheckOnly                                                                bool
}
type ChannelDispatchResult struct {
	ProposalID    guid.ID `json:"proposal_id"`
	State         string  `json:"state"`
	RequiresHuman bool    `json:"requires_human"`
}
type ChannelDispatchProposer func(context.Context, *gorm.DB, ChannelDispatchRequest) (ChannelDispatchResult, error)
type ChannelDispatchFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (map[string]any, error)
type ChannelDispatchConnector struct {
	Bind      func(ChannelDispatchProposer) error
	BindFacts func(ChannelDispatchFacts) error
}

func (m *Module) provideChannelDispatch(s *module.Services) error {
	return module.Provide[ChannelDispatchConnector](s, ChannelDispatchConnector{Bind: func(proposer ChannelDispatchProposer) error {
		if proposer == nil || m.channelDispatch != nil {
			return ErrInvalidArgument
		}
		m.channelDispatch = proposer
		return nil
	}, BindFacts: func(reader ChannelDispatchFacts) error {
		if reader == nil || m.channelDispatchFacts != nil {
			return ErrInvalidArgument
		}
		m.channelDispatchFacts = reader
		return nil
	}})
}
func (m *Module) channelDispatchOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, message, work guid.ID, version int, checkOnly ...bool) (ChannelDispatchResult, error) {
	var empty ChannelDispatchResult
	source, e := m.conversionSourceOn(ctx, tx, tenant, project, channel, message)
	if e != nil {
		return empty, e
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if source.AuthorID == actor || source.WithdrawnAt != nil {
		return empty, ErrForbidden
	}
	members := query.BindMembers(tx, m.names)
	author, e := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ChannelID.Eq(channel), members.PrincipalID.Eq(source.AuthorID), members.LeftAt.IsNull()).First()
	if e != nil || author.MemberKind != "HUMAN" || author.External {
		return empty, ErrForbidden
	}
	mentions := query.BindMentions(tx, m.names)
	if _, e = mentions.WithContext(ctx).Where(mentions.TenantID.Eq(tenant), mentions.ChannelID.Eq(channel), mentions.MessageID.Eq(message), mentions.PrincipalID.Eq(actor)).First(); e != nil {
		return empty, ErrForbidden
	}
	if m.channelDispatch == nil {
		return empty, ErrUnavailable
	}
	return m.channelDispatch(ctx, tx, ChannelDispatchRequest{TenantID: tenant, ProjectID: project, ChannelID: channel, SourceMessageID: message, RequestedBy: source.AuthorID, WorkItemID: work, ExpectedWorkVersion: version, CheckOnly: len(checkOnly) > 0 && checkOnly[0]})
}
