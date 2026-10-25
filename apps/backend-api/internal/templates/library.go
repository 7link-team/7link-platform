package templates

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"gorm.io/gorm"
)

// TemplateFilters apply to individual immutable versions before pagination or
// grouping. Missing applicability metadata never implies a supported team mode.
type TemplateFilters struct {
	Domain   string `query:"domain" enum:"SOFTWARE,VIDEO,NOVEL,TEAM_TASKS"`
	Origin   string `query:"origin" enum:"FIRST_PARTY,TEAM"`
	State    string `query:"state" enum:"DRAFT,PUBLISHED,DEPRECATED,RETIRED"`
	TeamMode string `query:"team_mode" enum:"HUMAN_ONLY,HYBRID,AUTONOMOUS"`
	Key      string `query:"key" maxLength:"64"`
}

func (f TemplateFilters) valid() bool {
	return (f.Domain == "" || slices.Contains([]string{"SOFTWARE", "VIDEO", "NOVEL", "TEAM_TASKS"}, f.Domain)) &&
		(f.Origin == "" || slices.Contains([]string{"FIRST_PARTY", "TEAM"}, f.Origin)) &&
		(f.State == "" || slices.Contains([]string{"DRAFT", "PUBLISHED", "DEPRECATED", "RETIRED"}, f.State)) &&
		(f.TeamMode == "" || slices.Contains([]string{"HUMAN_ONLY", "HYBRID", "AUTONOMOUS"}, f.TeamMode)) &&
		len(f.Key) <= 64 && strings.TrimSpace(f.Key) == f.Key
}
func (f TemplateFilters) matches(v TemplateVersionView) bool {
	if f.Key != "" && v.Key != f.Key {
		return false
	}
	if f.Domain != "" && v.Definition.Domain != f.Domain {
		return false
	}
	if f.TeamMode == "" {
		return true
	}
	switch modes := v.Definition.Metadata["team_modes"].(type) {
	case []any:
		for _, mode := range modes {
			if mode == f.TeamMode {
				return true
			}
		}
	case []string:
		return slices.Contains(modes, f.TeamMode)
	}
	return false
}

// scanLibraryOn never loads an unbounded library into memory. SQL enforces
// tenant, current owner constraints and indexed identity filters. Definition
// metadata is decoded identically on all three dialects, in batches, with the
// cursor advanced only after filtering (no skipped matches or false empty pages).
func (m *Module) scanLibraryOn(ctx context.Context, tx *gorm.DB, f TemplateFilters, after guid.ID, familyAfter string, visit func(TemplateVersionView) bool) error {
	if e := m.authorizeOn(ctx, tx, "template_read", authn.PrincipalIDFromContext(ctx)); e != nil {
		return e
	}
	c, e := m.policy(ctx, tx, authn.TenantIDFromContext(ctx), "template_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return e
	}
	q := query.BindVersions(tx, m.names)
	global := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(0)), q.Origin.Eq("FIRST_PARTY"), q.State.Eq("PUBLISHED"))
	local := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)))
	if !c.AllowAll {
		owners := []driver.Valuer{}
		for _, owner := range c.OwnerIDs {
			id, e := guid.RequireID(owner)
			if e != nil {
				return e
			}
			owners = append(owners, id)
		}
		local = local.Where(q.CreatedBy.In(owners...))
	}
	base := q.WithContext(ctx).Where(local.Or(global))
	if f.Key != "" {
		base = base.Where(q.Key.Eq(f.Key))
	}
	if f.Origin != "" {
		base = base.Where(q.Origin.Eq(f.Origin))
	}
	if f.State != "" {
		base = base.Where(q.State.Eq(f.State))
	}
	for {
		rows, e := base.Where(q.ID.Gt(after)).Order(q.ID).Limit(100).Find()
		if e != nil {
			return e
		}
		for _, r := range rows {
			after = r.ID
			// Use the same byte ordering as family selection on every dialect.
			// Database default collations may order case/accents differently.
			if familyAfter != "" && r.Key <= familyAfter {
				continue
			}
			v, e := versionView(r)
			if e != nil {
				return e
			}
			if f.matches(v) && !visit(v) {
				return nil
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

type familyInput struct {
	TemplateFilters
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"50"`
}
type TemplateFamilyView struct {
	Key             string               `json:"key"`
	Name            string               `json:"name"`
	VersionCount    int                  `json:"version_count" doc:"Count of matching currently authorized versions."`
	Latest          TemplateVersionView  `json:"latest"`
	LatestPublished *TemplateVersionView `json:"latest_published,omitempty"`
}
type TemplateFamilyPage struct {
	Items      []TemplateFamilyView `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

func (m *Module) familiesHTTP(ctx context.Context, in *familyInput) (*respx.Body[TemplateFamilyPage], error) {
	out := TemplateFamilyPage{Items: []TemplateFamilyView{}}
	if !in.TemplateFilters.valid() || in.Limit < 1 || in.Limit > 100 || len(in.Cursor) > 64 || strings.TrimSpace(in.Cursor) != in.Cursor {
		return nil, templateError(ctx, ErrInvalid)
	}
	// Retain at most limit+1 families, even with a large library. Sorting is by
	// stable family key, independently of creation order; versions never split a
	// family across pages. Only currently authorized matching versions contribute.
	families := map[string]*TemplateFamilyView{}
	e := m.transaction(ctx, func(tx *gorm.DB) error {
		return m.scanLibraryOn(ctx, tx, in.TemplateFilters, 0, in.Cursor, func(v TemplateVersionView) bool {
			family := families[v.Key]
			if family == nil {
				families[v.Key] = &TemplateFamilyView{Key: v.Key, Name: v.Name, Latest: v}
				if len(families) > in.Limit+1 {
					keys := make([]string, 0, len(families))
					for key := range families {
						keys = append(keys, key)
					}
					slices.Sort(keys)
					delete(families, keys[len(keys)-1])
				}
				family = families[v.Key]
				if family == nil {
					return true
				}
			}
			family.VersionCount++
			if higherSemver(v.Definition.Semver, family.Latest.Definition.Semver) || (v.Definition.Semver == family.Latest.Definition.Semver && v.ID > family.Latest.ID) {
				family.Latest = v
				family.Name = v.Name
			}
			if v.State == "PUBLISHED" && (family.LatestPublished == nil || higherSemver(v.Definition.Semver, family.LatestPublished.Definition.Semver)) {
				published := v
				family.LatestPublished = &published
			}
			return true
		})
	})
	if e != nil {
		return nil, templateError(ctx, e)
	}
	keys := make([]string, 0, len(families))
	for key := range families {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out.HasMore = len(keys) > in.Limit
	if out.HasMore {
		keys = keys[:in.Limit]
	}
	for _, key := range keys {
		out.Items = append(out.Items, *families[key])
	}
	if out.HasMore {
		out.NextCursor = keys[len(keys)-1]
	}
	return respx.OK(ctx, out), nil
}
