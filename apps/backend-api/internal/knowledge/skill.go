package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	"unicode/utf8"
)

type SkillView struct {
	ID               guid.ID           `json:"id"`
	SkillKey         string            `json:"skill_key"`
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	State            string            `json:"state"`
	CurrentVersionID *guid.ID          `json:"current_version_id,omitempty"`
	CurrentVersion   *SkillVersionView `json:"current_version,omitempty"`
	Version          int               `json:"version"`
	UpdatedAt        time.Time         `json:"updated_at"`
}
type SkillVersionView struct {
	SourceWorkItemID  *guid.ID         `json:"source_work_item_id,omitempty"`
	SourceExecutionID *guid.ID         `json:"source_execution_id,omitempty"`
	ID                guid.ID          `json:"id"`
	SkillID           guid.ID          `json:"skill_id"`
	VersionNo         int              `json:"version_no"`
	Content           string           `json:"content,omitempty"`
	Frontmatter       SkillFrontmatter `json:"frontmatter"`
	ContentHash       string           `json:"content_hash"`
	State             string           `json:"state"`
	Origin            string           `json:"origin"`
	ReviewedBy        *guid.ID         `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time       `json:"reviewed_at,omitempty"`
	ReviewReason      string           `json:"review_reason"`
	CreatedAt         time.Time        `json:"created_at"`
}
type SkillResult struct {
	Skill    SkillView        `json:"skill"`
	Version  SkillVersionView `json:"version"`
	Warnings []string         `json:"warnings"`
}
type SkillContentInput struct {
	Content string `json:"content" minLength:"1" maxLength:"65536"`
}
type SkillStateInput struct {
	Version int    `json:"version" minimum:"1"`
	State   string `json:"state" enum:"ACTIVE,ARCHIVED"`
}
type SkillReviewInput struct {
	Decision string `json:"decision" enum:"APPROVED,REJECTED"`
	Reason   string `json:"reason" maxLength:"2000"`
}

func skillView(r *model.Skill) SkillView {
	return SkillView{ID: r.ID, SkillKey: r.SkillKey, Title: r.Title, Description: r.Description, State: r.State, CurrentVersionID: r.CurrentVersionID, Version: r.Version, UpdatedAt: r.UpdatedAt}
}
func skillVersionView(r *model.SkillVersion, content bool) SkillVersionView {
	f, _, _, _ := validateSkill(r.Content, "")
	v := SkillVersionView{ID: r.ID, SkillID: r.SkillID, VersionNo: r.VersionNo, Frontmatter: f, ContentHash: r.ContentHash, State: r.State, Origin: r.Origin, ReviewedBy: r.ReviewedBy, ReviewedAt: r.ReviewedAt, ReviewReason: r.ReviewReason, CreatedAt: r.CreatedAt}
	v.SourceWorkItemID, v.SourceExecutionID = r.SourceWorkItemID, r.SourceExecutionID
	if content {
		v.Content = r.Content
	}
	return v
}
func skillDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
func (m *Module) skillOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, permission string, lock bool) (*model.Skill, string, error) {
	kind, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, permission, false)
	if err != nil {
		return nil, "", err
	}
	q := query.BindSkills(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": id})
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var r model.Skill
	err = q.Take(&r).Error
	return &r, kind, skillDBError(err)
}
func (m *Module) skillVersionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (*model.SkillVersion, error) {
	var r model.SkillVersion
	err := query.BindSkillVersions(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "id": id}).Take(&r).Error
	return &r, skillDBError(err)
}
func (m *Module) nextSkillID() (guid.ID, error) {
	if m.ids == nil {
		return 0, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err == nil && id <= 0 {
		return 0, ErrUnavailable
	}
	return guid.ID(id), err
}
func (m *Module) createSkillOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, in SkillContentInput) (SkillResult, error) {
	var out SkillResult
	f, raw, warnings, err := validateSkill(in.Content, "")
	if err != nil {
		return out, err
	}
	kind, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_write", true)
	if err != nil {
		return out, err
	}
	var count int64
	if err = query.BindSkills(tx, m.names).WithContext(ctx).Where(map[string]any{"tenant_id": tenant, "skill_key": f.Name}).Count(&count).Error; err != nil {
		return out, err
	}
	if count > 0 {
		return out, ErrConflict
	}
	id, err := m.nextSkillID()
	if err != nil {
		return out, err
	}
	now := m.now()
	row := model.Skill{ID: id, TenantID: tenant, SkillKey: f.Name, Title: f.Name, Description: f.Description, State: "ACTIVE", Version: 1, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: now, UpdatedAt: now}
	inserted := query.BindSkills(tx, m.names).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if inserted.Error != nil {
		return out, inserted.Error
	}
	if inserted.RowsAffected != 1 {
		return out, ErrConflict
	}
	v, err := m.insertSkillVersion(ctx, tx, &row, in.Content, raw, 1)
	if err != nil {
		return out, err
	}
	err = m.recordLifecycle(ctx, tx, tenant, 0, id, "knowledge_skill", 1, kind, "knowledge_write", "knowledge.skill_created", map[string]any{"version_id": v.ID, "content_hash": v.ContentHash})
	return SkillResult{Skill: skillView(&row), Version: skillVersionView(v, true), Warnings: warnings}, err
}
func (m *Module) insertSkillVersion(ctx context.Context, tx *gorm.DB, s *model.Skill, content, raw string, n int) (*model.SkillVersion, error) {
	id, err := m.nextSkillID()
	if err != nil {
		return nil, err
	}
	r := model.SkillVersion{ID: id, TenantID: s.TenantID, SkillID: s.ID, VersionNo: n, Content: content, Frontmatter: raw, ContentHash: sourceHash(content), State: "DRAFT", Origin: "MANUAL", CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: m.now()}
	err = query.BindSkillVersions(tx, m.names).Create(&r).Error
	return &r, err
}
func (m *Module) addSkillVersionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SkillContentInput) (SkillResult, error) {
	var out SkillResult
	s, kind, err := m.skillOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return out, err
	}
	if s.State != "ACTIVE" {
		return out, ErrConflict
	}
	_, raw, warnings, err := validateSkill(in.Content, s.SkillKey)
	if err != nil {
		return out, err
	}
	if s.CurrentVersionID != nil {
		v, e := m.skillVersionOn(ctx, tx, tenant, *s.CurrentVersionID)
		if e != nil {
			return out, e
		}
		if v.ContentHash == sourceHash(in.Content) {
			return out, ErrConflict
		}
	}
	var last model.SkillVersion
	err = query.BindSkillVersions(tx, m.names).Where(map[string]any{"tenant_id": tenant, "skill_id": id}).Order(clause.OrderByColumn{Column: clause.Column{Name: "version_no"}, Desc: true}).Take(&last).Error
	if err != nil {
		return out, err
	}
	v, err := m.insertSkillVersion(ctx, tx, s, in.Content, raw, last.VersionNo+1)
	if err != nil {
		return out, err
	}
	err = m.recordLifecycle(ctx, tx, tenant, 0, id, "knowledge_skill", v.VersionNo, kind, "knowledge_write", "knowledge.skill_version_created", map[string]any{"version_id": v.ID, "content_hash": v.ContentHash})
	return SkillResult{Skill: skillView(s), Version: skillVersionView(v, true), Warnings: warnings}, err
}
func (m *Module) reviewSkillVersionOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SkillReviewInput) (SkillVersionView, error) {
	var out SkillVersionView
	if in.Decision != "APPROVED" && in.Decision != "REJECTED" || utf8.RuneCountInString(in.Reason) > 2000 {
		return out, ErrInvalid
	}
	// Authorize before reading content, then serialize every version transition on its parent.
	if _, err := m.authorize(ctx, tx, tenant, Scope{Scope: "TEAM"}, "knowledge_write", true); err != nil {
		return out, err
	}
	v, err := m.skillVersionOn(ctx, tx, tenant, id)
	if err != nil {
		return out, err
	}
	s, kind, err := m.skillOn(ctx, tx, tenant, v.SkillID, "knowledge_write", true)
	if err != nil {
		return out, err
	}
	v, err = m.skillVersionOn(ctx, tx, tenant, id)
	if err != nil {
		return out, err
	}
	if s.State != "ACTIVE" || v.State != "DRAFT" {
		return out, ErrConflict
	}
	now, actor := m.now(), authn.PrincipalIDFromContext(ctx)
	result := query.BindSkillVersions(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": id, "state": "DRAFT"}).Updates(map[string]any{"state": in.Decision, "reviewed_by": actor, "reviewed_at": now, "review_reason": strings.TrimSpace(in.Reason)})
	if result.Error != nil {
		return out, result.Error
	}
	if result.RowsAffected != 1 {
		return out, ErrConflict
	}
	if in.Decision == "APPROVED" {
		if s.CurrentVersionID != nil {
			current, e := m.skillVersionOn(ctx, tx, tenant, *s.CurrentVersionID)
			if e != nil {
				return out, e
			}
			if current.VersionNo >= v.VersionNo {
				return out, ErrConflict
			}
		}
		f, _, _, e := validateSkill(v.Content, s.SkillKey)
		if e != nil {
			return out, e
		}
		if err = query.BindSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": s.ID}).Updates(map[string]any{"current_version_id": v.ID, "description": f.Description, "version": s.Version + 1, "updated_at": now}).Error; err != nil {
			return out, err
		}
	}
	v.State, v.ReviewedAt, v.ReviewedBy, v.ReviewReason = in.Decision, &now, &actor, strings.TrimSpace(in.Reason)
	event := "skill_version.approved"
	if in.Decision == "REJECTED" {
		event = "skill_version.rejected"
	}
	err = m.recordLifecycle(ctx, tx, tenant, 0, s.ID, "knowledge_skill", v.VersionNo, kind, "knowledge_write", event, map[string]any{"skill_id": s.ID, "skill_key": s.SkillKey, "version_id": v.ID, "version_no": v.VersionNo, "content_hash": v.ContentHash, "reason": v.ReviewReason})
	return skillVersionView(v, true), err
}
func (m *Module) skillStateOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, in SkillStateInput) (SkillView, error) {
	s, kind, err := m.skillOn(ctx, tx, tenant, id, "knowledge_write", true)
	if err != nil {
		return SkillView{}, err
	}
	if in.Version < 1 || in.State != "ACTIVE" && in.State != "ARCHIVED" {
		return SkillView{}, ErrInvalid
	}
	if s.Version != in.Version {
		return SkillView{}, ErrConflict
	}
	s.State, s.Version, s.UpdatedAt = in.State, s.Version+1, m.now()
	if err = query.BindSkills(tx, m.names).Where(map[string]any{"tenant_id": tenant, "id": id, "version": in.Version}).Updates(map[string]any{"state": s.State, "version": s.Version, "updated_at": s.UpdatedAt}).Error; err != nil {
		return SkillView{}, err
	}
	err = m.recordLifecycle(ctx, tx, tenant, 0, id, "knowledge_skill", s.Version, kind, "knowledge_write", "knowledge.skill_state_changed", map[string]any{"state": s.State})
	return skillView(s), err
}
