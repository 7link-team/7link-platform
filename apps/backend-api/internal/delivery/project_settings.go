package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
	"time"
)

func (m *Module) injectSettingsMembers(s *module.Services) error {
	v, e := module.Resolve[core.ProjectMemberBindings](s)
	if errors.Is(e, module.ErrServiceMissing) {
		return nil
	}
	if e == nil {
		m.settingsMembers = v
	}
	return e
}

// Repository IDs remain stable; omission from a replacement archives a repository.
type ProjectRepositorySetting struct {
	ID                *guid.ID `json:"id,omitempty"`
	Name              string   `json:"name" minLength:"1" maxLength:"100"`
	RemoteURL         string   `json:"remote_url" minLength:"1" maxLength:"2000"`
	DefaultBranch     string   `json:"default_branch" minLength:"1" maxLength:"200"`
	ProtectedBranches []string `json:"protected_branches" maxItems:"20"`
	IsPrimary         bool     `json:"is_primary"`
}
type ProjectMemberSetting struct {
	PrincipalID guid.ID `json:"principal_id"`
	RoleKey     string  `json:"role_key" minLength:"1"`
}

func (in *ClientSettingsPatch) MemberPrincipals() []guid.ID {
	ids := []guid.ID{}
	if in == nil {
		return ids
	}
	for _, v := range append(append([]ProjectMemberSetting{}, in.AddMembers...), in.RemoveMembers...) {
		ids = append(ids, v.PrincipalID)
	}
	if in.LeadPrincipalID != nil && *in.LeadPrincipalID > 0 {
		ids = append(ids, *in.LeadPrincipalID)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
func (m *Module) settingsDetailsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, out *ClientSettings) error {
	c := query.BindContacts(tx, m.service.names)
	rows, e := c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project)).Order(c.IntakeContactID).Find()
	if e != nil {
		return e
	}
	out.Contacts = []projectContactInput{}
	for _, r := range rows {
		receive, accept := r.ReceivesDeliveries, r.CanAccept
		out.Contacts = append(out.Contacts, projectContactInput{r.IntakeContactID, r.Role, &receive, &accept})
	}
	r := query.BindRepositories(tx, m.service.names)
	repos, e := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ArchivedAt.IsNull()).Order(r.ID).Find()
	if e != nil {
		return e
	}
	out.Repositories = []ProjectRepositorySetting{}
	for _, v := range repos {
		var branches []string
		if json.Unmarshal([]byte(v.ProtectedBranches), &branches) != nil {
			return ErrUnavailable
		}
		id := v.ID
		out.Repositories = append(out.Repositories, ProjectRepositorySetting{&id, v.Name, v.RemoteURL, v.DefaultBranch, branches, v.IsPrimary})
	}
	if m.settingsMembers.ListOn == nil {
		return ErrUnavailable
	}
	bindings, e := m.settingsMembers.ListOn(ctx, tx, tenant, project)
	if e != nil {
		return e
	}
	out.Members = []projectRoleBindingView{}
	for _, b := range bindings {
		v := projectRoleBindingView{EntityID: b.EntityID, RoleID: b.RoleID, PrincipalID: b.PrincipalID, Effect: string(b.Effect), CreatedAt: b.CreatedAt}
		if !b.ExpiresAt.IsZero() {
			at := b.ExpiresAt
			v.ExpiresAt = &at
		}
		out.Members = append(out.Members, v)
	}
	return nil
}
func (m *Module) editSettingsDetailsOn(ctx context.Context, tx *gorm.DB, row *model.Project, in *ClientSettingsPatch) error {
	tenant, project, actor := row.TenantID, row.ProjectID, authn.PrincipalIDFromContext(ctx)
	at := time.Now().UTC().Truncate(time.Millisecond)
	if len(in.AddMembers) > 50 || len(in.RemoveMembers) > 50 {
		return ErrInvalidArgument
	}
	if in.Contacts != nil {
		if len(*in.Contacts) > 20 {
			return ErrInvalidArgument
		}
		body := projectCreateBody{Contacts: *in.Contacts}
		primary := false
		for _, v := range body.Contacts {
			if v.Role == "PRIMARY" {
				if primary || v.CanAccept == nil || !*v.CanAccept {
					return ErrInvalidArgument
				}
				primary = true
			}
		}
		for _, c := range *in.Contacts {
			if c.IntakeContactID <= 0 || !slices.Contains([]string{"PRIMARY", "CC"}, c.Role) || c.CanAccept == nil || c.ReceivesDeliveries == nil {
				return ErrInvalidArgument
			}
		}
		if e := m.projectReferencesOn(ctx, tx, tenant, body); e != nil {
			return e
		}
		q := query.BindContacts(tx, m.service.names)
		if _, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Delete(); e != nil {
			return e
		}
		for _, c := range *in.Contacts {
			v := model.ProjectContact{TenantID: tenant, ProjectID: project, IntakeContactID: c.IntakeContactID, Role: c.Role, CanAccept: *c.CanAccept, ReceivesDeliveries: *c.ReceivesDeliveries, CreatedAt: at, UpdatedAt: at, CreatedBy: &actor, UpdatedBy: &actor}
			if e := q.WithContext(ctx).Create(&v); e != nil {
				return e
			}
		}
	}
	if in.Repositories != nil {
		if len(*in.Repositories) > 10 {
			return ErrInvalidArgument
		}
		q := query.BindRepositories(tx, m.service.names)
		old, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).Find()
		if e != nil {
			return e
		}
		existing := map[guid.ID]*model.ProjectRepository{}
		for _, r := range old {
			existing[r.ID] = r
		}
		names := map[string]bool{}
		seen := map[guid.ID]bool{}
		primary := false
		for _, v := range *in.Repositories {
			if strings.TrimSpace(v.Name) == "" || len(v.Name) > 100 || names[v.Name] || !validRepositoryURL(v.RemoteURL) || len(v.RemoteURL) > 2000 || v.DefaultBranch == "" || len(v.DefaultBranch) > 200 || len(v.ProtectedBranches) > 20 || v.IsPrimary && primary {
				return ErrInvalidArgument
			}
			names[v.Name] = true
			primary = primary || v.IsPrimary
			for _, b := range v.ProtectedBranches {
				if strings.TrimSpace(b) == "" || len(b) > 200 {
					return ErrInvalidArgument
				}
			}
			var r *model.ProjectRepository
			if v.ID != nil {
				if *v.ID <= 0 || seen[*v.ID] || existing[*v.ID] == nil {
					return ErrInvalidArgument
				}
				r = existing[*v.ID]
				r.Version++
			} else {
				id, e := m.ids.Next()
				if e != nil {
					return e
				}
				r = &model.ProjectRepository{TenantID: tenant, ProjectID: project, ID: guid.ID(id), Version: 1, CreatedAt: at, CreatedBy: &actor}
			}
			branches, _ := json.Marshal(v.ProtectedBranches)
			r.Name, r.RemoteURL, r.DefaultBranch, r.ProtectedBranches, r.IsPrimary = v.Name, v.RemoteURL, v.DefaultBranch, string(branches), v.IsPrimary
			r.UpdatedAt, r.UpdatedBy = at, &actor
			seen[r.ID] = true
			if v.ID == nil {
				if e = q.WithContext(ctx).Create(r); e != nil {
					return e
				}
			} else {
				if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(r.ID)).Select(q.Name, q.RemoteURL, q.DefaultBranch, q.ProtectedBranches, q.IsPrimary, q.Version, q.UpdatedAt, q.UpdatedBy).Updates(r); e != nil {
					return e
				}
			}
		}
		for id := range existing {
			if !seen[id] {
				if _, e = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).UpdateSimple(q.ArchivedAt.Value(at), q.IsPrimary.Value(false), q.Version.Add(1), q.UpdatedAt.Value(at), q.UpdatedBy.Value(actor)); e != nil {
					return e
				}
			}
		}
	}
	if len(in.AddMembers) > 0 || len(in.RemoveMembers) > 0 {
		if m.settingsMembers.ListOn == nil || m.settingsMembers.DeleteOn == nil || m.service.roles.PutOn == nil || m.roles.RoleIDOn == nil {
			return ErrUnavailable
		}
		// Reuse creation's actual Human/Agent role mapping, without granting the editor a new role.
		for _, v := range in.AddMembers {
			bs, e := m.projectMembersOn(ctx, tx, tenant, actor, []projectMemberInput{{v.PrincipalID, v.RoleKey}})
			if e != nil {
				return e
			}
			wanted, e := m.roles.RoleIDOn(ctx, tx, tenant, v.RoleKey)
			if e != nil {
				return e
			}
			current, e := m.settingsMembers.ListOn(ctx, tx, tenant, project)
			if e != nil {
				return e
			}
			for _, b := range current {
				if b.PrincipalID == v.PrincipalID && b.RoleID == wanted && b.Effect == core.BindingDeny {
					return ErrWorkForbidden
				}
			}
			for _, b := range bs {
				if b.principalID != v.PrincipalID || b.roleID != wanted {
					continue
				}
				if _, _, e = m.service.roles.PutOn(ctx, tx, tenant, project, b.roleID, b.principalID, core.BindingAllow, time.Time{}); e != nil {
					return e
				}
			}
		}
		for _, v := range in.RemoveMembers {
			if !slices.Contains([]string{"delivery_lead", "engineer", "agent_executor", "agent_reviewer", "agent_tester", "agent_acceptor", "agent_coordinator", "agent_lead", "agent_assistant"}, v.RoleKey) || v.PrincipalID <= 0 {
				return ErrInvalidMember
			}
			role, e := m.roles.RoleIDOn(ctx, tx, tenant, v.RoleKey)
			if e != nil {
				return e
			}
			a := query.BindAssignments(tx, m.service.names)
			active, e := a.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(a.TenantID.Eq(tenant), a.ProjectID.Eq(project), a.AssigneePrincipalID.Eq(v.PrincipalID), a.Status.Eq("ACTIVE"), a.EndedAt.IsNull()).Limit(1).Find()
			if e != nil {
				return e
			}
			if len(active) > 0 {
				return ErrInvalidArgument
			}
			if v.RoleKey == "delivery_lead" {
				bindings, e := m.settingsMembers.ListOn(ctx, tx, tenant, project)
				if e != nil {
					return e
				}
				other := false
				for _, b := range bindings {
					if b.RoleID == role && b.PrincipalID != v.PrincipalID && b.Effect == core.BindingAllow && (b.ExpiresAt.IsZero() || b.ExpiresAt.After(at)) {
						other = true
					}
				}
				if !other {
					return ErrInvalidArgument
				}
			}
			if row.LeadAgentID != nil && m.agents.ByPrincipalOn == nil {
				return ErrUnavailable
			}
			if m.agents.ByPrincipalOn != nil {
				agent, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, v.PrincipalID)
				if e != nil {
					return e
				}
				if found && row.LeadAgentID != nil && agent.ID == *row.LeadAgentID {
					row.LeadAgentID = nil
					row.DispatchPolicy = "MANUAL"
					row.DispatchPolicyExplicit = true
				}
			}
			if _, e = m.settingsMembers.DeleteOn(ctx, tx, tenant, project, role, v.PrincipalID); e != nil {
				return e
			}
		}
	}
	if in.LeadPrincipalID != nil {
		if *in.LeadPrincipalID == 0 {
			if row.TeamMode == "AUTONOMOUS" {
				return ErrModePreconditions
			}
			row.LeadAgentID = nil
		} else {
			if m.agents.ByPrincipalOn == nil || m.modeGovernance.AgentRoleOn == nil {
				return ErrUnavailable
			}
			a, found, e := m.agents.ByPrincipalOn(ctx, tx, tenant, *in.LeadPrincipalID)
			if e != nil {
				return e
			}
			if !found || a.State != "ACTIVE" || !slices.Contains(a.Roles, "LEAD") {
				return ErrInvalidMember
			}
			member, e := m.members.IsMemberActiveOn(ctx, tx, tenant, a.PrincipalID)
			if e != nil {
				return e
			}
			if !member {
				return ErrInvalidMember
			}
			yes, e := m.modeGovernance.AgentRoleOn(ctx, tx, tenant, project, a.PrincipalID, "agent_lead")
			if e != nil {
				return e
			}
			if !yes {
				return ErrInvalidMember
			}
			row.LeadAgentID = &a.ID
		}
	}
	if in.DispatchPolicy != nil {
		if !slices.Contains([]string{"MANUAL", "AUTO"}, *in.DispatchPolicy) || row.TeamMode == "HUMAN_ONLY" && *in.DispatchPolicy != "MANUAL" {
			return ErrInvalidArgument
		}
		row.DispatchPolicy = *in.DispatchPolicy
		row.DispatchPolicyExplicit = true
	}
	if in.PortalSettings != nil {
		v := in.PortalSettings
		if v.ShowAILabel == nil || v.ShowWorkItemTitles == nil || v.ShowEvidenceSummary == nil || v.ShowReleaseNotes == nil || v.MilestonesVisible == nil || !slices.Contains([]string{"TEAM_ONLY", "ROLES"}, v.ContributorDisplay) {
			return ErrInvalidArgument
		}
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		row.PortalSettings = string(raw)
	}
	if len(in.AddMembers) > 0 || len(in.RemoveMembers) > 0 {
		if m.service.events == nil {
			return ErrUnavailable
		}
		_, e := m.service.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "project.members_changed", Actor: events.Actor{Kind: "HUMAN", ID: actor, AuthoritySource: "IAM:project_manage"}, Subject: events.Subject{Kind: "project", ID: project}, SubjectVersion: int64(row.Version + 1), Data: map[string]any{"added": in.AddMembers, "removed": in.RemoveMembers}})
		if e != nil {
			return e
		}
	}

	return nil
}
