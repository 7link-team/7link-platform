package collab

import (
	"context"
	"errors"

	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) injectRetirement(s *module.Services) error {
	c, err := module.Resolve[fleet.AgentRetirementRevocationConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Bind == nil {
		return ErrUnavailable
	}
	return c.Bind(Name, m.retireChannelMembershipsOn)
}

// Retirement ends all membership kinds, including derived/default memberships
// and immutable direct pairs, without deleting conversation history. The Human
// peer's membership and read marker remain intact. This lifecycle command uses
// current Human agent_retire authority instead of channel-owner impersonation.
func (m *Module) retireChannelMembershipsOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) error {
	if tx == nil || tx.Statement == nil || principal <= 0 || retirement <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || tenant <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrForbidden
	}
	if _, err := m.humanOn(ctx, tx, tenant, actor); err != nil {
		return err
	}
	if m.tenantPolicy == nil || m.agents.ByPrincipalOn == nil {
		return ErrUnavailable
	}
	constraint, err := m.tenantPolicy(ctx, tx, tenant, "agent_retire", actor)
	if err != nil {
		return err
	}
	if !constraint.AllowAll {
		return ErrForbidden
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, principal)
	if err != nil {
		return err
	}
	if !found || a.TenantID != tenant || a.PrincipalID != principal || (a.State != "RETIRING" && a.State != "RETIRED") {
		return ErrForbidden
	}
	q := query.BindMembers(tx, m.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.LeftAt.IsNull()).Order(q.ChannelID).Find()
	if err != nil {
		return err
	}
	c := query.BindChannels(tx, m.names)
	for _, member := range rows {
		channel, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(member.ChannelID)).First()
		if err != nil {
			return err
		}
		now := m.now()
		if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(member.ChannelID), q.PrincipalID.Eq(principal), q.LeftAt.IsNull()).Updates(map[string]any{"left_at": now, "updated_at": now, "updated_by": actor}); err != nil {
			return err
		}
		project := guid.ID(0)
		if channel.ProjectID != nil {
			project = *channel.ProjectID
		}
		if err = m.channelEvent(ctx, tx, tenant, project, member.ChannelID, "collab.member_updated", "agent_retire", principal); err != nil {
			return err
		}
	}
	return nil
}
