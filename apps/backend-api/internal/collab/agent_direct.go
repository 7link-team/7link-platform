package collab

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Agent pairs are project scoped. Both participants must currently be able to
// read and post in that project; tenant membership alone is insufficient.
func (m *Module) agentPairOn(ctx context.Context, tx *gorm.DB, tenant, project, peer guid.ID) error {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || peer <= 0 || actor == peer || authn.TenantIDFromContext(ctx) != tenant {
		return ErrInvalidArgument
	}
	if m.policy == nil {
		return ErrUnavailable
	}
	ids := []guid.ID{actor, peer}
	slices.Sort(ids)
	for _, id := range ids {
		kind, err := m.directParticipantOn(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if kind != "AGENT" {
			return ErrForbidden
		}
		for _, permission := range []string{"channel_read", "channel_post"} {
			c, err := m.policy(ctx, tx, tenant, project, permission, id)
			if err != nil {
				return err
			}
			if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, id.String())) {
				return ErrForbidden
			}
		}
	}
	return nil
}

func (m *Module) createAgentDirectOn(ctx context.Context, tx *gorm.DB, tenant, project, peer guid.ID) (ChannelView, error) {
	if m.ids == nil || m.audit == nil || m.publisher == nil {
		return ChannelView{}, ErrUnavailable
	}
	if err := m.agentPairOn(ctx, tx, tenant, project, peer); err != nil {
		return ChannelView{}, err
	}
	actor := authn.PrincipalIDFromContext(ctx)
	ids := []guid.ID{actor, peer}
	slices.Sort(ids)
	pair := fmt.Sprintf("%s:%s", ids[0], ids[1])
	c := query.BindChannels(tx, m.names)
	row, err := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.Kind.Eq("AGENT_DIRECT"), c.DirectPairKey.Eq(pair)).First()
	if err == nil {
		if row.ArchivedAt != nil {
			return ChannelView{}, ErrForbidden
		}
		if err = m.checkAgentDirectMembersOn(ctx, tx, tenant, project, row.ID); err != nil {
			return ChannelView{}, err
		}
		return channelView(row), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelView{}, err
	}
	id, err := m.ids.Next()
	if err != nil {
		return ChannelView{}, err
	}
	if id <= 0 {
		return ChannelView{}, ErrUnavailable
	}
	now := m.now()
	row = &model.Channel{ID: guid.ID(id), TenantID: tenant, ProjectID: &project, Kind: "AGENT_DIRECT", Name: pair, DirectPairKey: &pair, Visibility: "INTERNAL", Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
	if err = c.WithContext(ctx).Create(row); err != nil {
		return ChannelView{}, err
	}
	for _, principal := range ids {
		member := model.ChannelMember{ChannelID: row.ID, TenantID: tenant, PrincipalID: principal, MemberKind: "AGENT", Role: "MEMBER", Source: "EXPLICIT", JoinedAt: now, CreatedAt: now, UpdatedAt: now, CreatedBy: &actor, UpdatedBy: &actor}
		if err = query.BindMembers(tx, m.names).WithContext(ctx).Create(&member); err != nil {
			return ChannelView{}, err
		}
	}
	if err = m.appendAudit(ctx, tx, tenant, project, row.ID, "collab.channel_updated", 0, ProvisioningAudit{AuthoritySource: "IAM:channel_post"}); err != nil {
		return ChannelView{}, err
	}
	_, err = m.publisher.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "collab.channel_updated", Actor: events.Actor{Kind: "AGENT", ID: actor, AuthoritySource: "IAM:channel_post"}, Subject: events.Subject{Kind: "channel", ID: row.ID}, SubjectVersion: 1, Data: map[string]any{"channel_id": row.ID.String()}})
	view := channelView(row)
	view.CreatedNow = true
	return view, err
}

func (m *Module) checkAgentDirectMembersOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID) error {
	q := query.BindMembers(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel)).Order(q.PrincipalID).Find()
	if err != nil {
		return err
	}
	if len(rows) != 2 {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	peer := guid.ID(0)
	for _, row := range rows {
		if row.External || row.MemberKind != "AGENT" || row.LeftAt != nil {
			return ErrForbidden
		}
		if row.PrincipalID != actor {
			peer = row.PrincipalID
		}
	}
	if rows[0].PrincipalID != actor && rows[1].PrincipalID != actor {
		return ErrForbidden
	}
	return m.agentPairOn(ctx, tx, tenant, project, peer)
}

// The Agents inspection command checks owner/workspace authority first. Project
// private conversations additionally require current project channel access.
func (m *Module) inspectAgentProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	if m.policy == nil {
		return ErrUnavailable
	}
	actor := authn.PrincipalIDFromContext(ctx)
	c, err := m.policy(ctx, tx, tenant, project, "channel_read", actor)
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, actor.String())) {
		return ErrForbidden
	}
	return nil
}
