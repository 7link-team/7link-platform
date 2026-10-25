package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"

	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

// SkillCatalog resolves approved immutable content for future executions. Agent
// identifiers use the platform's principal-keyed Agent API convention. Consumers
// freeze the returned version/hash/content in their execution transaction and
// separately enforce tool grants; this catalog never asserts runtime delivery.
type SkillCatalog interface {
	ForAgent(context.Context, guid.ID, guid.ID) ([]ResolvedSkill, error)
	// ForAgentOn resolves in the execution owner's transaction; the owner must persist these immutable facts before releasing the transaction.
	ForAgentOn(context.Context, *gorm.DB, guid.ID, guid.ID) ([]ResolvedSkill, error)
}
type ResolvedSkill struct {
	SkillID     guid.ID  `json:"skill_id"`
	VersionID   guid.ID  `json:"version_id"`
	SkillKey    string   `json:"skill_key"`
	VersionNo   int      `json:"version_no"`
	Content     string   `json:"content"`
	ContentHash string   `json:"content_hash"`
	Tags        []string `json:"tags"`
}

// SkillMounter mounts inside the caller's transaction. Agent ID is a principal
// ID, matching the public Agent APIs; storage uses the directory's fleet ID.
type SkillMounter interface {
	MountOn(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) error
}
type AgentSkillView struct {
	ID                guid.ID   `json:"id"`
	Skill             SkillView `json:"skill"`
	PinnedVersionID   *guid.ID  `json:"pinned_version_id,omitempty"`
	ResolvedVersionID *guid.ID  `json:"resolved_version_id,omitempty"`
	Archived          bool      `json:"archived"`
}

func (m *Module) provideSkills(s *module.Services) error {
	if err := module.Provide[SkillCatalog](s, m); err != nil {
		return err
	}
	return module.Provide[SkillMounter](s, m)
}
func (m *Module) skillAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, permission string) (guid.ID, string, error) {
	kind, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, permission, permission == "agent_manage")
	if err != nil {
		return 0, "", err
	}
	if principal <= 0 {
		return 0, "", ErrInvalid
	}
	if m.agents.ByPrincipalOn == nil {
		return 0, "", ErrUnavailable
	}
	// Principal locking serializes the per-agent cap, attachment changes and catalog snapshots.
	if m.principals.LockActiveRow == nil {
		return 0, "", ErrUnavailable
	}
	_, active, err := m.principals.LockActiveRow(ctx, tx, principal)
	if err != nil {
		return 0, "", err
	}
	if !active {
		return 0, "", ErrNotFound
	}
	agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, principal)
	if err != nil {
		return 0, "", err
	}
	if !found || agent.TenantID != tenant || agent.PrincipalID != principal || agent.State != "ACTIVE" {
		return 0, "", ErrNotFound
	}
	return agent.ID, kind, nil
}
func (m *Module) mountSkillOn(ctx context.Context, tx *gorm.DB, tenant, principal, skill guid.ID, pin *guid.ID) (AgentSkillView, error) {
	var out AgentSkillView
	agent, kind, err := m.skillAgentOn(ctx, tx, tenant, principal, "agent_manage")
	if err != nil {
		return out, err
	}
	// Managing an agent does not imply the ability to read or mutate the library.
	// Mount authorization is agent_manage; approved content validity is checked below.
	var s model.Skill
	err = query.BindSkills(tx, m.names).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(map[string]any{"tenant_id": tenant, "id": skill}).Take(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && s.State != "ACTIVE" {
		return out, ErrSkillNotMountable
	}
	if err != nil {
		return out, err
	}
	if pin != nil {
		v, e := m.skillVersionOn(ctx, tx, tenant, *pin)
		if e != nil {
			return out, e
		}
		if v.SkillID != skill || v.State != "APPROVED" {
			return out, ErrSkillNotMountable
		}
	}
	q := query.BindAgentSkills(tx, m.names).WithContext(ctx)
	var row model.AgentSkill
	err = q.Where(map[string]any{"tenant_id": tenant, "agent_id": agent, "skill_id": skill, "live_slot": 1}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var count int64
		if err = query.BindAgentSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "agent_id": agent, "live_slot": 1}).Count(&count).Error; err != nil {
			return out, err
		}
		if count >= 30 {
			return out, ErrSkillNotMountable
		}
		id, e := m.nextSkillID()
		if e != nil {
			return out, e
		}
		live := 1
		row = model.AgentSkill{ID: id, TenantID: tenant, AgentID: agent, SkillID: skill, PinnedVersionID: pin, AttachedBy: authn.PrincipalIDFromContext(ctx), AttachedAt: m.now(), LiveSlot: &live}
		err = query.BindAgentSkills(tx, m.names).Create(&row).Error
	} else if err == nil {
		if row.PinnedVersionID == nil && pin == nil || row.PinnedVersionID != nil && pin != nil && *row.PinnedVersionID == *pin {
			return agentSkillView(&row, &s), nil
		}
		err = query.BindAgentSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": row.ID, "live_slot": 1}).Updates(map[string]any{"pinned_version_id": pin, "attached_by": authn.PrincipalIDFromContext(ctx), "attached_at": m.now()}).Error
		row.PinnedVersionID = pin
	}
	if err != nil {
		return out, err
	}
	err = m.recordLifecycle(ctx, tx, tenant, 0, skill, "knowledge_skill", s.Version, kind, "agent_manage", "knowledge.skill_mounted", map[string]any{"agent_id": agent, "agent_principal_id": principal, "pinned_version_id": pin})
	return agentSkillView(&row, &s), err
}
func agentSkillView(row *model.AgentSkill, s *model.Skill) AgentSkillView {
	out := AgentSkillView{ID: row.ID, Skill: skillView(s), PinnedVersionID: row.PinnedVersionID, Archived: s.State == "ARCHIVED"}
	if !out.Archived {
		out.ResolvedVersionID = s.CurrentVersionID
		if row.PinnedVersionID != nil {
			out.ResolvedVersionID = row.PinnedVersionID
		}
	}
	return out
}
func (m *Module) unmountSkillOn(ctx context.Context, tx *gorm.DB, tenant, principal, skill guid.ID) error {
	agent, kind, err := m.skillAgentOn(ctx, tx, tenant, principal, "agent_manage")
	if err != nil {
		return err
	}
	result := query.BindAgentSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "agent_id": agent, "skill_id": skill, "live_slot": 1}).Update("live_slot", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return m.recordLifecycle(ctx, tx, tenant, 0, skill, "knowledge_skill", 1, kind, "agent_manage", "knowledge.skill_unmounted", map[string]any{"agent_id": agent, "agent_principal_id": principal})
}
func (m *Module) MountOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, ids []guid.ID) error {
	unique := slices.Clone(ids)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	if len(unique) > 30 {
		return ErrSkillNotMountable
	}
	if _, _, err := m.skillAgentOn(ctx, tx, tenant, principal, "agent_manage"); err != nil {
		return err
	}
	// Validate every member before any writes; an invalid batch leaves no prefix mounted.
	for _, id := range unique {
		var row model.Skill
		err := query.BindSkills(tx, m.names).WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(map[string]any{"tenant_id": tenant, "id": id, "state": "ACTIVE"}).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSkillNotMountable
		}
		if err != nil {
			return err
		}
	}
	for _, id := range unique {
		if _, err := m.mountSkillOn(ctx, tx, tenant, principal, id, nil); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) agentSkillsOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID, offset, limit int) (AgentSkillPage, error) {
	out := AgentSkillPage{Items: []AgentSkillView{}}
	if offset < 0 || limit < 1 || limit > 200 {
		return out, ErrInvalid
	}
	agent, _, err := m.skillAgentOn(ctx, tx, tenant, principal, "agent_read")
	if err != nil {
		return out, err
	}
	var rows []model.AgentSkill
	err = query.BindAgentSkills(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "agent_id": agent, "live_slot": 1}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Offset(offset).Limit(limit + 1).Find(&rows).Error
	if err != nil {
		return out, err
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		var s model.Skill
		err = query.BindSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": row.SkillID}).Take(&s).Error
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, agentSkillView(&row, &s))
	}
	out.NextOffset = offset + len(rows)
	return out, nil
}
func (m *Module) ForAgent(ctx context.Context, tenant, principal guid.ID) ([]ResolvedSkill, error) {
	db, err := m.database()
	if err != nil {
		return nil, err
	}
	var out []ResolvedSkill
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = m.ForAgentOn(ctx, tx, tenant, principal); return e })
	return out, err
}

