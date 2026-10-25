package governance

import (
	"context"
	"errors"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
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
	revoke, err := module.Resolve[core.EntitySubjectBindingRevoker](s)
	if err != nil {
		return err
	}
	return c.Bind(Name, func(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) error {
		if err := m.revokeRetiringAgentOn(ctx, tx, tenant, principal, retirement); err != nil {
			return err
		}
		return revoke(ctx, tx, tenant, principal)
	})
}

func (m *Module) revokeRetiringAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal, retirement guid.ID) error {
	if principal <= 0 || retirement <= 0 {
		return ErrInvalidArgument
	}
	if err := m.delegationAuthorityOn(ctx, tx, tenant, 0, "agent_retire"); err != nil {
		return err
	}
	if m.delegations.agents.ByPrincipalOn == nil || m.seed.audit == nil || m.publisher == nil {
		return ErrUnavailable
	}
	// Serialize with new reservations/delegations using the same identity lock
	// as those commands. The retiring subject need not be an active principal.
	if err := m.principals.Lock(ctx, tx, principal); err != nil {
		return err
	}
	agent, found, err := m.delegations.agents.ByPrincipalOn(ctx, tx, tenant, principal)
	if err != nil {
		return err
	}
	if !found || agent.TenantID != tenant || agent.PrincipalID != principal || (agent.State != "RETIRING" && agent.State != "RETIRED") {
		return ErrForbidden
	}
	note := "Agent retirement: " + retirement.String()
	d := query.BindDelegations(tx, m.seed.names)
	delegations, err := d.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(d.TenantID.Eq(tenant), d.GranteePrincipalID.Eq(principal), d.State.Eq("ACTIVE")).Order(d.ID).Find()
	if err != nil {
		return err
	}
	for _, row := range delegations {
		if err = m.endDelegationRowOn(ctx, tx, row, "REVOKED", "AGENT_RETIRED", note, "IAM:agent_retire"); err != nil {
			return err
		}
	}
	g := query.BindSpawnGrants(tx, m.seed.names)
	grants, err := g.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(g.TenantID.Eq(tenant), g.GranteePrincipalID.Eq(principal), g.State.In("ACTIVE", "EXHAUSTED")).Order(g.ID).Find()
	if err != nil {
		return err
	}
	for _, row := range grants {
		if err = m.endSpawnGrantOn(ctx, tx, row, "REVOKED", "AGENT_RETIRED", note, authn.PrincipalIDFromContext(ctx), "IAM:agent_retire"); err != nil {
			return err
		}
	}
	// A retired child's reservation is released even when its supervisor or
	// grant has expired. Actual spend and daily creation usage are preserved.
	members := query.BindSpawnGrantMembers(tx, m.seed.names)
	member, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(agent.ID)).First()
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil {
		return ErrUnavailable
	}
	if err == nil {
		// Match Release's grant -> member lock order, then reread the member
		// with a current read before deciding whether to append a new event.
		if _, err = g.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(g.TenantID.Eq(tenant), g.ID.Eq(member.GrantID)).First(); err != nil {
			return err
		}
		member, err = members.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(members.TenantID.Eq(tenant), members.AgentID.Eq(agent.ID)).First()
		if err != nil {
			return err
		}
	}
	if err == nil && member.State == "ACTIVE" {
		if err = m.releaseSpawnOn(ctx, tx, tenant, agent.ID, "RETIRED"); err != nil {
			return err
		}
		grant, err := g.WithContext(ctx).Where(g.TenantID.Eq(tenant), g.ID.Eq(member.GrantID)).First()
		if err != nil {
			return err
		}
		if err = m.spawnEventWithAuthorityOn(ctx, tx, grant, "spawn_grant.updated", note+"; child capacity released", authn.PrincipalIDFromContext(ctx), "IAM:agent_retire"); err != nil {
			return err
		}
	}
	return nil
}
