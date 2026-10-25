package templates

import (
	"context"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderBindingView struct {
	Kind            string  `json:"kind"`
	ProviderID      guid.ID `json:"provider_id"`
	ProviderVersion int     `json:"provider_version"`
}
type ProviderBindingsView struct {
	BindingID guid.ID               `json:"binding_id"`
	ProjectID guid.ID               `json:"project_id"`
	Version   int                   `json:"version"`
	Providers []ProviderBindingView `json:"providers"`
}

func (m *Module) providerBindingsOn(ctx context.Context, tx *gorm.DB, binding *model.Binding) (ProviderBindingsView, error) {
	q := query.BindProviderBindings(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(binding.TenantID), q.BindingID.Eq(binding.ID)).Order(q.ID.Desc()).Limit(1001).Find()
	if err != nil {
		return ProviderBindingsView{}, err
	}
	if len(rows) > 1000 {
		return ProviderBindingsView{}, ErrUnavailable
	}
	out := ProviderBindingsView{BindingID: binding.ID, ProjectID: binding.ProjectID, Version: binding.Version, Providers: []ProviderBindingView{}}
	seen := map[string]bool{}
	for _, row := range rows {
		if !seen[row.Kind] {
			out.Providers = append(out.Providers, ProviderBindingView{row.Kind, row.ProviderID, row.ProviderVersion})
			seen[row.Kind] = true
		}
	}
	slices.SortFunc(out.Providers, func(a, b ProviderBindingView) int {
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	})
	return out, nil
}
func (m *Module) bindProvidersOn(ctx context.Context, tx *gorm.DB, project guid.ID, version int, choices map[string]guid.ID) (ProviderBindingsView, error) {
	if m.ids == nil {
		return ProviderBindingsView{}, ErrUnavailable
	}
	if len(choices) == 0 || len(choices) > len(providerKinds) {
		return ProviderBindingsView{}, ErrInvalid
	}
	binding, err := m.studioAuthorizeOn(ctx, tx, project)
	if err != nil {
		return ProviderBindingsView{}, err
	}
	if binding.Version != version {
		return ProviderBindingsView{}, ErrConflict
	}
	keys := make([]string, 0, len(choices))
	for kind, id := range choices {
		if !slices.Contains(providerKinds, kind) || id <= 0 {
			return ProviderBindingsView{}, ErrInvalid
		}
		keys = append(keys, kind)
	}
	slices.Sort(keys)
	// Provider rows follow project/binding locks in stable ID order.
	ids := []guid.ID{}
	for _, id := range choices {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	providers := map[guid.ID]*model.TemplateProvider{}
	q := query.BindProviders(tx, m.names)
	for _, id := range slices.Compact(ids) {
		row, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(binding.TenantID), q.ID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ProviderBindingsView{}, ErrProviderBinding
		}
		if e != nil {
			return ProviderBindingsView{}, e
		}
		view := m.providerView(row)
		if view.State != "ENABLED" || row.HealthCheckedAt == nil {
			return ProviderBindingsView{}, ErrProviderBinding
		}
		providers[id] = row
	}
	for _, kind := range keys {
		row := providers[choices[kind]]
		if row.Kind != kind {
			return ProviderBindingsView{}, ErrProviderBinding
		}
		id, e := m.ids.Next()
		if e != nil {
			return ProviderBindingsView{}, e
		}
		receipt := &model.TemplateProviderBinding{TenantID: binding.TenantID, ID: guid.ID(id), BindingID: binding.ID, ProjectID: project, Kind: kind, ProviderID: row.ID, ProviderVersion: row.Version, CreatedBy: authn.PrincipalIDFromContext(ctx), CreatedAt: time.Now().UTC()}
		if e = query.BindProviderBindings(tx, m.names).WithContext(ctx).Create(receipt); e != nil {
			return ProviderBindingsView{}, e
		}
	}
	updated := tx.WithContext(ctx).Table(m.names.Bindings.String()).Where("tenant_id=? AND id=? AND version=?", binding.TenantID, binding.ID, version).Update("version", version+1)
	if updated.Error != nil {
		return ProviderBindingsView{}, updated.Error
	}
	if updated.RowsAffected != 1 {
		return ProviderBindingsView{}, ErrConflict
	}
	binding.Version++
	if err = m.providerEventOn(ctx, tx, binding.TenantID, binding.ID, "template.provider_binding_changed", map[string]any{"project_id": project, "provider_ids": choices, "binding_version": binding.Version}); err != nil {
		return ProviderBindingsView{}, err
	}
	return m.providerBindingsOn(ctx, tx, binding)
}
