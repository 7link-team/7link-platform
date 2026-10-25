package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

type SkillPage struct {
	Items      []SkillView `json:"items"`
	HasMore    bool        `json:"has_more"`
	NextOffset int         `json:"next_offset"`
}
type SkillVersionPage struct {
	Items      []SkillVersionView `json:"items"`
	HasMore    bool               `json:"has_more"`
	NextOffset int                `json:"next_offset"`
}
type AgentSkillPage struct {
	Items      []AgentSkillView `json:"items"`
	HasMore    bool             `json:"has_more"`
	NextOffset int              `json:"next_offset"`
}
type skillListInput struct {
	KnowledgeTenantInput
	Offset int    `query:"offset" default:"0" minimum:"0"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Search string `query:"search" maxLength:"200"`
	State  string `query:"state" enum:"ACTIVE,ARCHIVED"`
}
type SkillPathInput struct {
	KnowledgeTenantInput
	SkillID string `path:"skill_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type SkillVersionPathInput struct {
	KnowledgeTenantInput
	VersionID string `path:"version_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type skillVersionListInput struct {
	SkillPathInput
	Offset int    `query:"offset" default:"0" minimum:"0"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Search string `query:"search" maxLength:"200"`
}
type skillCreateHTTPInput struct {
	KnowledgeTenantInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SkillContentInput
}
type skillAddVersionHTTPInput struct {
	SkillPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SkillContentInput
}
type skillStateHTTPInput struct {
	SkillPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SkillStateInput
}
type skillReviewHTTPInput struct {
	SkillVersionPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SkillReviewInput
}
type SkillAgentPathInput struct {
	KnowledgeTenantInput
	AgentPrincipalID string `path:"agent_principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type skillAgentListInput struct {
	SkillAgentPathInput
	Offset int    `query:"offset" default:"0" minimum:"0"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Search string `query:"search" maxLength:"200"`
}
type SkillMountPathInput struct {
	SkillAgentPathInput
	SkillID string `path:"skill_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type SkillMountInput struct {
	PinnedVersionID *guid.ID `json:"pinned_version_id,omitempty"`
}
type skillMountHTTPInput struct {
	SkillMountPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body           SkillMountInput
}
type skillUnmountHTTPInput struct {
	SkillMountPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
}

func (m *Module) skillsOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in skillListInput) (SkillPage, error) {
	out := SkillPage{Items: []SkillView{}}
	if in.Offset < 0 || in.Limit < 1 || in.Limit > 200 || in.State != "" && in.State != "ACTIVE" && in.State != "ARCHIVED" {
		return out, ErrInvalid
	}
	if _, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_read", false); err != nil {
		return out, err
	}
	q := query.BindSkills(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant})
	if in.State != "" {
		q = q.Where(map[string]any{"state": in.State})
	}
	if in.Search != "" {
		q = q.Where(clause.Or(clause.Like{Column: "skill_key", Value: "%" + in.Search + "%"}, clause.Like{Column: "description", Value: "%" + in.Search + "%"}))
	}
	var rows []model.Skill
	err := q.Order(clause.OrderByColumn{Column: clause.Column{Name: "skill_key"}}).Offset(in.Offset).Limit(in.Limit + 1).Find(&rows).Error
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > in.Limit
	if out.HasMore {
		rows = rows[:in.Limit]
	}
	for _, r := range rows {
		out.Items = append(out.Items, skillView(&r))
	}
	out.NextOffset = in.Offset + len(rows)
	return out, nil
}
func (m *Module) skillVersionsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, offset, limit int, search string) (SkillVersionPage, error) {
	out := SkillVersionPage{Items: []SkillVersionView{}}
	if offset < 0 || limit < 1 || limit > 200 {
		return out, ErrInvalid
	}
	if _, _, err := m.skillOn(ctx, tx, tenant, id, "knowledge_read", false); err != nil {
		return out, err
	}
	q := query.BindSkillVersions(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "skill_id": id})
	if search != "" {
		q = q.Where(clause.Like{Column: "content", Value: "%" + search + "%"})
	}
	var rows []model.SkillVersion
	err := q.Order(clause.OrderByColumn{Column: clause.Column{Name: "version_no"}, Desc: true}).Offset(offset).Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, r := range rows {
		out.Items = append(out.Items, skillVersionView(&r, false))
	}
	out.NextOffset = offset + len(rows)
	return out, nil
}
func skillError(ctx context.Context, err error) (error, bool) {
	if errors.Is(err, ErrDistillationNotAccepted) {
		return respx.ErrStatus(ctx, 422, 150423, "knowledge.skill_distillation_not_accepted", nil), true
	}
	var validation *SkillFrontmatterError
	if errors.As(err, &validation) {
		return respx.ErrStatus(ctx, 422, 150427, "knowledge.skill_frontmatter", map[string]any{"errors": validation.Errors}), true
	}
	if errors.Is(err, ErrSkillNotMountable) {
		return respx.ErrStatus(ctx, 422, 150428, "knowledge.skill_not_mountable", nil), true
	}
	return nil, false
}
func (m *Module) registerSkillsHTTP(api huma.API) error {
	op := func(id, method, path, summary string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"tenants"}, DefaultStatus: status, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}
	}
	read, write := authz.Guard{Resource: "knowledge", Verb: "read"}, authz.Guard{Resource: "knowledge", Verb: "write"}
	authz.Register(m.registrar, api, op("knowledge.skills.list", http.MethodGet, "/skills", "List versioned skills with offset pagination", 200), read, func(ctx context.Context, in *skillListInput) (*respx.Body[SkillPage], error) {
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, t guid.ID) (SkillPage, error) { return m.skillsOn(ctx, tx, t, *in) })
	})
	authz.Register(m.registrar, api, op("knowledge.skills.create", http.MethodPost, "/skills", "Validate SKILL.md and create an immutable draft version", 201), write, func(ctx context.Context, in *skillCreateHTTPInput) (*lifecycleOutput[SkillResult], error) {
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.skills.create", http.MethodPost, "/skills", map[string]string{"tenant_id": in.TenantID}, in.Body, 201, func(tx *gorm.DB, t guid.ID) (SkillResult, error) { return m.createSkillOn(ctx, tx, t, in.Body) }, func(tx *gorm.DB, t guid.ID, _ SkillResult) error {
			_, e := m.authorize(ctx, tx, t, Scope{Scope: "TEAM"}, "knowledge_write", true)
			return e
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skills.get", http.MethodGet, "/skills/{skill_id}", "Read a skill and its current approved version", 200), read, func(ctx context.Context, in *SkillPathInput) (*respx.Body[SkillView], error) {
		id, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, t guid.ID) (SkillView, error) {
			s, _, e := m.skillOn(ctx, tx, t, id, "knowledge_read", false)
			if e != nil {
				return SkillView{}, e
			}
			v := skillView(s)
			if s.CurrentVersionID != nil {
				r, e := m.skillVersionOn(ctx, tx, t, *s.CurrentVersionID)
				if e != nil {
					return v, e
				}
				current := skillVersionView(r, true)
				v.CurrentVersion = &current
			}
			return v, nil
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skills.update", http.MethodPatch, "/skills/{skill_id}", "Archive or restore a skill with a current version", 200), write, func(ctx context.Context, in *skillStateHTTPInput) (*lifecycleOutput[SkillView], error) {
		id, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.skills.update", http.MethodPatch, "/skills/{skill_id}", map[string]string{"tenant_id": in.TenantID, "skill_id": in.SkillID}, in.Body, 200, func(tx *gorm.DB, t guid.ID) (SkillView, error) { return m.skillStateOn(ctx, tx, t, id, in.Body) }, func(tx *gorm.DB, t guid.ID, _ SkillView) error {
			_, _, e := m.skillOn(ctx, tx, t, id, "knowledge_write", false)
			return e
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skills.versions.list", http.MethodGet, "/skills/{skill_id}/versions", "List immutable skill version metadata", 200), read, func(ctx context.Context, in *skillVersionListInput) (*respx.Body[SkillVersionPage], error) {
		id, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, t guid.ID) (SkillVersionPage, error) {
			return m.skillVersionsOn(ctx, tx, t, id, in.Offset, in.Limit, in.Search)
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skills.versions.create", http.MethodPost, "/skills/{skill_id}/versions", "Add a validated draft version without changing current mounts", 201), write, func(ctx context.Context, in *skillAddVersionHTTPInput) (*lifecycleOutput[SkillResult], error) {
		id, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.skills.versions.create", http.MethodPost, "/skills/{skill_id}/versions", map[string]string{"tenant_id": in.TenantID, "skill_id": in.SkillID}, in.Body, 201, func(tx *gorm.DB, t guid.ID) (SkillResult, error) { return m.addSkillVersionOn(ctx, tx, t, id, in.Body) }, func(tx *gorm.DB, t guid.ID, _ SkillResult) error {
			_, _, e := m.skillOn(ctx, tx, t, id, "knowledge_write", false)
			return e
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skill_versions.get", http.MethodGet, "/skill-versions/{version_id}", "Read immutable skill content and validation metadata", 200), read, func(ctx context.Context, in *SkillVersionPathInput) (*respx.Body[SkillVersionView], error) {
		id, e := guid.RequireID(in.VersionID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, t guid.ID) (SkillVersionView, error) {
			if _, e := m.authorize(ctx, tx, t, Scope{Scope: "TEAM"}, "knowledge_read", false); e != nil {
				return SkillVersionView{}, e
			}
			v, e := m.skillVersionOn(ctx, tx, t, id)
			if e != nil {
				return SkillVersionView{}, e
			}
			return skillVersionView(v, true), nil
		})
	})
	authz.Register(m.registrar, api, op("knowledge.skill_versions.review", http.MethodPost, "/skill-versions/{version_id}/reviews", "Human approval or rejection of a draft skill version", 201), write, func(ctx context.Context, in *skillReviewHTTPInput) (*lifecycleOutput[SkillVersionView], error) {
		id, e := guid.RequireID(in.VersionID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.skill_versions.review", http.MethodPost, "/skill-versions/{version_id}/reviews", map[string]string{"tenant_id": in.TenantID, "version_id": in.VersionID}, in.Body, 201, func(tx *gorm.DB, t guid.ID) (SkillVersionView, error) {
			return m.reviewSkillVersionOn(ctx, tx, t, id, in.Body)
		}, func(tx *gorm.DB, t guid.ID, _ SkillVersionView) error {
			_, e := m.authorize(ctx, tx, t, Scope{Scope: "TEAM"}, "knowledge_write", true)
			return e
		})
	})
	authz.Register(m.registrar, api, op("knowledge.agent_skills.list", http.MethodGet, "/agents/{agent_principal_id}/skills", "List mounts including archived skills and resolved version identities", 200), authz.Guard{Resource: "agent", Verb: "read"}, func(ctx context.Context, in *skillAgentListInput) (*respx.Body[AgentSkillPage], error) {
		id, e := guid.RequireID(in.AgentPrincipalID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleRead(ctx, m, in.TenantID, func(tx *gorm.DB, t guid.ID) (AgentSkillPage, error) {
			return m.agentSkillsSearchOn(ctx, tx, t, id, in.Offset, in.Limit, in.Search)
		})
	})
	authz.Register(m.registrar, api, op("knowledge.agent_skills.mount", http.MethodPut, "/agents/{agent_principal_id}/skills/{skill_id}", "Mount a skill following current or pin an approved version", 200), authz.Guard{Resource: "agent", Verb: "manage"}, func(ctx context.Context, in *skillMountHTTPInput) (*lifecycleOutput[AgentSkillView], error) {
		agent, e := guid.RequireID(in.AgentPrincipalID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		skill, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		return lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.agent_skills.mount", http.MethodPut, "/agents/{agent_principal_id}/skills/{skill_id}", map[string]string{"tenant_id": in.TenantID, "agent_principal_id": in.AgentPrincipalID, "skill_id": in.SkillID}, in.Body, 200, func(tx *gorm.DB, t guid.ID) (AgentSkillView, error) {
			return m.mountSkillOn(ctx, tx, t, agent, skill, in.Body.PinnedVersionID)
		}, func(tx *gorm.DB, t guid.ID, _ AgentSkillView) error {
			_, _, e := m.skillAgentOn(ctx, tx, t, agent, "agent_manage")
			return e
		})
	})
	authz.Register(m.registrar, api, op("knowledge.agent_skills.unmount", http.MethodDelete, "/agents/{agent_principal_id}/skills/{skill_id}", "Remove an active skill mount while preserving historical versions", 204), authz.Guard{Resource: "agent", Verb: "manage"}, func(ctx context.Context, in *skillUnmountHTTPInput) (*struct{}, error) {
		agent, e := guid.RequireID(in.AgentPrincipalID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		skill, e := guid.RequireID(in.SkillID)
		if e != nil {
			return nil, knowledgeError(ctx, ErrInvalid)
		}
		_, e = lifecycleCommand(ctx, m, in.TenantID, in.IdempotencyKey, "knowledge.agent_skills.unmount", http.MethodDelete, "/agents/{agent_principal_id}/skills/{skill_id}", map[string]string{"tenant_id": in.TenantID, "agent_principal_id": in.AgentPrincipalID, "skill_id": in.SkillID}, struct{}{}, 204, func(tx *gorm.DB, t guid.ID) (struct{}, error) {
			return struct{}{}, m.unmountSkillOn(ctx, tx, t, agent, skill)
		}, func(tx *gorm.DB, t guid.ID, _ struct{}) error {
			_, _, e := m.skillAgentOn(ctx, tx, t, agent, "agent_manage")
			return e
		})
		return nil, e
	})
	if !m.registrar.IsDeclarationOnly() {
		for _, path := range []string{"/skills", "/skills/{skill_id}", "/skills/{skill_id}/versions", "/skill-versions/{version_id}/reviews", "/agents/{agent_principal_id}/skills/{skill_id}"} {
			if e := authz.RegisterRequestContract(api, "/tenants/{tenant_id}"+path); e != nil {
				return e
			}
		}
	}
	return nil
}
func (m *Module) agentSkillsSearchOn(ctx context.Context, tx *gorm.DB, t, agent guid.ID, offset, limit int, search string) (AgentSkillPage, error) {
	if search == "" {
		return m.agentSkillsOn(ctx, tx, t, agent, offset, limit)
	}
	if offset < 0 || limit < 1 || limit > 200 {
		return AgentSkillPage{}, ErrInvalid
	}
	// The mount cap is thirty; filter the complete bounded set before pagination.
	all, err := m.agentSkillsOn(ctx, tx, t, agent, 0, 200)
	if err != nil {
		return all, err
	}
	items := []AgentSkillView{}
	search = strings.ToLower(search)
	for _, r := range all.Items {
		if strings.Contains(strings.ToLower(r.Skill.SkillKey+" "+r.Skill.Description), search) {
			items = append(items, r)
		}
	}
	out := AgentSkillPage{Items: []AgentSkillView{}, NextOffset: offset}
	if offset >= len(items) {
		return out, nil
	}
	end := min(offset+limit, len(items))
	out.Items = items[offset:end]
	out.NextOffset = end
	out.HasMore = end < len(items)
	return out, nil
}
