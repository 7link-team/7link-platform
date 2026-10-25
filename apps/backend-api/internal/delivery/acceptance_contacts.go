package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

// RecordedAcceptanceContact resolves a configured current client for a Human
// recording offline evidence. It never changes the authenticated principal.
type RecordedAcceptanceContact func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)

func (m *Module) provideAcceptanceContacts(s *module.Services) error {
	if err := module.Provide[ReleaseNoteDrafters](s, ReleaseNoteDrafters{SelectOn: m.selectReleaseNoteDrafterOn, AuthorizeOn: m.releaseNoteDrafterOn, WriterOn: m.releaseNoteWriterOn}); err != nil {
		return err
	}
	return module.Provide[RecordedAcceptanceContact](s, m.recordedAcceptanceContactOn)
}

// ReleaseNoteDrafters exposes current project role/IAM facts, not model output.
type ReleaseNoteDrafters struct {
	SelectOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error)
	AuthorizeOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	WriterOn    func(context.Context, *gorm.DB, guid.ID, guid.ID) error
}

func (m *Module) releaseNoteWriterOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) error {
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "delivery_read")
	if err != nil {
		return err
	}
	if kind != "AGENT" {
		return ErrWorkForbidden
	}
	return m.releaseNoteDrafterOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx))
}
func (m *Module) releaseNoteDrafterOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID) error {
	if m == nil || m.agents.ByPrincipalOn == nil || m.workPolicy == nil || m.members == nil {
		return ErrUnavailable
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, principal)
	if err != nil {
		return err
	}
	if !found || agent.State != "ACTIVE" || agent.TenantID != tenant || agent.PrincipalID != principal {
		return ErrWorkForbidden
	}
	active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, principal)
	if err != nil {
		return err
	}
	if !active {
		return ErrWorkForbidden
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "delivery_read", principal)
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(project) {
		return ErrWorkForbidden
	}
	q := query.BindProjects(tx, m.service.names)
	p, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).Take()
	if err != nil {
		return err
	}
	if p.LeadAgentID != nil && *p.LeadAgentID == agent.ID {
		return nil
	}
	if m.modeGovernance.AgentRoleOn == nil {
		return ErrUnavailable
	}
	yes, err := m.modeGovernance.AgentRoleOn(ctx, tx, tenant, project, principal, "agent_coordinator")
	if err != nil {
		return err
	}
	if !yes {
		return ErrWorkForbidden
	}
	return nil
}
func (m *Module) selectReleaseNoteDrafterOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (guid.ID, error) {
	p, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "delivery_compose")
	if err != nil {
		return 0, err
	}
	if kind != "HUMAN" {
		return 0, ErrWorkForbidden
	}
	if p.LeadAgentID != nil && m.triageAgents.AgentOn != nil {
		a, found, e := m.triageAgents.AgentOn(ctx, tx, tenant, *p.LeadAgentID)
		if e != nil {
			return 0, e
		}
		if found {
			if e = m.releaseNoteDrafterOn(ctx, tx, tenant, project, a.PrincipalID); e == nil {
				return a.PrincipalID, nil
			}
			if !errors.Is(e, ErrWorkForbidden) {
				return 0, e
			}
		}
	}
	if m.agents.ActiveByRoleOn == nil {
		return 0, ErrUnavailable
	}
	agents, err := m.agents.ActiveByRoleOn(ctx, tx, tenant, "COORDINATOR")
	if err != nil {
		return 0, err
	}
	for _, a := range agents {
		e := m.releaseNoteDrafterOn(ctx, tx, tenant, project, a.PrincipalID)
		if e == nil {
			return a.PrincipalID, nil
		}
		if !errors.Is(e, ErrWorkForbidden) {
			return 0, e
		}
	}
	return 0, ErrUnavailable
}

func (m *Module) recordedAcceptanceContactOn(ctx context.Context, tx *gorm.DB, tenant, project, recipient guid.ID) (guid.ID, error) {
	if authn.TenantIDFromContext(ctx) != tenant || recipient <= 0 {
		return 0, ErrInvalidArgument
	}
	_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), "delivery_send")
	if err != nil {
		return 0, err
	}
	if kind != "HUMAN" {
		return 0, ErrWorkForbidden
	}
	if m.sources.ContactsOn == nil {
		return 0, ErrUnavailable
	}
	q := query.BindContacts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CanAccept.Is(true)).Order(q.IntakeContactID).Limit(21).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 20 {
		return 0, ErrUnavailable
	}
	ids := make([]guid.ID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.IntakeContactID)
	}
	contacts, err := m.sources.ContactsOn(ctx, tx, tenant, ids)
	if err != nil {
		return 0, err
	}
	for _, c := range contacts {
		if c.TenantID == tenant && c.PrincipalID != nil && *c.PrincipalID == recipient && c.State == "ACTIVE" && c.ArchivedAt == nil {
			for _, id := range ids {
				if id == c.ID {
					return c.ID, nil
				}
			}
		}
	}
	return 0, ErrWorkForbidden
}