// ForAgentOn locks the Agent and skill parents until the caller commits its
// execution snapshot. Approval/archive/mount changes therefore apply only to
// subsequent resolutions. Returned content is a value, never a live reference.
// A successful resolution proves library selection only, not runtime delivery.
func (m *Module) ForAgentOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) ([]ResolvedSkill, error) {
	out := []ResolvedSkill{}
	page, err := m.agentSkillsOn(ctx, tx, tenant, principal, 0, 200)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(page.Items, func(a, b AgentSkillView) int {
		if a.Skill.ID < b.Skill.ID {
			return -1
		}
		if a.Skill.ID > b.Skill.ID {
			return 1
		}
		return 0
	})
	for _, mount := range page.Items {
		s, _, err := m.skillOn(ctx, tx, tenant, mount.Skill.ID, "knowledge_read", true)
		if err != nil {
			return nil, err
		}
		if s.State != "ACTIVE" {
			continue
		}
		resolved := s.CurrentVersionID
		if mount.PinnedVersionID != nil {
			resolved = mount.PinnedVersionID
		}
		if resolved == nil {
			continue
		}
		v, err := m.skillVersionOn(ctx, tx, tenant, *resolved)
		if err != nil {
			return nil, err
		}
		if v.SkillID != s.ID || v.State != "APPROVED" {
			continue
		}
		f, _, _, err := validateSkill(v.Content, s.SkillKey)
		if err != nil {
			return nil, err
		}
		out = append(out, ResolvedSkill{SkillID: s.ID, VersionID: v.ID, SkillKey: s.SkillKey, VersionNo: v.VersionNo, Content: v.Content, ContentHash: v.ContentHash, Tags: f.Tags})
	}
	return out, nil
}

var _ SkillCatalog = (*Module)(nil)
var _ SkillMounter = (*Module)(nil)
